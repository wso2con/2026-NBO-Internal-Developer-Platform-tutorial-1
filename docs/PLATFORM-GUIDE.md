# Platform engineer guide — Kifaru Bank on OpenChoreo

Follow this top to bottom and you end with a platform a developer can deploy the
Kifaru demo onto: a tenancy, three environments, a promotion path, a cell design,
five bank-specific component types, three resource types, and an authorization
boundary. Deploying the application is the developer's half:
`DEVELOPER-GUIDE.md`.

**Everything here is `occ` + YAML.** `occ` has no `create` verb — `apply -f` is
the single creation path, and `occ <kind> get NAME` prints YAML. `kubectl`
appears once, at the end, to look *inside* a rendered cell at plain Kubernetes
objects OpenChoreo does not model. The manifests are in `manifests/platform/`,
numbered in apply order.

Tested against OpenChoreo 1.2.x (`occ` 1.2.5).

---

## Prerequisites

- An OpenChoreo 1.2.x install you can reach with `occ` (local k3d via the
  quick-start, or a multi-cluster install), logged in with a platform-engineer
  identity: `occ login`, then `occ namespace list` proves the token works.
- The four shipped builders (`occ clusterworkflow list`: `ballerina-buildpack-builder`,
  `dockerfile-builder`, `gcp-buildpacks-builder`, `paketo-buildpacks-builder`) and
  a data plane (`occ clusterdataplane list`).

```bash
export NS=kifaru-bank
```

---

## The shape: one namespace, and the team's own types

```
manifests/platform/
  01-namespace  02-environments  03-pipeline            the tenancy
  04-projecttype-bank-cell                              ProjectType
  05..09-componenttype-*                                ComponentType ×5
  10..12-resourcetype-*                                 ResourceType ×3
  13-authzrole-kifaru-developer                         ClusterAuthzRole — shipped developer + project create/update/delete
  14-authzrolebinding-kifaru-developers                 ClusterAuthzRoleBinding
```

OpenChoreo has two scopes for every type. `ClusterComponentType` is a
cluster-wide standard — the four shipped ones are `service`, `web-application`,
`worker`, `scheduled-task`. `ComponentType` (and `ProjectType`, `ResourceType`)
is a team's own, visible only inside its namespace: same spec, same CEL, same
rendering. The bank's types are namespace-scoped because **the namespace is the
boundary** — another tenant on the same cluster cannot see, use, edit or delete
them — and because names then stay plain: `internal-engine`, referenced by
developers as `deployment/internal-engine`.

One consequence: `ComponentType.allowedWorkflows[].kind` defaults to `Workflow`
(namespaced). The files say `kind: ClusterWorkflow` explicitly, because the
builders are the shipped cluster-scoped ones.

### Two installs, two things to check first

**The data plane name.** `02-environments.yaml` references `ClusterDataPlane/default`.
`dataPlaneRef` is **immutable** — getting it wrong means deleting and recreating
every Environment.

```bash
occ clusterdataplane list
```

**The gateway namespace.** `04-projecttype-bank-cell.yaml` opens the cell's
default-deny policy to the namespace the gateway runs in (`openchoreo-data-plane`
by default). If your install puts it elsewhere, every externally published
component times out while reporting `Ready`.

```bash
occ clusterdataplane get default       # spec.gateway.ingress.external.namespace
```

---

## P0 — Everything, in one command

If you just want the platform up:

```bash
occ apply -f manifests/platform/          # all 14 files, in order

occ projecttype list -n $NS               # bank-cell
occ componenttype list -n $NS             # internal-engine event-consumer regulated-batch web-frontend public-api
occ resourcetype list -n $NS              # postgres nats valkey
```

If you want to understand each piece, skip this and apply them one at a time in
P1–P9 below. Apply is idempotent, so doing both is harmless.

> Do not pipe `occ apply` into `head`. It applies as it walks the files, so a
> closed pipe stops it partway and you get a subset with no error to tell you.

---

## P1 — The namespace: the tenancy

```bash
occ apply -f manifests/platform/01-namespace.yaml
occ namespace list
```

`apiVersion` is **`openchoreo.dev/v1alpha1`**, not the core `v1` you would write
for kubectl — `occ` rejects the core group.

