// handlers.go — the HTTP surface (BUILD-SPEC.md §7.1).
//
// Routing is net/http's own pattern matching (Go 1.22+): no framework, so the
// route table is the route table. The flow in postApplications is the same one
// the Ballerina service implements, in the same order, with the same statuses.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

type api struct {
	cfg    config
	log    *slog.Logger
	db     *store
	events *publisher
	score  *scorer
}

func (a *api) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications", a.postApplications)
	mux.HandleFunc("GET /applications/{id}", a.getApplication)
	mux.HandleFunc("GET /applications", a.listApplications)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	return withCORS(mux)
}

// CORS is required by §7.5's design, not an addition to it: the console reads the
// API's address at runtime from /config.js and the browser then calls this API
// DIRECTLY, which is cross-origin whenever the two are served from different
// hosts or ports — as they are locally and on OpenChoreo, where each component
// gets its own route.
//
// "*" is deliberate. This API carries no authentication, no cookies and no
// credentials (§14 puts auth out of scope), so there is no session for a hostile
// origin to ride. If auth is ever added this must become an explicit origin list
// at the same time — a wildcard plus credentials is the combination that bites.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Accept, traceparent")
		h.Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// POST /applications — validate, insert PENDING, score, persist, publish, 201.
func (a *api) postApplications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	traceparent := r.Header.Get("traceparent")

	var req ApplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Message: "malformed JSON body"})
		return
	}

	if errs := validateApplication(req); len(errs) > 0 {
		id := req.ApplicationID
		if id == "" {
			id = "(none)"
		}
		a.log.Warn("application rejected", "application_id", id, "error_count", len(errs))
		writeJSON(w, http.StatusBadRequest, ValidationErrors{Message: "validation failed", Errors: errs})
		return
	}

	applicationID := req.ApplicationID
	if applicationID == "" {
		applicationID = generateApplicationID()
	}

	inserted, err := a.db.insertPending(ctx, applicationID, req)
	if err != nil {
		a.log.Error("insert failed", "application_id", applicationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not persist application"})
		return
	}

	if !inserted {
		// The id already exists. §7.1: return 200 with the existing record, do not
		// re-score, do not re-publish.
		//
		// That applies to a DECIDED record. A PENDING one means a previous attempt
		// got a 503 from scoring and never reached a decision; returning 200 forever
		// would strand the loan. So PENDING falls through and is scored on this
		// attempt. Matches the Ballerina service; raised with the spec author.
		existing, err := a.db.currentDecision(ctx, applicationID)
		if err != nil {
			a.log.Error("duplicate lookup failed", "application_id", applicationID, "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not read application"})
			return
		}
		if existing != "" && existing != "PENDING" {
			a.log.Info("duplicate submission ignored", "application_id", applicationID, "decision", existing)
			record, err := a.db.fetchApplication(ctx, applicationID)
			if err != nil || record == nil {
				writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not read application"})
				return
			}
			writeJSON(w, http.StatusOK, record)
			return
		}
		a.log.Info("retrying a pending application", "application_id", applicationID)
	} else {
		if submitted, err := a.db.fetchApplication(ctx, applicationID); err == nil && submitted != nil {
			if err := a.events.publishSubmitted(submitted, traceparent); err != nil {
				// Non-fatal: loan.submitted is informational. loan.approved is the one
				// the worker consumes, and that failure IS fatal below.
				a.log.Warn("could not publish loan.submitted", "application_id", applicationID, "error", err)
			}
		}
	}

	// Score it. If credit-scoring is unreachable or times out we return 503 and
	// leave the record PENDING, publishing nothing.
	//
	// WE NEVER APPROVE ON A SCORING FAILURE. A banking audience will ask, and the
	// answer has to be that an unavailable risk engine produces no decision at all
	// rather than a permissive default.
	scored, err := a.score.requestScore(ctx, req, applicationID, traceparent)
	if err != nil {
		a.log.Error("scoring unavailable, application left PENDING",
			"application_id", applicationID, "error", err)
		writeJSON(w, http.StatusServiceUnavailable,
			errorBody{Message: "scoring service unavailable, application left pending"})
		return
	}

	if err := a.db.persistDecision(ctx, applicationID, scored.Score, scored.Decision, scored.Reason); err != nil {
		a.log.Error("could not persist decision", "application_id", applicationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not persist decision"})
		return
	}

	decided, err := a.db.fetchApplication(ctx, applicationID)
	if err != nil || decided == nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not read application"})
		return
	}

	a.log.Info("application decided",
		"application_id", applicationID, "score", scored.Score, "decision", scored.Decision)

	if scored.Decision == "APPROVED" {
		if err := a.events.publishApproved(decided, traceparent); err != nil {
			a.log.Error("could not publish loan.approved", "application_id", applicationID, "error", err)
			writeJSON(w, http.StatusInternalServerError,
				errorBody{Message: "decision persisted but could not be published"})
			return
		}
		a.log.Info("loan.approved published", "application_id", applicationID)
	}

	writeJSON(w, http.StatusCreated, decided)
}

// GET /applications/{id}
func (a *api) getApplication(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	found, err := a.db.fetchApplication(ctx, id)
	if err != nil {
		a.log.Error("fetch failed", "application_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not read application"})
		return
	}
	if found == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Message: "no application " + id})
		return
	}

	// Attach the scoring breakdown for the console's detail screen (§7.5).
	// Best-effort: a scoring outage must not make a decided application unreadable.
	if found.Score != nil {
		if factors, err := a.score.scoreFactorsFor(ctx, found); err == nil {
			found.ScoreFactors = factors
		} else {
			a.log.Warn("could not recompute score factors", "application_id", id, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, found)
}

// GET /applications?limit=&bucket=
func (a *api) listApplications(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	bucket := r.URL.Query().Get("bucket")

	rows, err := a.db.listApplications(r.Context(), limit, bucket)
	if err != nil {
		a.log.Error("list failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Message: "could not list applications"})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// Liveness: the process is up. No dependency checks, by design (§7.1).
func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readiness: database and NATS reachable (§7.1).
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := a.db.ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"status": "not ready", "dependency": "database"})
		return
	}
	if err := a.events.ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"status": "not ready", "dependency": "nats"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// §7.1: if application_id is omitted, generate APP- plus six digits.
func generateApplicationID() string {
	return "APP-" + strconv.Itoa(100000+rand.Intn(900000))
}
