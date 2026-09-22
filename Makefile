# Makefile — Kifaru Bank retail lending demo
# The target list in BUILD-SPEC.md §9.3 is the interface. Keep every one of them working.

SHELL := /bin/bash
.DEFAULT_GOAL := help

COMPOSE := docker compose

# The compose project is named `kifaru` (see docker-compose.yml), so its default
# network is kifaru_default. One-off `docker run` containers join it to reach
# services by name.
COMPOSE_NETWORK := kifaru_default

BAL_SERVICES := credit-scoring loan-api
GO_SERVICES  := disbursement-worker arrears-eod payment-rail-stub
CONSOLE      := loan-officer-console
PORTAL       := loan-application-portal

# The six images a developer pushes to a registry for the platform deployment
# (docs/BUILD-IMAGES.md). loan-api-go is NOT here: the platform builds it from
# source. One set of images serves every deployment of this application.
BYOI_SERVICES := credit-scoring payment-rail-stub disbursement-worker arrears-eod \
                 $(CONSOLE) $(PORTAL)
# Most clusters are amd64; an Apple-silicon laptop builds arm64 by default, and
# the mismatch shows up only as a pod dying with "exec format error".
PLATFORM ?= linux/amd64
TAG      ?= v1

# `bal test` executes the module's init(), which validates configuration and exits
# non-zero when a required variable is missing (§3.2). Unit tests are pure and never
# open these connections, but init() still has to be satisfied — so supply
# placeholders here rather than weakening the startup check.
BAL_TEST_ENV := LOG_LEVEL=info \
                DATABASE_URL=postgres://test:test@localhost:5432/test \
                NATS_URL=nats://localhost:4222 \
                CREDIT_SCORING_URL=http://localhost:8081

# Minimums that `doctor` warns below (§9.3)
MIN_CPU  := 4
MIN_MEM  := 8
MIN_DISK := 20

.PHONY: help up down seed smoke test logs reset verify-hardened images push images-multiarch doctor \
        purge-events

help:
	@echo "Kifaru Bank lending demo — make targets (BUILD-SPEC.md §9.3)"
	@echo
	@echo "  up                 build and start everything, wait for healthy"
	@echo "  down               stop and remove volumes"
	@echo "  seed               apply migrations and seed data"
	@echo "  smoke              run demo/local-smoke.sh"
	@echo "  test               bal test + go test ./... in every module + vitest"
	@echo "  logs               follow all service logs"
	@echo "  reset              down, up, seed — a clean slate between dry runs"
	@echo "  purge-events       drop accumulated events and Valkey claims, no teardown"
	@echo "  verify-hardened    run every image read-only, non-root, no capabilities"
	@echo "  images             build the six platform images for PLATFORM (default linux/amd64)"
	@echo "  push               tag and push them to REGISTRY as kifaru-<service>:TAG (default v1)"
	@echo "  images-multiarch   buildx both platforms and push"
	@echo "  doctor             check Colima is running and sized adequately"

# `.env` is gitignored; bootstrap it from the committed example on first use.
.env:
	@cp .env.example .env
	@echo "created .env from .env.example"

up: .env
	$(COMPOSE) up -d --wait
	@echo
	@$(COMPOSE) ps

down:
	$(COMPOSE) down -v --remove-orphans

seed: .env
	$(COMPOSE) run --rm migrate

smoke:
	@if [[ -x demo/local-smoke.sh ]]; then \
		./demo/local-smoke.sh; \
	else \
		echo "demo/local-smoke.sh not present yet — written from phase 3 onward (§10)"; \
	fi

test:
	@set -e; ran=0; \
	for s in $(BAL_SERVICES); do \
		if [[ -f app/$$s/Ballerina.toml && -n "$$(find app/$$s -name '*.bal' -print -quit)" ]]; then \
			echo "==> bal test app/$$s"; \
			(cd app/$$s && $(BAL_TEST_ENV) bal test); ran=1; \
		fi; \
	done; \
	for s in $(GO_SERVICES); do \
		if [[ -f app/$$s/go.mod && -n "$$(find app/$$s -name '*.go' -print -quit)" ]]; then \
			echo "==> go test app/$$s"; (cd app/$$s && go test ./...); ran=1; \
		fi; \
	done; \
	for s in $(CONSOLE) $(PORTAL); do \
		if [[ -f app/$$s/package.json && -d app/$$s/node_modules ]]; then \
			echo "==> vitest app/$$s"; (cd app/$$s && npm test --silent); ran=1; \
		fi; \
	done; \
	if [[ $$ran -eq 0 ]]; then echo "no service code to test yet (phase 1)"; fi

logs:
	$(COMPOSE) logs -f --tail=100

reset:
	@$(MAKE) down
	@$(MAKE) up
	@$(MAKE) seed