**No pod ever runs here.** This namespace holds *records*: Projects, Components,
Environments, the pipeline, and the team's types. Workloads run in cells (P4).

---

## P2 — Three environments

```bash
occ apply -f manifests/platform/02-environments.yaml
occ environment list -n $NS
```

```
NAME          DATA PLANE                 PRODUCTION   AGE
development   ClusterDataPlane/default   false        0s
production    ClusterDataPlane/default   true         0s
staging       ClusterDataPlane/default   false        0s
```

**Check the PRODUCTION column.** `isProduction: true` is easy to omit and is not
cosmetic. Unlike `dataPlaneRef` it can be fixed by a later apply.

---

## P3 — The promotion path

```bash
occ apply -f manifests/platform/03-pipeline.yaml
occ deploymentpipeline get default -n $NS
```

development → staging → production, and no edge from development to production.
A developer cannot promote straight to production — not because of a rule in a
wiki: the path does not exist.

---

## P4 — The ProjectType: what a "cell" is at this bank

A Project has no infrastructure of its own. It references a **ProjectType**, and
that is what gets materialised per environment. Read the shipped one first — its
own description makes the argument:

```bash
occ clusterprojecttype get default
```

> "Minimal project type that provisions only the cell namespace per environment.
> **Use as the starting template when no additional shared infrastructure
> (NetworkPolicies, ResourceQuotas, baseline RBAC) is needed.**"

Then apply the bank's:

```bash
occ apply -f manifests/platform/04-projecttype-bank-cell.yaml
occ projecttype list -n $NS
```

```
NAME        RESOURCES   AGE
bank-cell   3           4m
```

**`RESOURCES 3` against the shipped `default`'s `1` is the whole point**: the
shipped type renders a bare namespace; yours renders a namespace that is closed
by default.

| Resource | Why |
|---|---|
| `cell-namespace` | **mandatory** — every ProjectType must declare a `v1/Namespace` named literally `${metadata.namespace}`, never guarded by `includeWhen`; omit it and the binding reports `NamespaceMissing` |
| `cell-default-deny` | nothing is reachable by default |
| `cell-allow-intra` | …except sibling pods in the cell, **and the gateway** |

plus two **required** parameters — `costCentre` matching `^CC-[0-9]{4}$`, and
`dataClassification` — and a validation refusing `public`.

### The gateway exception is not optional

`loan-api-go` and both web frontends are published through the gateway, and **the
gateway is not in the cell**. A plain default-deny blocks it like any other
stranger, and the failure is nasty because nothing looks broken: the HTTPRoute
exists, `occ` reports the component `Ready`, the pod is `1/1 Running`, and every
external request just times out. That is why `cell-allow-intra` has two `from`
entries:

```yaml
ingress:
  - from:
      - podSelector: {}                      # siblings inside the cell
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: openchoreo-data-plane   # the gateway
```

**This does not re-expose `credit-scoring`.** `internal-engine` has no
`httproute-external` template at all, so the gateway has no route to address it.
The ComponentType closes that door; the policy closes the rest of the room.

### Whether the policies are enforced depends on the cluster

k3s (k3d) runs an embedded policy controller — tested: cross-namespace refused,
same-namespace allowed. EKS with the default VPC CNI enforces NetworkPolicy only
when the addon is configured with `enableNetworkPolicy: true`; otherwise the
policies render and do nothing. Check before you claim isolation:

```bash
kubectl -n kube-system get ds aws-node -o yaml | grep enable-network-policy   # EKS
```

Where it is enforced, there is a timing trap: the controller syncs source pod IPs
a beat after a pod starts, and a pod that sends traffic within a second or two can
be refused until that sync lands. Services must retry their first calls.

### Why there is no ResourceQuota

The obvious fourth resource is a quota, and it was tried and removed: the
`postgres` / `nats` / `valkey` ResourceTypes set only `limits.memory`, no CPU
request, and a quota with `requests.cpu` makes CPU requests mandatory for *every*
pod — the database, broker and cache are rejected outright. The data-plane
agent's ClusterRole also cannot create ResourceQuotas without an additive grant.
If you want a cap, use a pod **count** (`pods: "30"`, no `requests.*` keys) plus
that grant.

### Two namespaces, and they are not the same one

