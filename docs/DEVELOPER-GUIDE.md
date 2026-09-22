# Developer guide — deploying Kifaru Bank onto the platform

The other half of `PLATFORM-GUIDE.md`. That one builds the platform; this one
deploys the application onto it, as a developer who may only use what the
platform engineer provided.

Everything here is `occ` + YAML. The manifests are in `manifests/app/`, numbered
in apply order:

| File | Step |
|---|---|
| `01-project-lending.yaml` | D1 — the project and its cells |
| `02-resources.yaml` | D2 — postgres, nats, valkey |
| `03-resource-bindings.yaml` | D2 — placement; `occ resource promote` pins the release |
| `04-components.yaml` | D4 — the six pre-built components |
| `05-workloads.yaml` | D5 — **edit**: `<REGISTRY>`, then `API_BASE_URL` ×2 after D8 |
| `06-component-loan-api-go.yaml` | D6 — built from this repo's source |
| `07-releasebinding-credit-scoring-cpu.yaml` | D7 — **edit**: `releaseName` |

Tested against OpenChoreo 1.2.x (`occ` 1.2.5).

```bash
export NS=kifaru-bank
export PROJECT=lending
```

---

## 0. Before you start

**The platform must be in place** (`PLATFORM-GUIDE.md`):

```bash
occ environment list -n $NS                # development / staging / production
occ projecttype list -n $NS                # bank-cell
occ componenttype list -n $NS              # internal-engine event-consumer regulated-batch web-frontend public-api
occ resourcetype list -n $NS               # postgres nats valkey
```

**Six images must be in a registry** your nodes can pull from — `BUILD-IMAGES.md`.
`loan-api-go` is the exception: the platform builds it from source, so the
repository the platform reads must be reachable. `06-component-loan-api-go.yaml`
points at this repository on GitHub; if you fork it, or it is private, change
`repository.url` (and add `secretRef` for a private one — a `SecretReference` in
`$NS` holding git credentials).

**Local tools:** `occ`, `kubectl` against the cluster the cells run on, `psql`
(`brew install libpq && brew link --force libpq`) for D3, `curl`, `jq`.

---

## The shape of what you are about to build

```
project lending                                                     (D1)
  ├── resources      loans-db, loan-events, disb-claims  ResourceType/postgres|nats|valkey   (D2)
  └── components
        loan-api-go              deployment/public-api        built from git by the platform (D6)
        credit-scoring           deployment/internal-engine   image                          (D4/D5)
        payment-rail-stub        deployment/internal-engine   image
        disbursement-worker      deployment/event-consumer    image
        arrears-eod              cronjob/regulated-batch      image
        loan-officer-console     deployment/web-frontend      image
        loan-application-portal  deployment/web-frontend      image
```

Per component at most: a Component, a Workload (pre-built images only), and a
ReleaseBinding per environment.

---

## D1 — Create the project

`occ` has no `create`; it scaffolds, and the scaffold is driven by the platform's
ProjectType schema. The file is already written; this is how it was produced:

```bash
occ project scaffold lending --projecttype bank-cell --namespace $NS -o /tmp/01-project.yaml
```

The two parameters the platform demands — `costCentre` must match
`^CC-[0-9]{4}$`, and `public` is refused:

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Project
metadata:
  name: lending
  namespace: kifaru-bank
spec:
  deploymentPipelineRef:
    name: default
  type:
    kind: ProjectType            # the team's own, namespace-scoped
    name: bank-cell
  parameters:
    costCentre: CC-4417
    dataClassification: confidential
```

The same file contains **one ProjectReleaseBinding per environment** — that is
what creates the cells.

```bash
occ apply -f manifests/app/01-project-lending.yaml
occ projectreleasebinding list -n $NS -p $PROJECT     # -p is NOT optional
```

```
NAME                  PROJECT   ENVIRONMENT   RELEASE              STATUS   AGE
lending-development   lending   development   lending-6bc8b59fd5   Ready    3s
lending-production    lending   production    lending-6bc8b59fd5   Ready    2s
lending-staging       lending   staging       lending-6bc8b59fd5   Ready    2s
```

Note the development cell's namespace — **you will need it and you must not guess
it** (the platform truncates long names):

```bash
occ projectreleasebinding get $PROJECT-development -n $NS
# status.namespace -> dp-kifaru-bank-lending-development-<hash>
```

> **Nothing deploys into an environment until its cell exists.** A component
> created too early sits waiting rather than failing.

---

## D2 — Declare the infrastructure

Three Resources from the team's ResourceTypes. Only `postgres` takes a parameter.
`spec.type` is **immutable**.

```bash
occ apply -f manifests/app/02-resources.yaml
occ resource list -n $NS -p $PROJECT
```

```
NAME          TYPE                    AGE
disb-claims   ResourceType/valkey     4s
loan-events   ResourceType/nats       4s
loans-db      ResourceType/postgres   4s
```

**A Resource does not run anywhere until it is placed in an environment, and
does not deploy until a release is pinned to that placement.** Two acts, two
commands:

```bash
occ apply -f manifests/app/03-resource-bindings.yaml       # placement: owner + environment, no release
occ resourcereleasebinding list -n $NS                     # Synced=False: "spec.resourceRelease is unset"
for r in loans-db loan-events disb-claims; do
  occ resource promote $r --env development -n $NS         # pins status.latestRelease
