# Building the images

Six of the seven Kifaru components are deployed from images you build and push
yourself ("bring your own image"). The seventh, `loan-api-go`, is built by the
platform from this repository's source — see `DEVELOPER-GUIDE.md` D6.

| Image | Component | Language |
|---|---|---|
| `kifaru-credit-scoring` | `credit-scoring` | Ballerina (JVM) |
| `kifaru-payment-rail-stub` | `payment-rail-stub` | Go |
| `kifaru-disbursement-worker` | `disbursement-worker` | Go |
| `kifaru-arrears-eod` | `arrears-eod` | Go |
| `kifaru-loan-officer-console` | `loan-officer-console` | React + Go server |
| `kifaru-loan-application-portal` | `loan-application-portal` | React + Go server |

One set of images serves every deployment of this application, in any namespace.

## Prerequisites

- Docker with `buildx` (Docker Desktop, Colima, Podman with the Docker CLI — anything
  that can build for a platform other than your own). On an Apple-silicon Mac,
  Colima with `--vz-rosetta` makes amd64 builds fast; plain QEMU emulation works but
  the Ballerina build takes a long time.
- A container registry your cluster's nodes can pull from: GHCR, Docker Hub, ECR,
  GCR, Harbor, or the in-cluster registry of a local install. You must be logged in
  (`docker login …`).

## 1. Build

From `tutorial-1/`:

```bash
make images                       # linux/amd64 by default
make images PLATFORM=linux/arm64  # only if your nodes are arm64 (Graviton, Apple-silicon k3d)
```

This builds the six images with `docker buildx build --platform … --load` and
tags them `kifaru/<component>:local`. It finishes by listing them with their
sizes; `credit-scoring` is ~116 MB and the slowest to build.

**Architecture is the trap.** A laptop builds for its own CPU by default. An
arm64 image on an amd64 node starts and immediately dies with
`exec format error`, and nothing in OpenChoreo's status says why — the binding
just reports `ResourcesDegraded`. Check what your nodes are before building:

```bash
kubectl get nodes -o custom-columns='NAME:.metadata.name,ARCH:.status.nodeInfo.architecture'
```

## 2. Push

```bash
make push REGISTRY=ghcr.io/your-org            # -> ghcr.io/your-org/kifaru-<component>:v1
make push REGISTRY=ghcr.io/your-org TAG=v2     # bump the tag on a rebuild
```

`make push` prints each image's architecture as it goes, so a mismatch is visible
before anything reaches the cluster.

Registry notes:

- **ECR** has no implicit repository creation — a push to a missing repository
  fails with `name unknown`. Create the six `kifaru-<component>` repositories
  first (`aws ecr create-repository --repository-name kifaru-<component>`), and log
  in with `aws ecr get-login-password | docker login --username AWS --password-stdin <registry>`.
- **GHCR** packages are private by default; make them public or give the cluster a
  pull secret.
- A **local k3d** install of OpenChoreo ships an in-cluster registry that is
  addressed by two names: push to `localhost:<port>`, and reference
  `host.k3d.internal:<port>/…` in the Workloads.
- **Node pull permission** is the other silent failure: pods sit in
  `ImagePullBackOff` while the binding reports `ResourcesDegraded`. On EKS the node
  group's instance role needs `AmazonEC2ContainerRegistryReadOnly`.

## 3. Tell the platform where they are

`manifests/app/05-workloads.yaml` references every image as
`<REGISTRY>/kifaru-<component>:v1`. Replace `<REGISTRY>` with the value you
pushed to:

```bash
sed -i.bak 's#<REGISTRY>#ghcr.io/your-org#g' manifests/app/05-workloads.yaml && rm manifests/app/05-workloads.yaml.bak
```

Then continue with `DEVELOPER-GUIDE.md`.

## Why not build everything on the platform?

You could: every type except `public-api` allows `dockerfile-builder`, and each
app directory has a Dockerfile. It would mean seven concurrent builds on the
workflow plane, one of them a JVM compile — fine on a big cluster, slow on a small
one. The two paths side by side are also the point: a component with no
`workflow` is legitimate — its Workload names `container.image` directly — while
the bank's public entry point is built by the approved pipeline from source.