| | `kifaru-bank` | the cell |
|---|---|---|
| name | you chose it | `dp-kifaru-bank-lending-development-<hash>`, generated |
| apiVersion | `openchoreo.dev/v1alpha1` | `v1`, plain Kubernetes |
| holds | Projects, Components, Environments, types | pods, services, the policies |
| how many | one, for the bank | **one per project per environment** |

Inside a ProjectType, `${metadata.namespace}` is always **the cell being
rendered**. If you need `kifaru-bank` inside a template, that is
`${metadata.projectNamespace}`.

---

## P5 — Five ComponentTypes: the bank's workload contracts

**Derive from the shipped types; do not hand-write them.** `service` alone has
eight `resources[]` entries of substantial CEL. The files were generated this way:

```bash
occ clustercomponenttype get service > service.yaml     # then edit: kind, namespace, name, rules
```

```bash
D=manifests/platform
occ apply -f $D/05-componenttype-internal-engine.yaml
occ apply -f $D/06-componenttype-event-consumer.yaml
occ apply -f $D/07-componenttype-regulated-batch.yaml
occ apply -f $D/08-componenttype-web-frontend.yaml
occ apply -f $D/09-componenttype-public-api.yaml

occ componenttype list -n $NS
occ componenttype get internal-engine -n $NS
```

**Five types cover all seven components.** Check that before anyone creates a
component: `Component.spec.componentType` cannot be changed after creation.

| Type | Derived from | For | The change that matters |
|---|---|---|---|
| `internal-engine` | `service` | `credit-scoring`, `payment-rail-stub` | **`httproute-external` deleted** — 7 resources, not 8 |
| `event-consumer` | `worker` | `disbursement-worker` | one builder; the no-endpoints rule is the shipped one, reworded |
| `regulated-batch` | `scheduled-task` | `arrears-eod` | `auditRetentionDays` with a floor of 2555 (seven years) |
| `web-frontend` | `web-application` | `loan-application-portal`, `loan-officer-console` | external route allowed |
| `public-api` | `service` | `loan-api-go` | **requires** an external endpoint; built from git by `dockerfile-builder` only |

Developers reference these as `deployment/internal-engine`,
`deployment/event-consumer`, `cronjob/regulated-batch`, `deployment/web-frontend`,
`deployment/public-api` — `workloadType/name` — with `kind: ComponentType`.

### Two types, two opposite rules

`internal-engine` cannot express a public route — the template is gone — and a
validation says so in words:

```yaml
- rule: ${!workload.endpoints.exists(name, ep, "external" in ep.visibility)}
  message: >-
    Internal engines must not expose external endpoints. Risk and scoring
    services are reachable only from inside the cell.
```

`public-api` carries the inverse:

```yaml
- rule: ${workload.endpoints.exists(name, ep, "external" in ep.visibility)}
  message: >-
    A public API must declare at least one external endpoint. Use
    'deployment/internal-engine' for services that stay inside the cell.
```

Choosing `public-api` is an explicit statement that this component faces the
world — not something that happens by leaving a default alone.

**Both fire at render time, not admission time.** `occ apply` accepts the
workload and `occ component deploy` prints "Successfully deployed" — both
misleading. The truth is on the binding: `occ releasebinding get <name> -n $NS`.

### Things worth knowing about what is in these files

- **`schedule` is an `environmentConfigs` field, not a parameter** — the cron
  expression is set per environment by the binding.
- **The production replica floor uses `metadata.environmentName`**, not
  `environment.isProduction` — `environment.*` is not available in ComponentType
  CEL (it exists only for ResourceTypes):
  `${metadata.environmentName != "production" || environmentConfigs.replicas >= 2}`.
- **Every type defaults to a 100m CPU limit.** Fine for Go; the JVM
  `credit-scoring` needs more, and the developer raises it per environment on the
  binding — an environmentConfig, not a type change (`DEVELOPER-GUIDE.md` D7).

---

## P6 — Traits: look, don't author

```bash
occ clustertrait list
```

Gating already happens: each ComponentType's `allowedTraits` decides what a
developer may attach. The shipped alert trait needs a notification channel the
quick-start install does not have, so it is listed and not used here.

---

## P7 — Workflow gating: who may build what

Not a separate object — it is `allowedWorkflows` inside the types you applied.

