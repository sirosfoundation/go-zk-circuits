# go-zk-circuits

A public, read-only catalog and content-addressed file host for zero-knowledge-proof circuit artifacts (Longfellow today; Vega/BBS anticipated). It answers exactly two questions: what circuits exist, and give me the bytes of one, verifiably.

Full design: [`docs/circuit-distribution-service-spec.md`](docs/circuit-distribution-service-spec.md).

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

There is no upload API — publishing goes through `circuitctl` and a normal git PR (see spec §5). `catalog/manifest.json` is a **generated build product**; `circuitctl verify` (run in CI) rejects any hand-edit or drift from `catalog/circuits/*.json`.

```sh
circuitctl add <file> \
  --system longfellow \
  --origin <url> --added-by <you> \
  [--toolchain "<what built these bytes>"] \
  [--license <spdx>] [--open-source] \
  [--unpublished]   # keep it in the repo, hash-verified, but out of the served manifest

circuitctl verify              # CI gate: hashes, schema, orphans, manifest freshness
circuitctl ls [--stale]        # human-readable table
circuitctl deprecate/revoke <id> --reason <text>
circuitctl publish/unpublish <id> [--reason <text>]
```

`published`, `openSource`, and `toolchain` all default to the fail-closed value (`false` / empty) unless explicitly asserted — see spec §2.4.1/§2.8.1 for why. An entry with `published: false` stays fully in the repo and fully integrity-checked; it is simply never included in the manifest the service actually serves, and its artifact bytes are unreachable at `/v1/artifacts/*` even though they're compiled into the binary.

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