done
occ resourcereleasebinding list -n $NS                     # no -p flag on this one
```

```
ResourceReleaseBinding 'loans-db-development' promoted to resourcerelease 'loans-db-6dc7b7f7b'
```

The release name is `<resource>-<hash of the type snapshot + parameters>`. Nobody
types it: `promote` reads it from `status.latestRelease` and writes it into the
binding that already exists for that environment. Change a Resource's parameters
and you get a new release — the binding stays on the old one until you promote
again. Same model as components.

All three must reach `Ready` before any component that depends on them will
deploy — a consumer otherwise reports `ResourceDependenciesPending`.

### What each resource hands you

| Resource type | outputs |
|---|---|
| `postgres` | `host`, `port`, `database`, `username`, `password`, **`url`**, `adminURL` |
| `nats` | `host`, `port`, `token`, **`url`**, `adminURL` |
| `valkey` | `host`, `port`, `password`, **`url`**, `connectionString`, `adminURL` |

The `url` output of each is what the application wants — `DATABASE_URL`,
`NATS_URL`, `VALKEY_URL`, the same names `.env.example` uses locally. Bind outputs
by name (D5); a typo is silent until reconcile and shows as `OutputNotResolved`.

---

## D3 — Load the schema and seed

The Resource gives you an empty Postgres. Load the same SQL the compose stack uses:

```bash
cd tutorial-1
./scripts/load-seed.sh                          # or: ./scripts/load-seed.sh staging
# multi-cluster install: KUBE_CONTEXT=<data-plane context> ./scripts/load-seed.sh
```

The script discovers everything, because none of the names are what you would
guess:

| Thing | Is NOT | Actually |
|---|---|---|
| the cell namespace | `kifaru-bank` | `dp-kifaru-bank-lending-development-<hash>` |
| the postgres Service | `loans-db` | `r-loans-db-development-<hash>` — named for the **release** |
| its secret | `loans-db-creds` | `r-loans-db-development-<hash>-creds` |
| the username | `kifaru` | `loans-db-user` — `<resource>-user` |
| the database | — | `kifaru`, as set in `02-resources.yaml` |

It ends by printing the four seeded loans. **Expect 12 / 47 / 78 / 124 days past
due**; due dates anchor to `CURRENT_DATE` at seed time, so re-seed (`--reseed`)
on the day you demo.

---

## D4 — Create the six pre-built components

Scaffold gives you the exact shape:

```bash
occ component scaffold credit-scoring \
  --componenttype deployment/internal-engine \
  -n $NS -p $PROJECT -o /tmp/04-credit-scoring.yaml
```

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: credit-scoring
  namespace: kifaru-bank
spec:
  owner:
    projectName: lending
  componentType:
    kind: ComponentType
    name: deployment/internal-engine     # workloadType/name goes in `name`
  autoDeploy: true                       # deploys to the FIRST environment only
```

**Do not give any of these a `workflow`** — a workflow turns the component into a
source build.

| Component | `componentType.name` |
|---|---|
| `credit-scoring` | `deployment/internal-engine` |
| `payment-rail-stub` | `deployment/internal-engine` |
| `disbursement-worker` | `deployment/event-consumer` |
| `arrears-eod` | `cronjob/regulated-batch` |
| `loan-officer-console` | `deployment/web-frontend` |
| `loan-application-portal` | `deployment/web-frontend` |

```bash
occ apply -f manifests/app/04-components.yaml
occ component list -n $NS -p $PROJECT
```

---

## D5 — Workloads for those six

For a pre-built image you write the Workload yourself. Required fields are
`owner` and `container` (`container.image` required); **`endpoints` is a map keyed
by endpoint name**, each needing `port` and `type`.

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Workload
metadata:
  name: disbursement-worker-workload
  namespace: kifaru-bank