| ComponentType | allowedWorkflows | Why |
|---|---|---|
| `public-api` | `dockerfile-builder` **only** | the public entry point comes from the approved pipeline, from source in git — never from a pushed image |
| `internal-engine` | `ballerina-buildpack-builder`, `dockerfile-builder` | `credit-scoring` is Ballerina, `payment-rail-stub` is Go; both stay inside the cell |
| `event-consumer` | `dockerfile-builder` | Go |
| `regulated-batch` | `dockerfile-builder` | Go |
| `web-frontend` | `dockerfile-builder` | Go server + static bundle |

No type anywhere allows `paketo-buildpacks-builder` or `gcp-buildpacks-builder` —
two of the four shipped builders are off the menu entirely. Nobody can slip an
arbitrary builder into a bank pipeline: the allow-list lives with the type, not in
CI config a developer edits.

---

## P8 — Authorization

```bash
occ clusterauthzrole list
occ clusterauthzrolebinding get developer-binding
```

Bindings map a **JWT claim** to a role — they are not per-user. The shipped
`developer-binding` maps `groups: developers` with `scope: {}` (cluster-wide).
The bank's is narrower:

```bash
occ apply -f manifests/platform/13-authzrole-kifaru-developer.yaml
occ apply -f manifests/platform/14-authzrolebinding-kifaru-developers.yaml
occ clusterauthzrolebinding get kifaru-bank-developers-binding
```

`groups: kifaru-bank-developers` → the **`kifaru-developer`** role, **scoped to
`kifaru-bank` only**. (`ClusterAuthzRoleBinding` is cluster-scoped by nature, which
is why its name carries the namespace.) Onboarding is entirely IdP-side: put a
person in the group and the platform grants the role with no change here.

### Why the bank has its own developer role

The shipped `developer` role has `project:view` and **no `project:create`** — in
OpenChoreo's default model a Project, and the cell it gets, is a platform-engineer
object; only `platform-engineer` carries the project verbs:

```bash
occ clusterauthzrole get developer
occ clusterauthzrole get platform-engineer
```

This bank wants developers to create their own projects inside their namespace
(`DEVELOPER-GUIDE.md` D1), so `13-authzrole-kifaru-developer.yaml` is the shipped
list plus `project:create`, `project:update`, `project:delete` — nothing else. A
role is a list of verbs; the bank wrote down the one it needed rather than handing
developers the platform-engineer role.

### The developer account (IdP side)

To see the refusal — a developer denied a platform-engineer action — create a user
in your IdP who is in `kifaru-bank-developers` and **not** in `platform-engineers`
or the shipped `developers` group (both bindings are cluster-wide).

On an install that uses the bundled Thunder IdP, that is the Thunder Console at
`<thunder url>/console/`, signed in as the Thunder admin (`admin`; the quick-start
prints or stores its password — on the Helm install it is the `admin-password` key of
the `thunder-admin-credentials` secret). Any other account can sign in and then gets
`403` on every admin page. In the console, organization unit **Default**:

1. **Users → New**, type `openchoreo-user` — `username` (e.g. `dev@kifaru.bank`),
   `email`, `given_name`, `family_name`, `password`.
2. **Groups → New** `kifaru-bank-developers`, member: that user. The name must match
   the binding's `entitlement.value` exactly.

Verify from a second session:

```bash
occ login                                                        # as the developer
occ component list -n $NS -p lending                             # allowed
occ apply -f manifests/platform/04-projecttype-bank-cell.yaml    # refused
```

---

## P9 — ResourceTypes: reuse where it fits, derive where it doesn't

```bash
occ clusterresourcetype list        # what your cluster ships
```

"The platform already offers a managed Postgres, so I am not going to invent one"
is a good platform-engineering line — when it is true. The OpenChoreo samples
ship `postgres`, `nats` and `valkey` types (`samples/getting-started/cluster-resource-types/`),
and two things can go wrong with reusing them as-is: a cluster may not have them
installed (they are samples, not part of the Helm install), and the sample `nats`
is NATS Core with **no JetStream** — `disbursement-worker` opens a durable
JetStream consumer at startup and crash-loops without it.

So the team derives its own, namespace-scoped, from those samples:

```bash
occ apply -f manifests/platform/10-resourcetype-postgres.yaml     # sample, unchanged apart from kind + namespace
occ apply -f manifests/platform/11-resourcetype-nats.yaml         # + --jetstream --store_dir /data on an emptyDir, 128Mi
occ apply -f manifests/platform/12-resourcetype-valkey.yaml       # sample, unchanged
occ resourcetype list -n $NS
```

