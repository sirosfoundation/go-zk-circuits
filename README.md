# go-zk-circuits

<div align="center">

[![CI](https://github.com/sirosfoundation/go-zk-circuits/actions/workflows/ci.yml/badge.svg)](https://github.com/sirosfoundation/go-zk-circuits/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sirosfoundation/go-zk-circuits.svg)](https://pkg.go.dev/github.com/sirosfoundation/go-zk-circuits)
[![Go Report Card](https://goreportcard.com/badge/github.com/sirosfoundation/go-zk-circuits)](https://goreportcard.com/report/github.com/sirosfoundation/go-zk-circuits)
[![Quality Gate](https://sonarcloud.io/api/project_badges/measure?project=sirosfoundation_go-zk-circuits&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=sirosfoundation_go-zk-circuits)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/sirosfoundation/go-zk-circuits/badge)](https://scorecard.dev/viewer/?uri=github.com/sirosfoundation/go-zk-circuits)
![coverage](https://raw.githubusercontent.com/sirosfoundation/go-zk-circuits/badges/.badges/main/coverage.svg)
[![Go Version](https://img.shields.io/github/go-mod/go-version/sirosfoundation/go-zk-circuits)](go.mod)
[![License](https://img.shields.io/badge/License-BSD_2--Clause-orange.svg)](LICENSE)

</div>

A public, read-only catalog and content-addressed file host for zero-knowledge-proof circuit artifacts (Longfellow today; Vega/BBS anticipated). It answers exactly two questions: what circuits exist, and give me the bytes of one, verifiably.

API reference: see `docs/swagger/` (generated from code via `make swagger`) or the live `/swagger/index.html` on a running instance.

## Two hostnames

| | What | Where |
|---|---|---|
| `api.circuits.siros.org` | This service — the actual read API (`/v1/manifest.json`, `/v1/circuits/*`, `/v1/artifacts/*`). | This repo, deployed to Fly.io. |
| `circuits.siros.org` | The human-facing website — live circuit list, provenance, docs. | Separate repo, `sirosfoundation/circuits.siros.org`, GitHub Pages. |

The website is just another consumer of this API's public, CORS-enabled `/v1/*` endpoints — no special access, no coupling beyond that.

## Quick start

```sh
make build          # builds ./zkc (the service) and ./circuitctl (the publish CLI)
./zkc                # serves the embedded catalog on :8080
curl localhost:8080/v1/manifest.json
```

```sh
make test            # unit tests
make test-integration  # spins up a real ./zkc process and exercises it over HTTP
make lint            # golangci-lint + gosec + staticcheck
```

## Publishing a circuit

There is no upload API. Publishing means: fork this repo, add the entry locally with `circuitctl`, and open a pull request — the same path for a first-time contributor as for a maintainer. `catalog/manifest.json` is a **generated build product**, never hand-edited; `circuitctl verify` (enforced in CI, see below) rejects any hand-edit or drift from `catalog/circuits/*.json`.

### 1. Fork and clone

```sh
gh repo fork sirosfoundation/go-zk-circuits --clone
cd go-zk-circuits
```

### 2. Build `circuitctl`

There's no separate distribution for it — it's built from this same repo, alongside the service itself:

```sh
go build -o circuitctl ./cmd/circuitctl
# or: make build   (also builds ./zkc, the service binary — not needed just to publish)
```

### 3. Add your circuit

```sh
./circuitctl add <file> \
  --system longfellow \
  --origin <url> --added-by <you> \
  [--toolchain "<what built these bytes>"] \
  [--license <spdx>] [--open-source] \
  [--unpublished]   # keep it in the repo, hash-verified, but out of the served manifest
```

This writes `catalog/circuits/<id>.json`, copies the artifact into `artifacts/sha256/<hash>`, and regenerates `catalog/manifest.json` for you — commit all three.

`published`, `openSource`, and `toolchain` all default to the fail-closed value (`false`/empty) unless explicitly asserted — see spec §2.4.1/§2.8.1 for why. An entry with `published: false` stays fully in the repo and fully integrity-checked; it is simply never included in the manifest the service actually serves, and its artifact bytes are unreachable at `/v1/artifacts/*` even though they're compiled into the binary.

### 4. Verify locally before pushing

```sh
./circuitctl verify              # the same gate CI runs: hashes, schema, orphans, manifest freshness
./circuitctl ls [--stale]        # human-readable table of the whole catalog
```

### 5. Open a pull request

```sh
git checkout -b add-<your-circuit-id>
git add catalog/ artifacts/
git commit -m "Add <your-circuit-id>"
git push -u origin add-<your-circuit-id>
gh pr create --repo sirosfoundation/go-zk-circuits
```

`main` is protected: your PR must pass CI (build/test, `circuitctl verify`, lint, `govulncheck`, a Docker build) **and** get one approving review from a `CODEOWNERS`-designated reviewer, since any PR touching `artifacts/` or `catalog/` always does (spec §5.4 — the bytes are the trust boundary). Once merged, it ships to the test environment automatically; a maintainer promotes it to production separately.

### Other lifecycle commands

```sh
circuitctl deprecate/revoke <id> --reason <text>
circuitctl publish/unpublish <id> [--reason <text>]
```

Same flow as above: run locally, commit the result, open a PR.

## Layout

```
cmd/zkc/          the service (go:embed's artifacts/ + catalog/ into the binary)
cmd/circuitctl/   the publish CLI
pkg/catalog/      wire schema, validation, deterministic manifest generation
pkg/api/          HTTP handlers (gin)
pkg/publish/      circuitctl's logic, shared with the service
catalog/circuits/ per-entry source-of-truth JSON files
catalog/manifest.json   GENERATED — never hand-edit
artifacts/sha256/ the actual circuit blobs, content-addressed
```

## License

BSD 2-Clause — see [LICENSE](LICENSE).