# Drops the two pieces of state that survive a database cleanup and then quietly
# break the NEXT run:
#
#   - the LOANS stream, which keeps events for 24h (§8), so old test runs pile up
#   - the Valkey disbursement claims, whose TTL is 7 days (§7.3)
#
# A stale claim is the nastier of the two: delete a loan's rows without clearing
# its claim and the worker suppresses the next legitimate disbursement, which
# looks exactly like a broken worker rather than leftover state.
#
# Unlike `reset` this leaves containers and the database alone — it is the fast
# path between dry runs. Seeded loans and their history are untouched.
purge-events:
	@if ! docker compose ps --status running --services 2>/dev/null | grep -q '^nats$$'; then \
		echo "nats is not running — start the stack with 'make up' first"; exit 1; \
	fi
	@printf 'events before:  '; \
	docker run --rm --network $(COMPOSE_NETWORK) natsio/nats-box:latest \
		nats --server nats:4222 stream info LOANS 2>/dev/null \
		| awk '/^ *Messages:/ {print $$2; found=1} END {if (!found) print "(no LOANS stream)"}'
	@docker run --rm --network $(COMPOSE_NETWORK) natsio/nats-box:latest \
		nats --server nats:4222 stream purge LOANS --force >/dev/null 2>&1 \
		&& echo "events purged" || echo "no LOANS stream to purge"
	@claims=$$(docker compose exec -T valkey valkey-cli --scan --pattern 'disb:*' 2>/dev/null | tr -d '\r' | grep -c . || true); \
	docker compose exec -T valkey valkey-cli --scan --pattern 'disb:*' 2>/dev/null | tr -d '\r' | grep . \
		| xargs -r docker compose exec -T valkey valkey-cli DEL >/dev/null 2>&1 || true; \
	echo "claims cleared: $$claims"
	@printf 'events after:   '; \
	docker run --rm --network $(COMPOSE_NETWORK) natsio/nats-box:latest \
		nats --server nats:4222 stream info LOANS 2>/dev/null \
		| awk '/^ *Messages:/ {print $$2}'

# Services with real dependencies (loan-api needs postgres, NATS and credit-scoring)
# only start if they can reach them, so the hardened run joins the compose network
# and takes the same .env the stack uses. This checks the SECURITY posture — running
# read-only as a non-root uid with no capabilities — not isolation from the network.
VERIFY_RUNTIME_ARGS := --env-file .env --network $(COMPOSE_NETWORK)

# §11: rehearses the Act 4 live edit. Every image must start read-only, non-root,
# with all capabilities dropped. Requires `make up` first, for the network and deps.
#
# Two shapes of component, two definitions of success:
#
#   long-running services  still running after the settle window
#   batch jobs             exited with code 0 — arrears-eod is REQUIRED to run to
#                          completion and exit (§7.4), so "it exited" is the pass
#                          condition, not the failure
#
# The container is deliberately NOT started with --rm: it has to survive long
# enough for its exit code to be read, and is removed explicitly afterwards.
verify-hardened:
	@set -e; found=0; failed=0; \
	for s in $(BAL_SERVICES) $(GO_SERVICES) $(CONSOLE) $(PORTAL); do \
		img="kifaru/$$s:local"; \
		if docker image inspect "$$img" >/dev/null 2>&1; then \
			found=1; \
			printf '%-24s ' "$$s"; \
			docker rm -f "verify-$$s" >/dev/null 2>&1 || true; \
			if docker run -d --name "verify-$$s" \
				--read-only --tmpfs /tmp --user 10001:10001 --cap-drop ALL \
				$(VERIFY_RUNTIME_ARGS) \
				"$$img" >/dev/null 2>&1; then \
				sleep 8; \
				running="$$(docker inspect -f '{{.State.Running}}' verify-$$s 2>/dev/null)"; \
				code="$$(docker inspect -f '{{.State.ExitCode}}' verify-$$s 2>/dev/null)"; \
				if [[ "$$running" == "true" ]]; then \
					echo "PASS (running)"; \
				elif [[ "$$code" == "0" ]]; then \
					echo "PASS (batch job, exited 0)"; \
				else \
					echo "FAIL (exited $$code)"; failed=1; \
					docker logs "verify-$$s" 2>&1 | tail -20; \
				fi; \
				docker rm -f "verify-$$s" >/dev/null 2>&1 || true; \
			else \
				echo "FAIL (would not start)"; failed=1; \
			fi; \
		fi; \
	done; \
	if [[ $$found -eq 0 ]]; then echo "no images built yet (phase 1)"; fi; \
	exit $$failed