| Resource type | output | becomes |
|---|---|---|
| postgres | `url` | `DATABASE_URL` |
| nats | `url` | `NATS_URL` |
| valkey | `url` | `VALKEY_URL` |

The `LOANS` stream itself is created by `disbursement-worker` on first start
(idempotent `CreateOrUpdateStream`) — the same job `nats-init` does in the local
compose stack. The types need the External Secrets Operator on the data plane
(the quick-start installs it): credentials are generated there and never reach
the control plane.

---

## P10 — Hand over: the menu

```bash
occ projecttype list -n $NS
occ componenttype list -n $NS
occ resourcetype list -n $NS
occ environment list -n $NS
occ deploymentpipeline get default -n $NS
```

```
NAME              WORKLOAD TYPE
event-consumer    deployment
internal-engine   deployment
public-api        deployment
regulated-batch   cronjob
web-frontend      deployment
```

That is the entire menu — the set of things that can be built here. Tell the
developers two more things: where images go (`BUILD-IMAGES.md`), and that
`loan-api-go` is the one component the platform builds from git.

---

## Render check — prove the platform works before a developer touches it

Creating a Project is the developer's step, but an unrendered ProjectType is an
untested one, and `spec.type` is immutable. `occ` scaffolds from your schema:

```bash
occ project scaffold lending --projecttype bank-cell --namespace $NS -o /tmp/lending.yaml
```

The generated file carries your parameter descriptions, a placeholder for
`costCentre`, and one ProjectReleaseBinding per environment.

**First, watch it refuse.** Set `dataClassification: public` and apply:

```bash
occ apply -f /tmp/lending.yaml
occ projectreleasebinding list -n $NS -p lending      # RenderingFailed ×3
occ projectreleasebinding get lending-development -n $NS
```

```
Failed to render manifests: project type validation failed: rule[0]
"${parameters.dataClassification != \"public\"}" evaluated to false: No project
at this bank is classified public. Customer data is confidential by default;
use "internal" only with a DPO exemption.
```

No component, no workload, no image — the platform refuses the *project*.

**Then watch it work.** Set `confidential`, re-apply, wait ~10 s:

```bash
occ projectreleasebinding get lending-development -n $NS
```

```
Synced          ReleaseSynced    RenderedRelease "p-lending-development-…" is up to date
NamespaceReady  NamespaceReady   Namespace "dp-kifaru-bank-lending-development-…" is ready
ResourcesReady  ResourcesReady   All 2 resource(s) ready
Ready           Ready            ProjectReleaseBinding is ready
```

**`All 2 resource(s)`** — the two policies; the mandated namespace is not counted,
so the shipped `default` type reports `All 0 resource(s)`. The one `kubectl` in
this guide looks inside the cell (on a multi-cluster install, use the data-plane
context):

```bash
CELL=$(occ projectreleasebinding get lending-development -n $NS | awk '/^  namespace:/{v=$2} END{print v}')
kubectl get networkpolicy -n $CELL
kubectl get ns $CELL -o jsonpath='{.metadata.annotations}'
```

```
NAME                          POD-SELECTOR   AGE
allow-same-cell-and-gateway   <none>         30s
default-deny-ingress          <none>         30s

{"bank.kifaru/cost-centre":"CC-4417","bank.kifaru/data-classification":"confidential"}
```

The developer's `costCentre` reached the namespace annotation.

**Changing a ProjectType does not change existing cells.** Edit and re-apply
`bank-cell` and the controller cuts a new ProjectRelease — but the bindings stay
pinned to the old one until you name the new release on the binding. A cell's
shape does not change under a running application because a PE edited a
template. Same promotion model as components and resources.

Clean up the check:

```bash
occ project delete lending -n $NS
```

---

## Teardown

```bash
occ namespace delete $NS                    # takes projects, environments, pipeline, cells, and the namespaced types
occ clusterauthzrolebinding delete kifaru-bank-developers-binding
occ clusterauthzrole delete kifaru-developer               # only once no binding uses it
```

Nothing cluster-scoped is left behind except the authz role and binding — which
is the point of keeping the types in the namespace.