spec:
  owner: { projectName: lending, componentName: disbursement-worker }   # IMMUTABLE
  container:
    image: <REGISTRY>/kifaru-disbursement-worker:v1
    env:
      - { key: LOG_LEVEL, value: info }
      - { key: ADMIN_PORT, value: "9090" }
      - { key: IDEMPOTENCY_TTL_HOURS, value: "168" }
      - { key: PAYMENT_RAIL_API_KEY, value: local-dev-key-not-a-real-secret }
  # NO endpoints block at all — event-consumer refuses any endpoint
  dependencies:
    resources:
      - ref: loans-db
        envBindings: { url: DATABASE_URL }
      - ref: loan-events
        envBindings: { url: NATS_URL }
      - ref: disb-claims
        envBindings: { url: VALKEY_URL }
    endpoints:
      - component: payment-rail-stub
        name: http                        # the endpoint's key in its Workload
        visibility: project
        envBindings: { address: PAYMENT_RAIL_URL }
```

Dependencies are declared, not configured: `dependencies.resources` binds a
Resource's outputs to env vars by name; `dependencies.endpoints` binds another
component's in-cell address. The full file has all six. Put your registry in and
apply:

```bash
sed -i.bak 's#<REGISTRY>#ghcr.io/your-org#g' manifests/app/05-workloads.yaml && rm manifests/app/05-workloads.yaml.bak
occ apply -f manifests/app/05-workloads.yaml
```

**Three rules worth repeating:**

- `owner` is **immutable**. A typo means deleting the Workload.
- Each `env` entry needs **exactly one** of `value` or `valueFrom`.
- `credit-scoring` must be `visibility: [project]`. Set `external` and the
  platform refuses it — deliberately, in D10.

`API_BASE_URL` for the two frontends is `loan-api-go`'s external URL, which does
not exist until D6/D7. Apply them now with the placeholder (they start, with no
API behind them) and update once you have the URL from D8 — updating a Workload
cuts a new release and redeploys.

---

## D6 — Build `loan-api-go` from source

The one component the platform builds. `public-api` allows `dockerfile-builder`
and nothing else.

```bash
occ component scaffold loan-api-go \
  --componenttype deployment/public-api \
  --clusterworkflow dockerfile-builder \
  -n $NS -p $PROJECT -o /tmp/06-loan-api-go.yaml
```

```yaml
apiVersion: openchoreo.dev/v1alpha1
kind: Component
metadata:
  name: loan-api-go
  namespace: kifaru-bank
spec:
  owner:
    projectName: lending
  componentType:
    kind: ComponentType
    name: deployment/public-api
  autoDeploy: true
  workflow:
    kind: ClusterWorkflow
    name: dockerfile-builder
    parameters:
      repository:
        url: https://github.com/kaviththiranga/2026-NBO-Internal-Developer-Platform-tutorial-1
        appPath: app/loan-api-go            # where the builder looks for workload.yaml
        revision: { branch: main }
      docker:
        context: app/loan-api-go
        filePath: app/loan-api-go/Dockerfile