# Build the six BYOI images for the platform's node architecture. `${c}` in braces:
# in zsh, $c:local is a modifier (lowercase) and would tag kifaru/<c>ocal.
images:
	@set -e; for s in $(BYOI_SERVICES); do \
		echo "==> buildx $$s ($(PLATFORM))"; \
		docker buildx build --platform $(PLATFORM) --load -t "kifaru/$${s}:local" "app/$$s"; \
	done
	@docker images --format '{{.Repository}}:{{.Tag}}\t{{.Size}}' | grep '^kifaru/'

# Push to YOUR registry. Log in first (docker login / aws ecr get-login-password /
# gh auth token | docker login ghcr.io …). Then put the same REGISTRY into
# manifests/app/05-workloads.yaml (docs/BUILD-IMAGES.md).
push:
	@if [[ -z "$$REGISTRY" ]]; then \
		echo "REGISTRY is required, e.g. make push REGISTRY=ghcr.io/you"; exit 1; \
	fi
	@set -e; for s in $(BYOI_SERVICES); do \
		docker image inspect "kifaru/$${s}:local" >/dev/null 2>&1 || { echo "missing kifaru/$$s:local — run make images"; exit 1; }; \
		arch=$$(docker image inspect "kifaru/$${s}:local" --format '{{.Architecture}}'); \
		echo "==> $$REGISTRY/kifaru-$$s:$(TAG) ($$arch)"; \
		docker tag "kifaru/$${s}:local" "$$REGISTRY/kifaru-$$s:$(TAG)"; \
		docker push "$$REGISTRY/kifaru-$$s:$(TAG)"; \
	done

images-multiarch:
	@if [[ -z "$$REGISTRY" ]]; then \
		echo "REGISTRY is required, e.g. make images-multiarch REGISTRY=ghcr.io/you"; exit 1; \
	fi
	@docker buildx inspect kifaru >/dev/null 2>&1 || docker buildx create --name kifaru --use
	@set -e; for s in $(BAL_SERVICES) $(GO_SERVICES) $(CONSOLE) $(PORTAL); do \
		if [[ -f app/$$s/Dockerfile ]]; then \
			echo "==> buildx $$s"; \
			docker buildx build --builder kifaru \
				--platform linux/amd64,linux/arm64 \
				-t "$$REGISTRY/$$s:local" --push app/$$s; \
		fi; \
	done

# The most common failure on this setup is a stopped or undersized VM, and the symptom
# is an unrelated-looking build error (§9.3).
doctor:
	@fail=0; \
	if ! command -v colima >/dev/null 2>&1; then \
		echo "FAIL  colima not installed"; exit 1; \
	fi; \
	if ! colima status >/dev/null 2>&1; then \
		echo "FAIL  colima is not running — start it with:"; \
		echo "      colima start --cpu 6 --memory 10 --disk 100 --vm-type vz --vz-rosetta --mount-type virtiofs"; \
		exit 1; \
	fi; \
	echo "OK    colima running"; \
	cpu=$$(colima ssh -- nproc 2>/dev/null); \
	mem=$$(colima ssh -- sh -c "free -g | awk '/^Mem:/ {print \$$2}'" 2>/dev/null); \
	disk=$$(colima ssh -- sh -c "df -BG --output=avail /var/lib/docker | tail -1 | tr -dc '0-9'" 2>/dev/null); \
	printf '      cpu=%s  memory=%sGB  free-disk=%sGB\n' "$$cpu" "$$mem" "$$disk"; \
	if [[ -n "$$cpu"  && "$$cpu"  -lt $(MIN_CPU)  ]]; then echo "WARN  cpu $$cpu < $(MIN_CPU)"; fail=1; fi; \
	if [[ -n "$$mem"  && "$$mem"  -lt $(MIN_MEM)  ]]; then echo "WARN  memory $${mem}GB < $(MIN_MEM)GB"; fail=1; fi; \
	if [[ -n "$$disk" && "$$disk" -lt $(MIN_DISK) ]]; then echo "WARN  free disk $${disk}GB < $(MIN_DISK)GB"; fail=1; fi; \
	if ! docker info >/dev/null 2>&1; then echo "FAIL  docker unreachable"; exit 1; fi; \
	echo "OK    docker reachable"; \
	if command -v bal   >/dev/null 2>&1; then echo "OK    bal  $$(bal version | head -1)"; else echo "WARN  bal not installed"; fail=1; fi; \
	if command -v go    >/dev/null 2>&1; then echo "OK    $$(go version)"; else echo "WARN  go not installed"; fail=1; fi; \
	if command -v psql  >/dev/null 2>&1; then echo "OK    $$(psql --version)"; else echo "WARN  psql not installed (needed by make smoke)"; fail=1; fi; \
	if command -v jq    >/dev/null 2>&1; then echo "OK    jq $$(jq --version)"; else echo "WARN  jq not installed (needed by make smoke)"; fail=1; fi; \
	if [[ $$fail -eq 0 ]]; then echo; echo "doctor: clean"; else echo; echo "doctor: warnings above"; fi
