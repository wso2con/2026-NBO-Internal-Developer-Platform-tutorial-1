package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	// No simulated latency in tests, so they stay fast.
	return handlePayment(&ledger{refs: make(map[string]string)},
		config{port: 8080, failRate: 0, latency: 0, logLevel: "info"})
}

func post(t *testing.T, h http.HandlerFunc, key, body string) (int, paymentResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h(rec, req)

	var out paymentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const validBody = `{"application_id":"APP-100244","amount_kes":250000,"wallet_msisdn":"254712345678"}`

// §7.6: first call returns 201 with a PR- prefixed provider_ref.
func TestFirstPaymentReturns201(t *testing.T) {
	h := newTestHandler(t)
	status, resp := post(t, h, "APP-100244", validBody)

	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	if !strings.HasPrefix(resp.ProviderRef, "PR-") {
		t.Errorf("provider_ref = %q, want PR- prefix", resp.ProviderRef)
	}
	if resp.Status != "SENT" {
		t.Errorf("status = %q, want SENT", resp.Status)
	}
	if resp.Duplicate {
		t.Error("duplicate = true on first call, want false")
	}
}

// §7.6: the same idempotency key twice returns 200 with the SAME provider_ref
// and duplicate:true. This is what stops a retry from paying twice.
func TestSameKeyReturnsSameRefAndDuplicateFlag(t *testing.T) {
	h := newTestHandler(t)

	first, firstResp := post(t, h, "APP-100244", validBody)
	if first != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first)
	}

	second, secondResp := post(t, h, "APP-100244", validBody)
	if second != http.StatusOK {
		t.Errorf("second status = %d, want 200", second)
	}
	if secondResp.ProviderRef != firstResp.ProviderRef {
		t.Errorf("provider_ref changed: %q then %q — must be identical",
			firstResp.ProviderRef, secondResp.ProviderRef)
	}
	if !secondResp.Duplicate {
		t.Error("duplicate = false on second call, want true")
	}
}

func TestDifferentKeysGetDifferentRefs(t *testing.T) {
	h := newTestHandler(t)
	_, a := post(t, h, "APP-100244", validBody)
	_, b := post(t, h, "APP-100301", validBody)

	if a.ProviderRef == b.ProviderRef {
		t.Errorf("distinct keys shared a provider_ref: %q", a.ProviderRef)
	}
}

func TestMissingIdempotencyKeyIsRejected(t *testing.T) {
	h := newTestHandler(t)
	status, _ := post(t, h, "", validBody)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func TestMalformedBodyIsRejected(t *testing.T) {
	h := newTestHandler(t)
	status, _ := post(t, h, "APP-1", "{not json")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

// FAIL_RATE=1 must fail every call, so the worker's retry path can be exercised.
func TestFailRateOneAlwaysFails(t *testing.T) {
	h := handlePayment(&ledger{refs: make(map[string]string)},
		config{port: 8080, failRate: 1, latency: 0, logLevel: "info"})
	status, _ := post(t, h, "APP-100244", validBody)
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 with FAIL_RATE=1", status)
	}
}

// --- configuration ----------------------------------------------------------

func TestConfigDefaults(t *testing.T) {
	for _, k := range []string{"PORT", "FAIL_RATE", "LATENCY_MS", "LOG_LEVEL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	// §2.4 default 8080; §7.6 defaults FAIL_RATE 0 and LATENCY_MS 150.
	if cfg.port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.port)
	}
	if cfg.failRate != 0 {
		t.Errorf("failRate = %v, want 0", cfg.failRate)
	}
	if cfg.latency != 150*time.Millisecond {
		t.Errorf("latency = %v, want 150ms", cfg.latency)
	}
}

func TestConfigRejectsBadValues(t *testing.T) {
	cases := []struct{ key, value string }{
		{"PORT", "abc"},
		{"PORT", "99999"},
		{"FAIL_RATE", "2"},
		{"FAIL_RATE", "nope"},
		{"LATENCY_MS", "-1"},
		{"LOG_LEVEL", "verbose"},
	}
	for _, c := range cases {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			t.Setenv(c.key, c.value)
			if _, err := loadConfig(); err == nil {
				t.Errorf("loadConfig() accepted %s=%q, want an error naming the variable",
					c.key, c.value)
			} else if !strings.Contains(err.Error(), c.key) {
				t.Errorf("error %q does not name the variable %s", err, c.key)
			}
		})
	}
}