```

**Do not create a Workload for this component.** The build generates
`loan-api-go-workload` itself from the image plus `app/loan-api-go/workload.yaml`
(read at build time). Without that descriptor the generated Workload has only
`container.image` — no endpoints, no env, so nothing works. The descriptor uses
an **array** of endpoints and `name:` (not `key:`) for env entries — a `key:` is
accepted silently and produces env vars with empty names.

```bash
occ apply -f manifests/app/06-component-loan-api-go.yaml
occ component workflow run  loan-api-go -n $NS -p $PROJECT
occ component workflow logs loan-api-go -n $NS -f                 # follow it (logs takes no -p)
```

Steps: `checkout-source → build-image → publish-image → generate-workload-cr`
(a few minutes). With `autoDeploy: true` it deploys into development as soon as
the Workload is generated.

The platform did not pick the language. It said that a public API — in any
language — is built by an approved pipeline, is exposed on purpose, and runs two
replicas in production. (`app/loan-api` is the same API in Ballerina; it is not
deployed here.)

---

## D7 — Deploy

With `autoDeploy: true` each component deploys to the **first** environment in the
pipeline (development) as soon as its Workload exists. Otherwise, or to redeploy:

```bash
occ component deploy credit-scoring -n $NS -p $PROJECT
occ releasebinding list -n $NS
```

Anything not `Ready` — read the reason, do not guess:

```bash
occ releasebinding get credit-scoring-development -n $NS
occ component logs credit-scoring -n $NS -p $PROJECT
kubectl get pods -n <cell namespace>
```

| Status | Means |
|---|---|
| `RenderingFailed` | a ComponentType or ProjectType validation refused it — the message names the rule |
| `ResourceApplyFailed` | rendered fine, the data plane refused to apply it |
| `ResourceDependenciesPending` | a Resource has no `Ready` binding in this environment (D2 — did you promote?) |
| `ResourcesDegraded` | the pod is not healthy — `ImagePullBackOff` = image/tag/architecture, `CrashLoopBackOff` = read the container log |

**`credit-scoring` and the 100m CPU limit — do this before any demo.** Every type
defaults to `resources.limits.cpu: 100m`. A JVM (Ballerina) service throttled to a
tenth of a core starts in ~20 s but answers the first `/score` calls in 2–5 s;
`loan-api-go` gives it 2 s (`SCORING_TIMEOUT_MS`) and returns **503 scoring service
unavailable** when it is late. The limit is an environment config, so it is the
developer's to set, on the binding:

```bash
occ releasebinding get credit-scoring-development -n $NS | grep releaseName   # -> 07-…yaml
occ apply -f manifests/app/07-releasebinding-credit-scoring-cpu.yaml
```

At 500m the pod rolls and the first submission on a fresh pod comes back 201.
Still warm it with one `POST` before showing it to anyone.

---

## D8 — Get the URLs

**Read them from the binding. Never construct them.**

```bash
occ releasebinding get loan-api-go-development -n $NS               # status.endpoints[].externalURLs
occ releasebinding get loan-application-portal-development -n $NS
occ releasebinding get loan-officer-console-development -n $NS
```

Put `loan-api-go`'s external URL into both frontends' `API_BASE_URL` in
`05-workloads.yaml` and re-apply — the other four Workloads are unchanged, so
they are no-ops. Then smoke it:

```bash
U=<loan-api-go external url>
curl -s $U/healthz
curl -s -X POST $U/applications -H 'Content-Type: application/json' -d @demo/payloads/APP-100244.json | jq .
```

Expect `201` with `"decision":"APPROVED"` and, within a few seconds, a
`disbursement` block with `"status":"SENT"` — that is `loan-api-go` → `credit-scoring`
→ JetStream → `disbursement-worker` → `payment-rail-stub`, all wired by the
dependencies you declared. `APP-100245` is the designed **DECLINED** case.

---

## D9 — Promote

Promotion creates a **second binding pointing at the same release** — no rebuild,
no re-render. The platform's pipeline decides what is legal.

```bash
occ component deploy credit-scoring -n $NS -p $PROJECT --to staging
occ releasebinding list -n $NS
```

Staging needs its own cell (created in D1), its own resource placements (D2
again with `environment: staging` + `occ resource promote … --env staging`), and
its own schema load (D3).

`--to production` from development has nowhere to go — the pipeline has no such
edge.

---

## D10 — Two things to try

**1. The application works.** Portal → submit → console shows it → submit the
same application again → nothing is paid twice.

**2. The platform refuses a public risk engine.** Edit `credit-scoring`'s Workload
to `visibility: [external]` and re-apply:

```bash
occ apply -f manifests/app/05-workloads.yaml                 # accepted
occ component deploy credit-scoring -n $NS -p $PROJECT       # "Successfully deployed"
occ releasebinding list -n $NS                               # STATUS: RenderingFailed
occ releasebinding get credit-scoring-development -n $NS
```

```
Failed to render resources: component type validation failed: rule[0]
"${!workload.endpoints.exists(name, ep, "external" in ep.visibility)}"
evaluated to false: Internal engines must not expose external endpoints.
Risk and scoring services are reachable only from inside the cell.
```

**Both the apply and the deploy report success** — the truth is on the binding.
Flip it back and deploy again.

---

## Troubleshooting

**Everything is `Pending` and nothing says why.** The cell probably does not exist
for that environment. `occ projectreleasebinding list -n $NS -p $PROJECT`.

**`ResourceDependenciesPending`.** A Resource has no `Ready` binding in that
environment. Placed but not promoted is the usual cause. It is per environment.

**`OutputNotResolved`.** An `envBindings` key does not match an output name: it
is `url`, not `URL` or `connectionUrl`.

**The frontends load but every call fails.** `API_BASE_URL` is stale or wrong —
it must be `loan-api-go`'s **external** URL from the binding status.

**`disbursement-worker` is `CrashLoopBackOff` with `could not create the LOANS
stream`.** The nats it was given has no JetStream. The Resource must use the
team's `ResourceType/nats`, not a NATS-Core type.

**The build fails at `checkout-source`.** The platform cannot read the repository:
wrong URL, wrong `appPath`, or a private repo without `secretRef`.

**`occ ... list` returns nothing though objects exist.** Several list commands
default to the project named `default`. Pass `-p $PROJECT`.

---

## Teardown

```bash
occ project delete lending -n $NS      # takes components, workloads, cells, resource placements
```

The platform (namespace, environments, pipeline, types) survives — that is the
boundary between this guide and `PLATFORM-GUIDE.md`.
