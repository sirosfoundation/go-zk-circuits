# ZK Circuit Distribution & Metadata Service — Technical Specification

**Status:** Draft spec, ready for implementation. Nothing below is built yet.
**Date:** 2026-08-13
**Working name:** `zk-circuits` (service), `go-zk-circuits` (repo), `circuitctl` (publish CLI) — all naming is provisional, see §9.
**Parent plan:** `/home/leifj/.claude/plans/silver-drifting-heron.md` ("Longfellow ZKP Integration — SDK + Sample App Plan"). This document expands that plan's §5 (and Open Question #6, which resolved to "build this service") into a full spec. It is standalone: you should not need to read the parent plan to implement it, though §2 of the parent has the research backing most of the constraints cited here.
**Position in the parent plan's phasing:** this service is the parent plan's **Phase 1**, deliberately sequenced before the Rust/UniFFI work (Phase 2) and the SDK client work (Phase 3), because Phase 3's fetch-and-cache client needs something real to fetch from.

### Conventions used in this document

- **MUST / SHOULD / MAY** are used in the RFC 2119 sense, and are load-bearing: they mark the difference between "the client is broken if it does this" and "judgment call at implementation time".
- **[ASSUMPTION]** marks a statement I could not verify from source in this pass and which the implementer should confirm cheaply before depending on it. These are mostly about the TS11 registry / `Ts11RegistryClient` precedent, which I described from the parent plan's characterisation rather than by reading the code.
- **[DECISION]** marks a call I made deliberately, with the rationale attached, so it can be revisited on purpose rather than by accident.
- `${CIRCUITS_BASE_URL}` stands in for the API service's base URL: `https://api.circuits.siros.org` — **[DECISION, 2026-08-13] resolved**, see §9.1. A separate human-facing site lives at `https://circuits.siros.org` (§1.6) and is not the API; do not conflate the two when reading URLs in this document.

---

## 1. Purpose & scope

### 1.1 What this service is

A **public, read-only catalog and content-addressed file host for zero-knowledge-proof circuit artifacts and their metadata**, operated by siros.org.

It answers exactly two questions for a client:

1. *"What ZK circuit artifacts exist, and what are their properties?"* — the **manifest**.
2. *"Give me the bytes of artifact X, and let me prove they're the bytes you advertised."* — **content-addressed download**.

That is the whole service. It is a distribution channel with a typed index in front of it.

### 1.2 Why it exists

Longfellow ZKP presentation requires the prover (wallet) and verifier to load the *same* circuit artifact. Circuit files are ~280–340 KB zstd-compressed binaries, keyed by a `(version, numAttributes, blockEncHash, blockEncSig, circuitHash)` tuple, and there is **no forward or backward compatibility between circuit revisions** — a prover built for `v8 / 2 attributes` cannot use a `v7` or a `v8 / 4 attributes` circuit at all.

Today, every consumer in the ecosystem vendors these files by hand:

- the org's `wallet-frontend` ZK branch embeds circuit bytes in its build,
- `openwallet-foundation/multipaz` bundles v6 and v7 circuits as source-tree resources (`multipaz-longfellow/src/commonMain/circuits/`),
- `abetterinternet/zk-cred-longfellow` carries them as test fixtures.

There is **no public distribution mechanism for Longfellow circuits anywhere** — not crates.io, not GitHub release artifacts, nothing. This service fills that gap for our own SDKs first, and is deliberately shaped so it could serve the wider interop community later without redesign.

The alternative — bundling circuits into each SDK release — was considered and rejected in the parent plan (Open Question #6): it ties every circuit change (a new attribute count, a regenerated artifact, eventually a second proof system's artifacts) to a coordinated release of two native SDKs, and adds ~300 KB–1 MB+ per circuit family to every app binary that links the SDK. Fetch-and-cache decouples those. See §7.5 for the honest limits of that decoupling — it is smaller than it first appears, and knowing why is important.

### 1.3 In scope

- Hosting immutable circuit artifact blobs.
- A typed, versioned, system-agnostic metadata manifest describing those blobs.
- Content-addressed, integrity-verifiable retrieval.
- A publish workflow for getting new artifacts into the catalog, with provenance and review.
- A client library design (`ZkCircuitClient`) for both native SDKs: resolve → fetch → verify → cache → serve.
- Multi-proof-system support from day one (Longfellow now; Vega/BBS later).

### 1.4 Explicitly out of scope

Stating these plainly because each one is a plausible-sounding feature that would wreck the service's simplicity:

| Out of scope | Why / where it lives instead |
|---|---|
| **Verifying proofs** | The service never sees a proof. Verification happens in the verifier's own code (multipaz-verifier-server, our own verifier CLI) against a circuit it loaded itself. |
| **Generating or compiling circuits** | Circuits are produced upstream by `libzk`/`zk-cred-longfellow` tooling. This service distributes artifacts someone else produced; it is not a build service. Provenance is recorded (§2.4), not reproduced. |
| **Proving, key management, credential storage** | Entirely SDK/wallet side. The service handles no secrets and no user data, ever (§8.2). |
| **Trust management for issuers/verifiers** | That is `go-trust`'s job. This service makes no trust decisions about anything. |
| **Per-user / per-wallet state, quotas, accounting** | The service is stateless and anonymous. No accounts, no API keys (§8.5). |
| **Serving the ZK *system implementation*** (Rust crate, UniFFI bindings, AARs/XCFrameworks) | Those go through the org's normal package channels, exactly as `siros-wscd-manager` does today. This service ships *data*, not *code*. |
| **Deciding whether a given wallet build may use a given circuit** | That is a compile-time property of the SDK build (§7). The manifest describes what exists; it does not authorize use. |
| **Dynamic circuit selection policy** (which circuit to prefer when several match) | SDK-side, in `ZkProofSystemRegistry` (parent plan §3.2). The service returns facts, not preferences. |
| **Mirroring/proxying other people's circuit hosts** | There are none to mirror. Revisit only if upstream ever ships one. |

### 1.5 Non-functional targets

Deliberately modest — this is an internal/interop-testing-grade service to start with, and over-engineering the SLO is the main risk to shipping it.

- **Availability target:** best-effort, no formal SLO for v1. A client that has already cached its circuit is fully functional with the service down (§6.6); a client that has not cannot do ZK presentation. This asymmetry is intentional and is why prefetch-at-bootstrap is a SHOULD for clients (§6.5).
- **Catalog size:** order 10 entries at launch, tens for years. Anything in this spec that scales past ~500 entries is speculative; §3.7 says what to do if that assumption breaks.
- **Artifact size:** 280–340 KB compressed per Longfellow circuit today. Design headroom to 5 MB per artifact (Vega's reported ~464 KB proving keys are comfortably inside this; other systems may be larger).
- **Total corpus:** design headroom to ~100 MB. §4.5 gives the concrete trigger for changing storage strategy past that.
- **Traffic:** low. One manifest fetch per client per day at most (§6.4), one artifact download per client per circuit *ever* (immutable + content-addressed = permanently cacheable).

### 1.6 Public website vs. API service — two hostnames, two repos

**[DECISION, 2026-08-13]** There are **two separate public surfaces**, deliberately kept apart:

| | `circuits.siros.org` | `api.circuits.siros.org` |
|---|---|---|
| **What it is** | Human-facing website: what this catalog is, live circuit list, links to docs/GitHub. | The actual read API from §3 — `/v1/manifest.json`, `/v1/circuits/*`, `/v1/artifacts/*`. |
| **Hosting** | GitHub Pages, static HTML/CSS/JS, no build backend. | The Go service (`go-zk-circuits`, §4) on Fly.io. |
| **Repo** | A separate new repo, `circuits.siros.org`, following the same repo-per-subdomain convention as `registry.siros.org` and `apt.siros.org` (siblings already on GitHub Pages) — visually and structurally aligned with those (shared header/footer/brand assets/design tokens), not a one-off. | `go-zk-circuits` (unchanged). |
| **Data flow** | Fetches `https://api.circuits.siros.org/v1/manifest.json` client-side via JS to render a live circuit list — no server-side rendering, no build-time data fetch, so the page never goes stale between deploys. | Serves that same manifest to every consumer: this website, the SDK clients (§6), `circuitctl`-adjacent tooling, anyone with `curl`. |

**Why split at all, rather than one site:** the API's design constraint (§3.1 — every endpoint implementable by a dumb static file server) is specifically about *machine* consumption and reversible hosting; a human-facing marketing/docs page has entirely different concerns (design, copy, SEO, brand consistency with sibling SIROS sites) and putting it on the same Fly-hosted Go service would compromise both the API's minimalism and the site's ability to just be a GitHub Pages repo like its siblings. Splitting costs nothing — the site is a *client* of the API like any other consumer, using the CORS-enabled public endpoints (§8.4 notes the transport is already public/unauthenticated; the API gains a permissive `Access-Control-Allow-Origin` for GET/HEAD specifically so this works).

**Consequence for §8.6 (privacy of the fetch):** the website's client-side fetch of the manifest is itself just another manifest read, same privacy profile as any SDK client's (§6.5, §8.6) — it reads the catalog, never anything artifact-specific tied to a presentation.

---

## 2. Data model

### 2.1 The central design choice: generic params bag

**[DECISION] The manifest uses a fixed envelope of universal fields plus an open, per-system `params` key/value bag. It does not use a discriminated union with a closed set of per-system schemas.**

Rationale, in order of weight:

1. **It matches an already-shipped, validated schema.** `org.multipaz.mdoc.zkp.ZkSystemSpec` — the design reference the parent plan adopts for the SDK-level API (§3.1) and for the wire format (§6) — *is* exactly this shape: `id`, `system`, and a params bag carrying `version`, `circuit_hash`, `num_attributes`, `block_enc_hash`, `block_enc_sig`. Since our clients must construct a `ZkSystemSpec`-shaped object anyway to talk to a multipaz-derived verifier, any other manifest shape means a lossy transform in the middle. Same shape end-to-end, no transform.
2. **We do not know Vega's artifact shape yet, and that is the point.** Vega proves over generic signed data rather than mdoc-specific circuits; the parent plan (§2.4) explicitly records that whether it needs a circuit-file equivalent at all is *unconfirmed*. A closed union would need a schema change — i.e. a `/v2` and a client update — the moment we learn the answer. An open bag absorbs it.
3. **The parent plan already committed to this at the SDK layer for the same reason** (§3.3: `ZkSystemSpec` is a generic bag "precisely so each system defines its own spec/matching semantics"). Having the transport schema disagree with the SDK schema would be a gratuitous inconsistency.
4. **The service does not interpret params.** It is a file host with an index. Only the per-system SDK plugin understands what `block_enc_sig` means. A discriminated union would put knowledge in the service that only the client can act on.

**Cost of this choice, accepted:** the server cannot validate system-specific metadata, so a typo in `num_attributes` is not caught at request time. **Mitigation:** validation moves to *publish* time, where it belongs — `circuitctl` (§5.3) knows the Longfellow filename convention and lints known systems' params, and CI rejects a manifest the tool would not have generated. Unknown systems pass through unvalidated by design.

### 2.2 Params typing rules

`params` is a JSON object with **scalar values only** — `string`, `number`, or `boolean`. No nested objects, no arrays.

Reason: it must map losslessly onto both `Map<String, Any>` in Kotlin and `[String: ZkParamValue]` in Swift, and onto multipaz's `ZkSystemSpec` params, without either side needing a JSON tree walker. Nesting is where cross-platform param bags go wrong.

Client rules:

- Clients **MUST** tolerate a numeric-valued param encoded as a JSON string (`"num_attributes": "2"` as well as `2`). Publishers **MUST** emit the canonical form (JSON number for numbers), but a client that hard-fails on the string form will break the first time a hand-edited manifest slips through, and the defensive coercion costs one line.
- Clients **MUST** ignore params they do not recognise. Adding a param is a non-breaking change.
- Clients **MUST NOT** assume params are ordered.

### 2.3 Identifiers

**`id`** — the primary key of a catalog entry. Stable forever, never reused, never mutated.

- Charset: `[A-Za-z0-9._-]`, 1–128 chars. **Constrained deliberately** so `id` is safe as a path segment and a filename on every platform, which is what makes the per-entry metadata endpoint (§3.4) and the static-host option (§4.2) work without escaping rules.
- **[DECISION] `id` SHOULD be chosen to equal the spec id the interop verifier advertises on the wire**, where such a value is known. This is the field that has to line up with `zkSystemId` in the `zkDocuments[]` envelope (parent plan §6); inventing our own scheme and mapping it would create an unnecessary translation layer at exactly the point where a black-box interop target gives us no way to debug a mismatch.
- The parent plan's illustrative example is `longfellow-libzk-v1_8_2_...`; multipaz names its system `LongfellowZkSystem`. **The exact convention Balfanz's v8/PPID verifier uses is not known** — this is an open question (§9.3), and the reason for `aliases`.

**`aliases`** — an optional array of additional ids that resolve to this same entry.

This exists specifically because our primary interop target is a **black-box** verifier (a private multipaz fork, source inaccessible per the parent plan's Open Question #3). When we discover it advertises a spec id we did not predict, `aliases` lets us serve it without duplicating a 300 KB blob or minting a second entry that would then need its own compatibility pin. Alias ids share `id`'s charset rules and MUST be globally unique across the catalog (an alias MUST NOT collide with another entry's `id` or alias).

### 2.4 Entry schema

One catalog entry (a `CircuitDescriptor`):

| Field | Type | Req | Notes |
|---|---|---|---|
| `id` | string | ✅ | Primary key. §2.3. |
| `aliases` | string[] | | Additional resolvable ids. §2.3. Default `[]`. |
| `system` | string | ✅ | Proof-system family. Registry of known values in §2.7. `longfellow` today. |
| `systemVersion` | string | ✅ | The system's own artifact-generation version, as a string (`"8"` for Longfellow v8). String, not int, because we cannot promise every future system versions numerically. Also mirrored into `params.version` for `ZkSystemSpec` fidelity. |
| `docTypes` | string[] | | Document types this artifact can be used with, e.g. `["org.iso.18013.5.1.mDL"]`. Empty/absent = "not constrained by doc type" (the expected Vega case). Feeds `ZkProofSystem.supportedDocTypes` matching. |
| `published` | boolean | ✅ | **[DECISION, 2026-08-13]** Gates whether this entry (and its artifact bytes) are servable at all — see below. Not a lifecycle state like `status`; a separate, more fundamental gate. |
| `status` | enum | ✅ | `active` \| `deprecated` \| `revoked`. Semantics in §7.4. **`revoked` is the only manifest field that can negatively gate a cached artifact.** |
| `params` | object | ✅ | The system-specific bag. §2.1, §2.2. Longfellow keys in §2.5. |
| `artifact` | object | ✅ | The bytes. §2.6. |
| `source` | object | | Provenance. §2.8. Strongly recommended for every entry; not `required` only because early hand-published entries may genuinely not know. |
| `publishedAt` | string | ✅ | RFC 3339 UTC. When this entry entered the catalog. |
| `deprecatedAt` | string | | RFC 3339. Present iff `status != active`. |
| `notes` | string | | Free text for humans. Clients MUST NOT parse it. |

### 2.4.1 `published` — build-time visibility gate

**[DECISION, 2026-08-13]** An entry can exist in the source-of-truth repo (`catalog/circuits/<id>.json` + its artifact bytes under `artifacts/sha256/`) — reviewable, hash-verified by `circuitctl verify`, ready for when its provenance/licensing/whatever-else clears — **without ever being visible to a client**, by setting `published: false`.

**Why this exists:** the alternative to a per-entry gate is holding up the *entire* catalog's deploy pipeline until every single entry's provenance is settled — which is backwards for a repo meant to hold several circuits from different sources on different timelines. The concrete trigger: the first real v8 circuit was sourced from a local copy of a private, black-box fork with unconfirmed redistribution rights (§9.6 vs. Appendix C's provenance note) — that entry needed to keep existing in the repo (so tooling, tests, and future automation have something real to work with) while being fully absent from what the deployed service actually serves, so that adding the *next* circuit — with clean provenance — doesn't have to wait on resolving the first one's.

**Mechanics — deliberately a build-time filter, not a wire-format flag clients ever see conditionally:**

- `circuitctl verify` operates on **every** entry regardless of `published` — hash integrity, schema validity, orphan detection all still apply. An unpublished entry's bytes are exactly as protected against corruption/drift as a published one; "unpublished" means "not yet servable," not "not yet real."
- `circuitctl` regenerating `catalog/manifest.json` **filters to `published: true` entries only** before building the manifest. An unpublished entry's JSON file and artifact bytes stay in the repo, but never appear in the generated manifest.
- The service (`cmd/zkc`) only ever loads `catalog/manifest.json` — it has no separate path to an unpublished entry's metadata. So an unpublished entry is invisible at `/v1/manifest.json`, and `/v1/circuits/{id}.json` 404s for it exactly as if it didn't exist.
- **The artifact endpoint (§3.6) closes the one loophole a naive implementation would leave open**: the bytes are still `go:embed`'d into the binary (they have to be, so they're ready the moment the entry is published — re-fetching upstream bytes at publish time would reintroduce exactly the kind of two-step inconsistency §4.3 designed `go:embed` to avoid). Without an explicit check, a client who already knew or guessed the sha256 hash could still `GET /v1/artifacts/sha256/<hex>` and receive the bytes, even though the entry referencing them is absent from the manifest. `ArtifactHandler` **MUST** therefore refuse to serve any hash that is not referenced by at least one entry in the *currently loaded* (i.e. already-published-only) manifest, returning the same 404 `artifact-not-found` used for an unknown or withdrawn hash (§3.6) — an unpublished artifact and a nonexistent one are indistinguishable to a client, which is the point.
- Publishing/unpublishing is a normal `circuitctl` operation — `circuitctl publish <id>` / `circuitctl unpublish <id>` (§5.3) — that flips the flag on the entry's own JSON file and regenerates the manifest, exactly like `deprecate`/`revoke` do for `status`. Unlike `revoke`, this is not a security-fail-closed action in the client-facing sense (a client never sees an unpublished entry either way), so no `--reason` is required, though one is accepted and recorded in `notes` for audit purposes.
- **Default:** `circuitctl add` sets `published: true` unless `--unpublished` is passed — the common case (clean provenance, ready to go live) should need no extra flag. Separately, at the schema level, an entry JSON file that omits the `published` key entirely (e.g. hand-edited, or written before this field existed) unmarshals to `false` — Go's zero value for `bool` — which is the fail-closed behavior you want for anything not written by a tool that actually made the decision.

### 2.5 Longfellow `params`

Keys chosen to match `ZkSystemSpec`'s existing names exactly (snake_case, unlike the envelope's camelCase — this inconsistency is deliberate: the envelope is *ours*, the params are *multipaz's*, and silently renaming them would be the lossy transform §2.1 exists to avoid).

| Key | Type | Notes |
|---|---|---|
| `version` | number | Circuit version, `8`. Same value as envelope `systemVersion`, in the form `ZkSystemSpec` wants it. |
| `num_attributes` | number | Attribute count the circuit is built for. Part of the hard compatibility key (§7.1). |
| `block_enc_hash` | number | From the artifact filename convention (`4307` in the example below). |
| `block_enc_sig` | number | From the filename convention (`2945`). |
| `circuit_hash` | string | Hex. The circuit's *own* identity hash as used by libzk/multipaz. **See the warning in §2.6 — this is NOT necessarily the hash of the bytes we serve.** |
| `upstream_filename` | string | The canonical upstream filename, e.g. `8_2_4307_2945_bb8e6a26d2700ddad968562d1c4aee83067772fee6f889748a0bc64f2c694ad5`. Recorded because every other consumer in the ecosystem identifies circuits by this string, so it is the field a human will actually grep for. |

### 2.6 `artifact` object — and the two-hashes trap

```
artifact: {
  url:          string   // REQUIRED. Absolute or root-relative. Content-addressed path.
  hash:         string   // REQUIRED. "sha256:<lowercase-hex>" of the bytes AS SERVED.
  size:         number   // REQUIRED. Byte length of the bytes AS SERVED.
  compression:  string   // REQUIRED. "zstd" | "none".
  mediaType:    string   // REQUIRED. e.g. "application/zstd".
  uncompressed: {         // OPTIONAL but SHOULD be present when compression != "none"
    hash: string          // "sha256:<hex>" of the decompressed bytes
    size: number          // decompressed byte length
  }
}
```

**⚠️ The most important detail in this whole schema.** There are up to three distinct hashes in play and conflating them will produce a bug that only shows up against a real verifier:

1. `artifact.hash` — hash of the **exact bytes the service serves** (zstd-compressed). This is the *transport integrity* hash. It is what content-addressing uses, what the client verifies after download, and the only hash the service itself has any opinion about.
2. `artifact.uncompressed.hash` — hash of the **decompressed** circuit bytes. Lets a client that has already decompressed (and possibly stored the decompressed form) re-verify without re-compressing. Also catches a zstd-level mismatch, since zstd compression is not bit-deterministic across encoder versions/settings — *the same circuit compressed twice may have two different `artifact.hash` values*, which is exactly why the uncompressed hash matters as the stable identity.
3. `params.circuit_hash` — libzk's/multipaz's **own** identifier for the circuit, taken from the upstream filename. Its exact preimage (compressed? uncompressed? a structural hash of the circuit representation rather than of any file?) **is not established** and is an open question (§9.4).

Rules:

- A client **MUST** verify `artifact.hash` against downloaded bytes before using or storing them as valid (§6.3).
- A client **MUST NOT** assume `params.circuit_hash == artifact.hash` or `== artifact.uncompressed.hash`. It is metadata for the proof system, not an integrity check for us.
- `circuitctl` (§5.3) **MUST** compute 1 and 2 itself and **MUST** carry 3 across from the filename without reinterpreting it.

`artifact.url` **SHOULD** be root-relative (`/v1/artifacts/sha256/<hex>`) so the catalog is host-portable — a client resolves it against the base URL it fetched the manifest from. Absolute URLs are permitted (they allow a CDN or bucket offload without changing the client), but a client **MUST** reject an absolute `artifact.url` whose scheme is not `https` (§8.4).

### 2.7 `system` registry

`system` is a short lowercase token. Known values:

| `system` | Status | Artifact shape |
|---|---|---|
| `longfellow` | Live target | zstd-compressed circuit blob, keyed by `(version, num_attributes, block_enc_hash, block_enc_sig, circuit_hash)`. |
| `vega` | Anticipated | **Unknown.** May be proving keys (~464 KB reported), may need no artifact at all. Deliberately unconstrained. §9.5. |
| `bbs` | Anticipated | Almost certainly **no artifact** — BBS is signature-based and needs no circuit. Such an entry would be metadata-only; see §2.9. |

New `system` values may be added without a schema version bump. A client **MUST** ignore entries whose `system` it does not know (§7.2) — this is what makes adding Vega a non-event for shipped Longfellow-only clients.

### 2.8 `source` object (provenance)

```
source: {
  origin:      string   // REQUIRED. Where these bytes came from, as a URL or clear locator.
                        //   e.g. "https://github.com/abetterinternet/zk-cred-longfellow/pull/222"
  originRef:   string   // OPTIONAL. Commit SHA / tag / PR number pinning the origin.
  originPath:  string   // OPTIONAL. Path within the origin.
  toolchain:   string   // OPTIONAL but see §2.8.1 — what actually produced these bytes: compiler/tool
                        //   name+version, build command, or a pointer to the build script/CI job that ran it.
                        //   e.g. "zk-cred-longfellow @ a1b2c3d, cargo build --release --target wasm32, rustc 1.84.0"
  license:     string   // OPTIONAL but see §9.6 — SPDX id or short statement.
  openSource:  boolean  // REQUIRED, see §2.8.1 — explicit human-reviewable claim, not inferred from `license`.
  addedBy:     string   // REQUIRED. Human or automation that published it (git identity is fine).
  verifiedBy:  VerificationRecord[] // OPTIONAL. Structured, machine-readable interop confirmations. §5.7.
}

VerificationRecord: {
  tool:             string  // REQUIRED. e.g. "circuitctl verify-interop", "siros-verifier-cli".
  toolVersion:      string  // REQUIRED. e.g. "0.4.1".
  verifierIdentity: string  // REQUIRED. What it was checked against, e.g. "multipaz-verifier-server (balfanz/ppid)".
  date:             string  // REQUIRED. RFC 3339 UTC — when the check ran, not when the entry was edited.
  result:           enum    // REQUIRED. "accepted" | "rejected".
  notes:            string  // OPTIONAL. Free text, e.g. failure detail. Clients MUST NOT parse it.
}
```

This is cheap to record and expensive to reconstruct later. When a proof fails against a real verifier six months from now, `source` is what tells you whether you are holding the same bytes the upstream PR intended. `verifiedBy` in particular is how a human — or a UI — decides whether an entry is interop-proven or merely present.

**[DECISION, 2026-08-13] `verifiedBy` is structured, not free-text strings**, precisely so it is a real informational element of the manifest rather than a log line only a human reads. This lets a client or an ops dashboard answer "has this circuit been checked against a real verifier, against which one, and when" *without string-parsing*, the same way `params` (§2.2) stays a typed bag rather than opaque text. It carries **no trust authority** — a client's decision to use a circuit is still governed entirely by the compile-time pin table (§7.3) and the hash check (§6.3), never by what `verifiedBy` claims. It is descriptive metadata, useful for humans, dashboards, and `circuitctl ls --stale` (§5.7) — not a security control. Populated exclusively by `circuitctl verify-interop` (§5.7), never hand-edited, so an entry's interop-verified claim is exactly as trustworthy as the CI-enforced tooling that produced it.

### 2.8.1 `toolchain` and `openSource` — human-traceable provenance, not just machine metadata

**[DECISION, 2026-08-13]** `origin`/`originRef`/`originPath` answer "where did these bytes come from"; they do not answer "what actually produced these specific bytes" or "can I legally redistribute and use them" — two questions a human reviewer needs to answer *without* going and reading the upstream repo's build system themselves. The `published` gate (§2.4.1) controls *whether* an entry is visible at all; `toolchain`/`openSource` control *how much a human can verify once it is*.

- **`toolchain`** records the actual build provenance: which compiler/tool, which version, which command, or which CI job produced the artifact bytes — not just which repo they came from. A circuit is a compiled/generated artifact, not source code; "here's the GitHub URL" doesn't tell a reviewer whether they could reproduce these exact bytes, or whether the toolchain itself is trustworthy (spec §8.3's parser-exposure risk is precisely about *what a toolchain produced*, not just *whose repo it came from*). Free text, not machine-parsed — but a **required discipline** for anyone running `circuitctl add`, not optional documentation.
- **`openSource`** is a required boolean, deliberately **not derived from `license`** even though the two are related. Reasons: (1) a human reviewer shouldn't need to know SPDX-to-OSI-approved mappings to answer "is this open source" at a glance on the website (§1.6) — an explicit boolean is legible without that background; (2) `license` can be present-but-wrong, present-but-unconfirmed, or absent entirely (exactly the current state of the v8 entry, §Appendix C) — collapsing "there's a license string" into "yes it's open source" would be a silent overclaim; (3) it forces the publisher (human running `circuitctl add`) to make an affirmative claim rather than have the tooling infer one. **Default is `false`** (fail-closed, same rationale as `published`'s zero-value default, §2.4.1) — `circuitctl add` requires an explicit `--open-source` flag to assert `true`; omitting it is not an oversight-prone default; it's "unconfirmed, treat as not open source until someone says otherwise."
- **Together with the rest of `source`**, these make every *published* entry's provenance card on `circuits.siros.org` (§1.6) answer, without the reviewer leaving the page: where did this come from (`origin`/`originRef`/`originPath`, linked), what built it (`toolchain`), can I use it (`license` + `openSource`), who vouches for it (`addedBy`, `verifiedBy`). An entry missing any of these isn't hidden — the website renders the gap honestly ("toolchain: not recorded", "open source: not confirmed") rather than omitting the field, because the point of this section is that absence of information is itself information a reviewer needs.

### 2.9 Metadata-only entries

An entry **MAY** omit `artifact` entirely if its system needs no downloadable artifact (the expected BBS case, possibly Vega). Such an entry still carries `id`/`system`/`systemVersion`/`params`/`status` and still serves the discovery function: it tells a client "this system+version is a thing that exists and here are its parameters".

Clients **MUST** handle a missing `artifact` without erroring — a plugin that needs no bytes resolves such an entry to a spec-only handle (§6.2). This is the concrete reason the schema separates "catalog entry" from "downloadable file" rather than treating them as one thing, and it is the cheapest available hedge against the parent plan's open question about whether Vega needs circuit files at all.

### 2.10 Manifest document

```
{
  "manifestVersion": 1,
  "generatedAt":     "<RFC3339>",
  "catalog":         "siros-zk-circuits",
  "circuits":        [ CircuitDescriptor, ... ],
  "next":            null
}
```

- `manifestVersion` — integer, schema version of *this document*. Bumped only on a breaking change to the envelope, in lockstep with the URL path prefix (`/v1/` ↔ `manifestVersion: 1`). A client **MUST** refuse a `manifestVersion` it does not know rather than best-effort-parse it.
- `generatedAt` — when the manifest was generated (not fetched). Useful for "how stale is my cached copy" logging and for spotting a stuck publish pipeline.
- `catalog` — a constant string identifying *which* catalog this is. Exists so that a misconfigured base URL pointing at some other JSON document fails loudly instead of parsing as an empty catalog.
- `circuits` — the entries. Order is not significant; clients MUST NOT depend on it.
- `next` — reserved for pagination (§3.7). `null` in v1. Clients **MUST** follow it if non-null; publishing it before it is needed is free, and it is the one field that is very annoying to add later.

### 2.11 Example manifest

```json
{
  "manifestVersion": 1,
  "generatedAt": "2026-08-13T21:40:11Z",
  "catalog": "siros-zk-circuits",
  "circuits": [
    {
      "id": "longfellow-libzk-v1_8_2_4307_2945",
      "aliases": ["longfellow-8-2"],
      "system": "longfellow",
      "systemVersion": "8",
      "docTypes": ["org.iso.18013.5.1.mDL", "eu.europa.ec.eudi.pid.1"],
      "published": true,
      "status": "active",
      "params": {
        "version": 8,
        "num_attributes": 2,
        "block_enc_hash": 4307,
        "block_enc_sig": 2945,
        "circuit_hash": "bb8e6a26d2700ddad968562d1c4aee83067772fee6f889748a0bc64f2c694ad5",
        "upstream_filename": "8_2_4307_2945_bb8e6a26d2700ddad968562d1c4aee83067772fee6f889748a0bc64f2c694ad5"
      },
      "artifact": {
        "url": "/v1/artifacts/sha256/9f2c1d5e4b8a37c60d1e2f4a5b6c7d8e90a1b2c3d4e5f60718293a4b5c6d7e8f",
        "hash": "sha256:9f2c1d5e4b8a37c60d1e2f4a5b6c7d8e90a1b2c3d4e5f60718293a4b5c6d7e8f",
        "size": 312474,
        "compression": "zstd",
        "mediaType": "application/zstd",
        "uncompressed": {
          "hash": "sha256:3a7b9c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3",
          "size": 1048576
        }
      },
      "source": {
        "origin": "https://github.com/abetterinternet/zk-cred-longfellow/pull/222",
        "originRef": "pr-222",
        "originPath": "test-vectors/mdoc_zk/8_2_4307_2945_bb8e6a26....zst",
        "toolchain": "zk-cred-longfellow @ pr-222, cargo build --release --target wasm32-unknown-unknown, rustc 1.84.0",
        "license": "MPL-2.0 (confirmed redistributable — see spec §9.6)",
        "openSource": true,
        "addedBy": "leifj@siros.org",
        "verifiedBy": [
          {
            "tool": "circuitctl verify-interop",
            "toolVersion": "0.1.0",
            "verifierIdentity": "multipaz-verifier-server (balfanz/ppid)",
            "date": "2026-08-20T09:00:00Z",
            "result": "accepted"
          }
        ]
      },
      "publishedAt": "2026-08-13T21:40:11Z",
      "notes": "Primary v8/PPID target for SDK Phase 3. Hashes above are ILLUSTRATIVE placeholders; real values come from circuitctl at publish time."
    },
    {
      "id": "longfellow-libzk-v1_7_1_2100_1800",
      "system": "longfellow",
      "systemVersion": "7",
      "docTypes": ["org.iso.18013.5.1.mDL"],
      "status": "deprecated",
      "params": {
        "version": 7,
        "num_attributes": 1,
        "block_enc_hash": 2100,
        "block_enc_sig": 1800,
        "circuit_hash": "0000000000000000000000000000000000000000000000000000000000000000",
        "upstream_filename": "7_1_2100_1800_0000...0000"
      },
      "artifact": {
        "url": "/v1/artifacts/sha256/1111111111111111111111111111111111111111111111111111111111111111",
        "hash": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
        "size": 287001,
        "compression": "zstd",
        "mediaType": "application/zstd"
      },
      "source": {
        "origin": "https://github.com/openwallet-foundation/multipaz",
        "originPath": "multipaz-longfellow/src/commonMain/circuits/",
        "toolchain": "not recorded — vendored from multipaz's public repo before circuitctl existed",
        "openSource": true,
        "addedBy": "leifj@siros.org"
      },
      "publishedAt": "2026-08-13T21:40:11Z",
      "deprecatedAt": "2026-08-13T21:40:11Z",
      "notes": "v7 kept only for interop against multipaz public main (v6/v7 only, no v8). Not a PPID target."
    }
  ],
  "next": null
}
```

The v7 entry is illustrative of a real use: it documents the *other* side of the parent plan's interop constraint (public multipaz has no v8), so a developer chasing a verification failure can see both circuits in one place.

---

## 3. API design

### 3.1 Design constraint that shapes everything below

**[DECISION] Every endpoint in the read API MUST be implementable by a dumb static file server.**

This is the single most consequential API decision. It means:

- No required query parameters, ever.
- No server-side filtering the client depends on.
- Every resource is a file at a deterministic path.
- All caching semantics expressible with `ETag` / `Cache-Control` / `Last-Modified`.

Why: it keeps the hosting decision (§4) *reversible*. We can serve from a Go service today and a bucket+CDN tomorrow — or run both, or fail over from one to the other — with **zero client changes**. Given that the correctness of the whole system rests on the client's hash check rather than on any server behaviour (§8.3), giving the server a monopoly on some query capability buys nothing and costs us that flexibility. Optional convenience endpoints (§3.5) may exist on the Go implementation; clients **MUST** function without them.

### 3.2 URL layout

```
GET  ${CIRCUITS_BASE_URL}/v1/manifest.json              # full catalog
GET  ${CIRCUITS_BASE_URL}/v1/circuits/{id}.json         # single entry
GET  ${CIRCUITS_BASE_URL}/v1/artifacts/{alg}/{hex}      # artifact bytes, content-addressed
HEAD <any of the above>                                 # same headers, no body

# Optional, Go implementation only (§3.5):
GET  ${CIRCUITS_BASE_URL}/v1/circuits?system=…&version=…&numAttributes=…

# Operational (Go implementation only, matching go-trust's conventions):
GET  ${CIRCUITS_BASE_URL}/healthz
GET  ${CIRCUITS_BASE_URL}/readyz
GET  ${CIRCUITS_BASE_URL}/metrics
GET  ${CIRCUITS_BASE_URL}/info
```

Only `GET` and `HEAD` are supported on the read API. Everything else returns **405** with an `Allow: GET, HEAD` header. There is no write API — publishing goes through git (§5).

`/v1/` is the schema-version prefix and moves in lockstep with `manifestVersion`. On a breaking schema change, `/v2/` is added and `/v1/` continues to be served for **at least 12 months** (rationale: mobile SDK adoption is slow and an old wallet build losing ZK capability is a silent, user-visible regression).

### 3.3 `GET /v1/manifest.json`

The primary endpoint. Returns the document from §2.10.

**Response headers:**

| Header | Value | Why |
|---|---|---|
| `Content-Type` | `application/json` | |
| `ETag` | strong, `"sha256-<first16hex>"` of the canonical manifest bytes | Enables conditional revalidation; content-derived so it is stable across redeploys of unchanged content (important — a restart-derived ETag would cause every client to re-download the manifest on every deploy). |
| `Cache-Control` | `public, max-age=300, stale-while-revalidate=86400` | Short freshness because the manifest is the mutable part; long stale window because a stale manifest is nearly always still correct. |
| `Last-Modified` | `generatedAt` | |
| `X-Catalog-Generated-At` | `generatedAt` | Convenience for `HEAD`-based staleness checks and for debugging a stuck publish. |

**Status codes:** `200`, `304` (on `If-None-Match` / `If-Modified-Since` match), `429` (rate limited, Go option only), `503` (draining).

```bash
# Fetch the catalog
curl -sS https://api.circuits.siros.org/v1/manifest.json | jq .

# Conditional revalidation — the normal client path after first fetch
curl -sS -D- -o /dev/null \
  -H 'If-None-Match: "sha256-9f2c1d5e4b8a37c6"' \
  https://api.circuits.siros.org/v1/manifest.json
# → HTTP/2 304

# Cheap staleness check without transferring the body
curl -sSI https://api.circuits.siros.org/v1/manifest.json | grep -i x-catalog-generated-at
```

### 3.4 `GET /v1/circuits/{id}.json`

Returns a **single** `CircuitDescriptor` (the bare object from §2.4, not wrapped in a manifest envelope), for a given `id` **or alias**.

Purpose: it lets a client that already knows its `id` — which, per §7, is the *normal* case for a strict-pinned client — skip fetching and parsing the whole catalog. On a mobile connection that is a ~1 KB response instead of a manifest that grows without bound.

- `id` must satisfy §2.3's charset. A non-conforming `id` → **400**.
- Unknown `id` → **404** with the error body from §3.8.
- If `{id}` is an **alias**, the Go implementation **SHOULD** respond `301` to the canonical `/v1/circuits/{canonicalId}.json`. The static implementation instead serves a duplicate JSON file at the alias path (both behaviours are conformant; clients **MUST** follow up to 3 redirects on this endpoint and **MUST** treat a `200` at an alias path as authoritative).
- Same caching headers as §3.3, but `Cache-Control: public, max-age=3600` — a single entry changes far less often than the catalog as a whole (in practice only `status` ever changes, per §5.6).

```bash
curl -sS https://api.circuits.siros.org/v1/circuits/longfellow-libzk-v1_8_2_4307_2945.json | jq .
curl -sSL https://api.circuits.siros.org/v1/circuits/longfellow-8-2.json | jq -r .id   # alias
```

### 3.5 `GET /v1/circuits?…` (optional convenience)

Filtered listing. **Optional server capability; no client may depend on it.** Returns the same envelope as §2.10 with a filtered `circuits` array.

Parameters (all optional, AND-combined, exact match): `system`, `version` (matches `systemVersion`), `numAttributes` (matches `params.num_attributes`), `docType` (membership in `docTypes`), `status` (defaults to `active`; `status=all` returns everything).

Unknown query parameters **MUST** be ignored, not rejected — so a newer client sending a filter an older server doesn't know gets a superset, which it then filters locally. Fail-open here is correct because the client filters locally anyway.

This endpoint exists for humans and for curl-driven debugging more than for clients. It is the first thing to cut if the Go implementation is running late.

```bash
curl -sS 'https://api.circuits.siros.org/v1/circuits?system=longfellow&version=8&numAttributes=2' | jq '.circuits[].id'
```

### 3.6 `GET /v1/artifacts/{alg}/{hex}` — content-addressed download

The bytes. `{alg}` is `sha256` (the only algorithm for v1; the path segment exists so `sha384`/`blake3` can be added without a new URL shape). `{hex}` is lowercase hex, exactly 64 chars for `sha256`.

The address **is** the integrity guarantee: a client that knows the hash can verify what it got without consulting the manifest at all. This is what makes a cached copy self-validating (§6.3) and what makes the manifest non-authoritative for content (§8.3).

**Response headers:**

| Header | Value | Why |
|---|---|---|
| `Content-Type` | `application/zstd` (from `artifact.mediaType`) | |
| `Content-Length` | exact size | Lets a client abort early on a size mismatch before hashing. |
| `ETag` | strong, `"sha256:<hex>"` — i.e. the content address itself | The resource is immutable, so the address is the perfect ETag. |
| `Cache-Control` | `public, max-age=31536000, immutable` | Content-addressed ⇒ can never change ⇒ cache forever. This is what makes CDN/OS-level caching free and correct. |
| `Accept-Ranges` | `bytes` | |
| `X-Content-SHA256` | `<hex>` | Redundant with the path, but it makes a `HEAD` self-describing and a proxy misconfiguration obvious. |

**Range requests: SHOULD be supported.** These are 300 KB downloads to mobile devices on unreliable networks; resumability is worth having, and both the Go option (`http.ServeContent`) and the static option (bucket/CDN) get it essentially for free. A client that resumes **MUST** hash the fully-reassembled bytes (§6.3) — partial verification is not verification.

**Status codes:** `200`, `206` (range), `304`, `404` (unknown hash — note this is also the response for a *withdrawn* artifact, §5.6), `400` (malformed `{alg}`/`{hex}`), `416` (unsatisfiable range), `429`, `503`.

An artifact **MUST** remain retrievable as long as any published manifest references it, and **SHOULD** remain retrievable indefinitely thereafter — an old wallet build re-installing on a new device needs the circuit its code was pinned to, which may be several manifest generations old. Deleting artifacts is the one operation that can brick a shipped client; §5.6 covers the narrow exception.

```bash
# Download and verify in one line — the whole integrity story
curl -sS -o circuit.zst \
  https://api.circuits.siros.org/v1/artifacts/sha256/9f2c1d5e4b8a37c60d1e2f4a5b6c7d8e90a1b2c3d4e5f60718293a4b5c6d7e8f
sha256sum circuit.zst
# → must equal the hex in the URL, and artifact.hash in the manifest

# Metadata only
curl -sSI https://api.circuits.siros.org/v1/artifacts/sha256/9f2c…e8f

# Verify the decompressed form too (artifact.uncompressed.hash)
zstd -d -c circuit.zst | sha256sum
```

### 3.7 Pagination

**[DECISION] No pagination in v1.** The catalog will hold single-digit-to-tens of entries for years; each entry serialises to well under 1 KB, so the whole manifest is a few KB. Paginating that would be pure ceremony, and every client would carry loop logic it never exercises — which means untested loop logic, which is worse than none.

What is specced instead:

- The `next` field exists in the envelope from day one, always `null` (§2.10). Clients **MUST** implement following it — a 5-line loop — even though it will never fire in v1. This is the compromise: the *protocol* is paginated, the *deployment* is not.
- Reserved parameters `?cursor=` and `?limit=` are defined but unimplemented. A server that does not implement them **MUST** ignore them (per §3.5's fail-open rule) and return the full catalog with `next: null`.
- **Concrete trigger to actually implement it:** manifest exceeding **512 KB** or **500 entries**, whichever comes first. At that point implement opaque-cursor pagination (`next` = absolute URL, `limit` default 100, max 500) and, more importantly, expect clients to have migrated to `/v1/circuits/{id}.json` lookups by then anyway.

### 3.8 Error responses

All errors: `application/problem+json` (RFC 9457), because `go-trust`'s API surface is gin-based and this is the low-friction standard choice, and because a machine-readable `type` beats string-matching a message.

```json
{
  "type":   "https://api.circuits.siros.org/problems/artifact-not-found",
  "title":  "Artifact not found",
  "status": 404,
  "detail": "No artifact with sha256:9f2c… is present in this catalog.",
  "instance": "/v1/artifacts/sha256/9f2c…"
}
```

The **static-host option cannot produce these** — it will return whatever the bucket/CDN returns for a missing key. Therefore: clients **MUST** treat the status code as authoritative and the body as advisory-only, and **MUST NOT** require a parseable JSON body on a non-2xx response. This is a real constraint the client implementer needs to internalise, since it is a common source of "works against staging, crashes against prod-behind-CDN" bugs.

### 3.9 OpenAPI

The Go implementation **SHOULD** publish OpenAPI 3.x via `swaggo/swag` annotations, matching `go-trust`'s existing `make swagger` build step and `swaggo/gin-swagger` serving. This is a low-cost consistency win: it is already in the org's Makefile vocabulary, and it gives the SDK implementers a browsable contract.

---

## 4. Hosting / architecture decision

### 4.1 Requirements the hosting must satisfy

1. Serve a few-KB JSON document with `ETag`/`Cache-Control`.
2. Serve immutable ~300 KB blobs at content-addressed paths, with `ETag`, long-lived caching, and ideally `Range`.
3. HTTPS on a stable, org-controlled hostname.
4. A publish path with review and rollback.
5. Not become an operational burden.

That is genuinely all. Note what is absent: no database, no auth, no per-request computation, no state.

### 4.2 Option A — static file host (bucket + CDN)

Objects in S3/GCS/R2 behind a CDN, laid out exactly as §3.2 (`/v1/manifest.json`, `/v1/circuits/*.json`, `/v1/artifacts/sha256/*`). No server code at all. Publishing = `aws s3 sync` / `rclone` from CI.

**Pros**

- Zero server code, zero server bugs, zero patching, zero runtime dependencies.
- Best-in-class availability and global latency for the exact workload (immutable blobs + a tiny JSON doc) without us engineering for it.
- Effectively free at our volume; scales past any plausible traffic with no thought.
- Cannot be compromised via application logic, because there is none.
- Bandwidth-heavy blob serving is what these products are for.

**Cons**

- **A new cloud vendor + IAM + CDN + custom-domain + TLS setup for this org.** This is the real cost: the org's operational muscle memory is Fly.io + Go. Introducing a bucket means new credentials in CI, a new billing relationship, a new place to look when something breaks, and a new thing nobody else on the team has touched.
- No `/metrics` in the org's Prometheus idiom; observability is whatever the vendor's dashboard gives.
- Cannot implement §3.5's convenience filtering, §3.8's structured errors, or alias redirects (needs duplicate files instead).
- No place to put future server-side logic (a signing endpoint, an upload API) without adding a service anyway.
- Ironically weaker *deploy provenance* than a git-backed container image unless the sync pipeline is carefully built.

### 4.2.1 Static hosting variants worth naming specifically

§4.2 above assumed "a cloud bucket + CDN" as the shape of Option A, but that's not the only way to get a zero-server-code static host — two more concrete variants, since they change the cost/risk tradeoff meaningfully:

**GitHub Pages / raw + jsDelivr (git-native static hosting).** Serve `/v1/*` straight from a GitHub repo — either GitHub Pages (a branch/folder published as a static site) or `cdn.jsdelivr.net/gh/sirosfoundation/<repo>@<tag>/<path>` fronting the repo's raw content through jsDelivr's real CDN.
- **Pros:** genuinely zero new vendor — no IAM, no billing relationship, no new credential in CI. jsDelivr specifically gives real `Cache-Control: immutable, max-age=1y`-class caching for anything addressed by a git tag/commit SHA, which maps well onto content-addressed, immutable artifact paths (§2.6/§5.4).
- **Cons:** plain GitHub Pages has **no custom response headers** (fights §3.4's caching requirements); jsDelivr fixes that but adds a *second* external dependency on top of GitHub, with its own cache-purge propagation delay on first publish of a new tag. No `/metrics`, no §3.5 filtering, no alias redirects, no place for future server logic. GitHub repo-size limits arrive before Option A's own thresholds (§4.5) would.
- **[DECISION, 2026-08-13] Rejected, including as a Phase 1 stopgap.** Considered as a lightweight way to get "real bytes at a real URL" before the real service exists, but rejected even for that purpose: this project should be built as a real service from day one rather than through an intermediate throwaway artifact. Phase 1 (§10) targets the actual Go service directly - see the revised phasing there.

**Self-hosted (a box we run, not Fly).** A small VM/bare-metal host (anywhere - existing org infra, a colo box, a cheap VPS) running the same static files or the same Go binary, outside Fly's managed platform.
- **Pros:** no per-request/bandwidth billing surprises from a managed platform; full control over the box.
- **Cons:** this is *strictly worse* than either option above for this specific workload — it reintroduces exactly the "process to keep alive, patch, and monitor" cost of Option B (§4.3's con list) *without* Fly's managed layer (automatic TLS, health-check restarts, `fly releases rollback`, the org's existing on-call familiarity with the platform). There is no requirement in §4.1 that self-hosting satisfies better than Fly or a bucket already do. **Not recommended** unless there's an org-level reason to run infrastructure outside Fly entirely (e.g. a data-residency requirement) that isn't specific to this service - if that reason exists, it applies to every other Go service this org runs too, and should be decided at that level, not here.

None of this changes §4.4's recommendation - it just broadens what "Option A" could concretely mean if the trigger conditions in §4.4 are ever hit (§9.1's hostname decision, migration is DNS-only regardless of which Option A variant is chosen later).

### 4.3 Option B — small Go service on Fly.io

A `github.com/sirosfoundation/go-zk-circuits` service: gin router, artifacts and generated manifest **compiled into the binary via `go:embed`**, deployed as a container to Fly.io exactly as the org's other Go services are.

Verified against this repo (`go-trust`) as the pattern to copy: Go 1.26.5, `gin-gonic/gin`, `swaggo/swag` + `gin-swagger` for OpenAPI, `prometheus/client_golang` for `/metrics`, existing `/healthz` + `/readyz` + `/status` + `/info` handlers, an in-house per-IP rate-limit middleware (`pkg/api/ratelimit.go`), multi-stage `golang:1.26.5-alpine` → `alpine:3.24` Dockerfile, `Makefile` with `fmt`/`vet`/`test`/`lint`/`build`/`swagger` targets, `golangci-lint` + `gosec` + `staticcheck` in CI. All of that is directly reusable; none of it has to be designed.

**Pros**

- **Zero new infrastructure concepts for this org.** Same language, same router, same deploy target, same CI shape, same metrics/health conventions, same secrets story (none needed). A colleague who has touched `go-trust` can operate this on day one.
- `go:embed` makes the artifacts part of the immutable, git-derived, reviewable container image: **the deploy artifact and the catalog are the same object**. Rollback is `fly releases rollback` and it rolls back the catalog *and* the manifest atomically. That is a genuinely strong provenance and publish story (§5), and it is hard to match with a sync-based bucket pipeline.
- `net/http`'s `ServeContent` gives correct `ETag`, `If-None-Match`, `Range`/`206`, and `HEAD` handling for embedded files essentially for free.
- Room for the optional endpoints (§3.5), structured errors (§3.8), alias redirects, `/metrics`, and any future server logic.
- Trivially runnable locally and in tests — the SDK client's integration tests can spin up the real server (`make test-integration` already exists as a pattern here) instead of mocking HTTP.

**Cons**

- It is a process to keep alive, patch, and monitor for a workload that needs no compute. A bucket cannot have an outage caused by our code; this can.
- Serving 300 KB blobs from a single Fly region has worse tail latency than a CDN, and bandwidth costs sit on the Fly bill.
- `go:embed` puts a ceiling on corpus size (binary size = corpus size) and makes the image grow with every published circuit. Mitigation and threshold in §4.5.
- Slightly more code to review than "no code".

### 4.4 [DECISION] Recommendation

**Adopt Option B — a small Go service on Fly.io with `go:embed`ed artifacts — while holding the *wire contract* to Option A's constraints (§3.1) so the choice stays reversible.**

Rationale:

1. **The hard part of this project is not serving files; it is the SDK client, the compatibility rules, and the publish discipline.** The hosting decision should therefore be optimised for *lowest friction to ship*, and for this org that is unambiguously "another Go service on Fly" — the pattern is proven, the CI is copy-pasteable, and it introduces no new vendor, credential, or on-call surface. Introducing an S3/GCS/CDN dependency to avoid ~300 lines of `http.ServeContent` wiring is a bad trade *for this team*.
2. **`go:embed` collapses the publish problem into the deploy problem**, which the org has already solved well. Artifacts land in git via PR, CI builds an image, Fly deploys it, and manifest+bytes are atomically consistent by construction. There is no window where the manifest advertises a hash the host does not yet have — a real failure mode of any two-step sync pipeline, and a nasty one because it looks like a client bug.
3. **Reversibility makes the decision cheap.** Because §3.1 forbids any endpoint a static host cannot serve, moving to a bucket later is: sync the same paths, repoint DNS. No client changes, no schema changes, no coordination with shipped mobile apps. A decision you can undo for the cost of a DNS change does not deserve a long argument — so make the low-friction one now.
4. **The Go option's cons are all size- and scale-driven**, and §1.5's sizing says we are two orders of magnitude away from them. The bucket's cons are *setup*-driven and land on day one.

**When to switch to Option A** (write these triggers into the repo README so the decision is revisited by evidence, not vibes):

- Total embedded corpus exceeds **100 MB**, or container image build/push time becomes annoying.
- Egress becomes a noticeable line item on the Fly bill.
- Non-siros consumers start using it at volume (i.e. it graduates from internal/interop to public infrastructure).
- Anyone proposes an availability SLO for it.

An intermediate step exists and **SHOULD** be preferred over a full migration if only the first trigger fires: keep the Go service as the origin, and put a CDN in front of `/v1/artifacts/*` (immutable + long `max-age` + content-addressed makes this perfectly safe). That gets the CDN's bandwidth and latency without giving up the deploy story.

### 4.5 Storage strategy inside the Go service, with thresholds

| Corpus size | Strategy |
|---|---|
| **< 100 MB** (expected for years) | `go:embed` — artifacts in the repo, compiled into the binary. Atomic, reviewable, no runtime storage config. **This is the v1 choice.** |
| 100 MB – 1 GB | Move artifacts to a **Fly volume** (or Tigris/S3-compatible object storage) and keep only the manifest embedded. Loses atomicity between manifest and bytes; requires a publish-order rule (**upload bytes first, publish manifest second** — always, so the manifest never advertises a hash that isn't there yet). |
| > 1 GB | Switch to Option A outright; the Go service no longer earns its keep. |

Also: consider Git LFS for the repo once in-tree artifacts pass **~200 MB total** (git history accumulates every version ever published, so this arrives before the embed limit does if circuits are frequently replaced). §9.7.

### 4.6 Deploy story (Go option)

Mirroring the org's established Fly.io model (`sirosid-dev` orchestrates sibling services this way):

- **Repo:** `github.com/sirosfoundation/go-zk-circuits` (name TBD, §9.2).
- **Layout** (following `go-trust`'s shape):
  ```
  cmd/zkc/                  main
  pkg/api/                  gin router, handlers, health, metrics, ratelimit
  pkg/catalog/              manifest types, loading, validation, embed
  pkg/publish/              circuitctl logic (shared with the CLI)
  cmd/circuitctl/           publish CLI (§5.3)
  artifacts/sha256/<hex>    the blobs (go:embed target)
  catalog/circuits/*.json   per-entry source-of-truth files (§5.2)
  catalog/manifest.json     GENERATED — never hand-edited
  Dockerfile                multi-stage golang:1.26-alpine → alpine
  fly.toml                  Fly app config
  Makefile                  fmt vet test lint build swagger
  ```
- **Build:** multi-stage Dockerfile as in `go-trust`, but **`CGO_ENABLED=0`** — unlike `go-trust` there is no PKCS#11 dependency, so a fully static pure-Go binary and a `scratch`/`distroless` runtime image are available. Take them; smaller attack surface, faster builds.
- **Runtime config** (env vars, all optional with sane defaults — the service must run correctly with no configuration at all, which is a property worth protecting):
  | Var | Default | Purpose |
  |---|---|---|
  | `ZKC_LISTEN` | `:8080` | Bind address. |
  | `ZKC_BASE_URL` | derived from request | Used only to absolutise `next` and problem `type` URIs. |
  | `ZKC_LOG_LEVEL` | `info` | |
  | `ZKC_RATE_LIMIT_RPS` | `20` | Per-IP token bucket (§8.5). |
  | `ZKC_RATE_LIMIT_BURST` | `60` | |
  | `ZKC_ARTIFACT_DIR` | *(unset ⇒ use embedded)* | Escape hatch for the volume strategy (§4.5) and for local dev. |
- **Fly:** single app, 1–2 machines, smallest instance (256 MB is generous for a static-file server), `min_machines_running = 1`. **Do not rely on Fly autostart/scale-to-zero** for this: a cold start in front of a wallet that is mid-presentation and needs its circuit *now* is exactly the latency you do not want, and the cost saving is trivial.
- **Scaling:** stateless and read-only ⇒ horizontally scalable with no coordination. Add machines/regions if latency warrants; no shared state to worry about.
- **Observability:** `/metrics` in the Prometheus idiom already used here. Minimum useful series: manifest requests (by status), artifact requests (by artifact id/hash and status), bytes served, `304` ratio (the health signal for client caching working correctly — a low `304` ratio means clients are re-downloading and something is wrong), catalog entry count, build/version info. Deliberately **no per-client identifiers** in metrics or logs (§8.6).
- **CI:** copy `go-trust`'s workflow shape — `make all` (fmt, vet, test, build), `golangci-lint`, `gosec`, `staticcheck`, plus the catalog-specific gates from §5.4. Deploy on tag/merge to `main` via `flyctl deploy`.

---

## 5. Publish / update workflow

### 5.1 The decision

**[DECISION] PR-based publishing into the service's own git repository, with a `circuitctl` CLI that ingests artifacts and regenerates the manifest deterministically, and CI that refuses any hand-edited manifest. No upload API, no auth tokens, no admin UI — for v1 or, most likely, ever.**

Rationale:

1. **No new auth surface.** An upload endpoint needs credentials, credential rotation, an authz model, and audit logging. Git+GitHub already has all four, operated by someone else, and every engineer already has an account.
2. **Review is the actual security control.** The threat that matters here is a wrong or malicious circuit entering the catalog (§8.3). A human reviewing a PR that adds a 300 KB binary and a metadata block, with `source` provenance filled in, is a far better control than an authenticated `POST` that any leaked token could drive.
3. **Git history is the audit log we would otherwise have to build.** Who added which bytes, when, with what justification, reviewed by whom — for free, immutably, forever.
4. **Rollback is trivial and atomic** — revert the commit, redeploy; or `fly releases rollback`. With `go:embed` (§4.5) the manifest and the bytes roll back together, which no upload-API design gives you without extra work.
5. **Publish frequency is very low.** Realistically a handful of artifacts per year. Designing an automated upload pipeline for that is misallocated effort.

### 5.2 Source of truth layout

Per-entry JSON files under `catalog/circuits/<id>.json`, artifacts under `artifacts/sha256/<hex>`, and **`catalog/manifest.json` as a generated build product**.

Splitting per entry (rather than one big hand-maintained manifest) means PRs show a clean single-file diff, two concurrent additions do not conflict, and there is exactly one place a given circuit's metadata lives. The generated manifest is committed (so the repo is self-contained and `go:embed` is simple) but CI enforces that it matches what `circuitctl` would produce, so it can never drift.

### 5.3 `circuitctl`

A Go CLI in the same repo, sharing `pkg/publish` with the service so validation logic exists once.

```
circuitctl add <file> \
    --system longfellow \
    [--id <id>] [--alias <id>]... \
    [--doc-type org.iso.18013.5.1.mDL]... \
    --origin <url> [--origin-ref <ref>] [--origin-path <path>] [--license <spdx>] \
    [--param key=value]...
```

What `add` does, in order:

1. Read the input file. Reject if larger than a configured max (default 8 MB) — a guard against someone accidentally committing a whole test-vector directory.
2. Compute `sha256` and size of the bytes **as they will be served**.
3. Detect compression. If zstd: validate the frame, decompress, and compute `uncompressed.hash`/`size`. **Refuse if decompression fails** — a corrupt artifact must never be publishable, and this is the cheapest possible place to catch it.
4. For known systems, parse and cross-check system params against the filename convention. For `longfellow`, parse `<version>_<numAttributes>_<blockEncHash>_<blockEncSig>_<circuitHash>` and **fail loudly if an explicitly-passed `--param` contradicts the filename** rather than silently preferring one. (Circuit filenames are the only self-describing metadata these artifacts have; a mismatch means someone renamed a file and that is exactly the mistake worth blocking.)
5. Derive `id` if not given, using the convention in §2.3; reject a collision with any existing `id` or alias.
6. Write `artifacts/sha256/<hex>` and `catalog/circuits/<id>.json`.
7. Regenerate `catalog/manifest.json` deterministically (see §5.5).

Other subcommands:

- `circuitctl verify` — recompute every artifact's hash from disk, re-validate every entry against the schema, confirm every `artifact.url` resolves to a file present in the tree, confirm no orphan artifacts, confirm `manifest.json` is byte-identical to a fresh regeneration. **This is the CI gate.**
- `circuitctl deprecate <id> [--reason …]` / `circuitctl revoke <id> --reason …` — flip `status`, set `deprecatedAt`, regenerate. Separate commands because `revoke` has client-visible fail-closed consequences (§7.4) and should be hard to do by accident.
- `circuitctl publish <id> [--reason …]` / `circuitctl unpublish <id> [--reason …]` — flip `published` (§2.4.1), regenerate. No `--reason` requirement (unlike `revoke`) since neither direction is client-visible either way — an unpublished entry is simply absent, not a live thing being revoked out from under someone.
- `circuitctl ls` — human-readable table.
- `circuitctl fetch <url>` (optional, later) — download from an upstream origin and hand off to `add` with `source.origin` prefilled. Nice-to-have; the manual path is two commands.

### 5.4 CI gates on a publish PR

Non-negotiable:

1. `circuitctl verify` passes (covers hash integrity, schema validity, manifest freshness, orphan detection).
2. `source.origin` and `source.addedBy` are non-empty on every added entry.
3. No modification to an existing entry's `artifact` block — **artifacts are immutable; replacing bytes means a new entry with a new id.** This gate is what makes client-side hash pinning meaningful (§7.3); without it, "the same id" could silently come to mean different bytes.
4. No deletion of an artifact file still referenced by any entry.
5. Diff summary posted to the PR: added/changed entries, artifact sizes, total corpus size, and a warning if corpus size crosses a §4.5 threshold.
6. Normal Go CI (`make all`, lint, gosec, staticcheck).

Recommended: **CODEOWNERS on `artifacts/` and `catalog/`** requiring review from a named group. The bytes are the trust boundary; treat them like release-signing material, not like docs.

### 5.5 Deterministic manifest generation

`manifest.json` **MUST** be byte-reproducible from the per-entry files:

- Entries sorted by `id`, lexicographic byte order.
- Object keys emitted in a fixed schema-defined order (not Go map order).
- 2-space indent, `\n` line endings, no trailing whitespace, UTF-8 no BOM.
- `generatedAt` is the **git commit timestamp of the publishing commit**, not wall-clock at build time. This is the subtle one: wall-clock breaks reproducibility, makes the ETag change on every rebuild, and would cause every client on earth to re-download the manifest after a no-op redeploy.

Reproducibility is what lets CI gate on "the committed manifest equals the generated manifest", which is what prevents hand-editing, which is what keeps the hashes trustworthy.

### 5.6 Withdrawing an artifact

Three escalating levels, and it matters that they are distinct:

1. **Deprecate** (`status: deprecated`) — still served, still usable. Signals "prefer something else". Zero client impact. This is the normal case.
2. **Revoke** (`status: revoked`) — still served (so the bytes remain inspectable and so a client can confirm what it has), but conforming clients **MUST refuse to use it even from cache** (§7.4). This is the tool for "this circuit is wrong/unsafe". It is fail-closed and therefore safe for a compromised manifest to assert (worst case: denial of service, never a bad proof).
3. **Delete the bytes** — last resort only, e.g. a licensing demand. **This will break any shipped client pinned to that artifact that has not yet cached it, permanently.** Requires a deliberate decision and a note in the repo. Prefer revoke.

### 5.7 Circuit validation & provenance-recording tooling — closing the loop the schema implies but doesn't yet build

**[DECISION, 2026-08-13]** §2.8 already defines the provenance *schema* (`source.verifiedBy` etc.) and §5.3/§5.4 already define publish-time *integrity* checks (hash, schema, filename cross-check). What was missing: the actual mechanism that exercises a circuit against a real verifier and turns that into a recorded, git-committed fact — rather than `verifiedBy` staying a hand-typed string nobody re-checks — plus a policy for when to re-run it.

Two distinct kinds of validation, kept distinct on purpose:

1. **Integrity validation** (already specced, §5.3): `circuitctl verify` — purely mechanical, hash/schema/filename checks, no external dependency, runs in every CI job. Answers "is this the bytes/metadata it claims to be."
2. **Interop validation** (new, this addendum): does this circuit actually produce a proof a real verifier accepts. This needs a working prover, which doesn't exist until the parent plan's Phase 3/4 — so the mechanism is *designed* now (schema/CLI surface anticipated in Phase 2) but *built* once a prover exists (Phase 4).

**`circuitctl verify-interop <id> --prover <path> --verifier <endpoint-or-cli>`** (new subcommand, built in Phase 4):

1. Loads catalog entry `<id>`, reads its artifact bytes (re-verifying `artifact.hash` first — never feed unverified bytes to a prover, per §6.3's rule applied server-side too).
2. Drives an actual proof generation with the given prover against a live verifier (`siros-verifier-cli`, `multipaz-verifier-server`, or the `balfanz/multipaz ppid` black-box target).
3. On completion, appends a `VerificationRecord` object (§2.8) to `source.verifiedBy` — `tool`, `toolVersion`, `verifierIdentity`, `date`, `result` — and regenerates the manifest. This is what makes `verifiedBy` a genuine informational element of the manifest itself (parseable by a dashboard or a future client UI), not a note buried in a commit message. Committed via a normal PR like any other publish action, so "this circuit was checked, and here's what happened" becomes git history, reviewable, and cannot be hand-typed into the JSON without going through the tool that actually ran the check.
4. **On failure, it still records the attempt** — a `VerificationRecord` with `result: "rejected"` — rather than staying silent. A circuit that was tried and rejected is meaningfully different from one nobody has tried yet, and the manifest should be able to say which. `circuitctl verify-interop` still exits non-zero on rejection (so CI/scripts treat it as a failure), but the record is written either way — this is what lets `circuitctl ls --stale` (below) and any future dashboard distinguish "never checked" from "checked and failed" from "checked and passed", instead of collapsing the first two into silence.

**Re-validation triggers** (a documented human checklist in the repo README, not an automated scheduler — the catalog is too small to justify one):

- The pinned prover crate (`proverCrate` in the SDK pin table, §7.3) is upgraded.
- The interop verifier target's version changes, or the black-box fork's advertised behavior shifts.
- `circuit_hash`'s preimage is ever established (§9.4) and found to disagree with what's on file.
- Annually, as basic hygiene.

**`circuitctl ls --stale`** (small addition to the existing `ls` subcommand, §5.3): flags any `active` entry whose `verifiedBy` predates the currently-pinned `proverCrate` version, or has no `verifiedBy` entry at all past some age since `publishedAt`. Purely advisory — surfaces staleness, a human judges whether it matters.

This does change the wire format from the original draft (§2.8's `verifiedBy` is now `VerificationRecord[]`, not `string[]`) — worth calling out since it's a schema change made in the same pass that designed the tooling behind it, not an independent decision. Client impact is nil: §2.2's "ignore params/fields you don't recognise" rule already covers a client that predates this change, and no `[ASSUMPTION]`/`[DECISION]` elsewhere in this spec depended on `verifiedBy`'s shape. Home in the phasing: schema change lands whenever this spec's Phase 1/2 implementation happens (cheap — no code depends on the old shape yet); `circuitctl verify-interop` itself is only actually runnable in Phase 4 once a prover exists — track it as an explicit Phase 4 exit-criterion addition, not something that quietly falls through the cracks between phases.

---

## 6. SDK-side client design (`ZkCircuitClient`)

To be implemented in both `siros-sdk-kotlin` and `siros-sdk-swift`, with matching semantics and platform-idiomatic APIs. **[ASSUMPTION]** The description below mirrors what the parent plan says about `Ts11RegistryClient` (SDK-side fetch-and-cache client against `registry.siros.org`, with a `VctmFetcher`/`MddlSchemaFetcher` TTL-cache precedent); I did not read that code in this pass. **First implementation task: read `Ts11RegistryClient` and the VCTM/MDDL fetchers in both SDKs and conform to their existing conventions** (HTTP client injection, error taxonomy, cache directory choice, TTL handling, coroutine/async idiom) wherever they differ from what follows. Consistency with the existing client beats anything specified here on style; the semantics in §6.3, §6.6, and §7 are the parts that must not bend.

### 6.1 Layering

Three components, separated because they have genuinely different failure modes and testability:

```
ZkCircuitClient          ← facade the ZkProofSystem plugins use
 ├── ZkCircuitCatalog    ← manifest: fetch, validate, TTL-cache, ETag revalidate
 └── ZkCircuitStore      ← artifact bytes: content-addressed local store, verify, evict
```

`ZkCircuitClient` is the only type the rest of the SDK sees. Both dependencies are injectable so unit tests can run with a fake catalog and an in-memory store, and so integration tests can point at a locally-running instance of the real Go service.

Deliberately **not** in this layer: which circuit to *prefer*, and whether the SDK's Rust prover can actually use it. Those belong to the plugin and to the compatibility table (§7), respectively. The client's job is "find, fetch, verify, cache" and nothing else.

### 6.2 Surface

```kotlin
// Illustrative — Kotlin shown; Swift mirrors with async/throws and Data.
interface ZkCircuitClient {
    /** Resolve a locally-satisfiable circuit for one of the verifier's requested specs.
     *  Consults the SDK compatibility table (§7) BEFORE the network. Returns null if
     *  nothing this build supports can satisfy the request. */
    suspend fun resolve(requested: List<ZkSystemSpec>): ZkCircuitHandle?

    /** Bytes for a resolved handle: cache hit, else download+verify+store.
     *  Decompression is the caller's/plugin's business — see §6.7. */
    suspend fun load(handle: ZkCircuitHandle): ByteArray

    /** Warm the cache outside a presentation. SHOULD be called at wallet bootstrap. §6.5 */
    suspend fun prefetch(specs: List<ZkSystemSpec>): PrefetchReport

    /** Is this available offline right now? Drives UI ("privacy mode unavailable") without I/O. */
    fun isCached(handle: ZkCircuitHandle): Boolean

    suspend fun refreshCatalog(force: Boolean = false)
    suspend fun prune()
}

data class ZkCircuitHandle(
    val id: String,
    val system: String,
    val systemVersion: String,
    val expectedHash: String,        // "sha256:<hex>" — from the SDK pin table where pinned
    val artifactUrl: String?,        // null for metadata-only entries (§2.9)
    val uncompressedHash: String?,
    val params: Map<String, ZkParam>,
    val status: CircuitStatus,
) {
    /** Hand to the plugin to construct the wire-level ZkSystemSpec. */
    fun toZkSystemSpec(): ZkSystemSpec
}
```

`resolve` returning `null` is a **normal, expected outcome**, not an error — most verifier requests will not be ZK requests at all, and of those, some will ask for circuits this build does not support. It must not throw, log at error level, or otherwise behave like a fault.

### 6.3 Integrity — the load-bearing rule

**A client MUST NOT hand circuit bytes to the prover unless it has computed their SHA-256 and matched it against an expected hash in the current call.**

Concretely:

1. Before download: know the expected hash (from the SDK pin table in strict mode, §7.3; from the manifest entry in manifest-trust mode, §7.6).
2. Reject early on `Content-Length` mismatch, before spending bandwidth.
3. Stream to a **temporary file**, hashing as you go. Cap total bytes read at `size` (a hostile or broken server MUST NOT be able to fill the device's disk).
4. On hash mismatch: delete the temp file, do not retry the same URL more than once, surface `ZkCircuitIntegrityFailure`. **Never** store unverified bytes anywhere the cache could later mistake for valid.
5. On success: `fsync`, then **atomically rename** into `…/sha256/<hex>`. Atomic rename is what guarantees a partially-written file can never be observed as a cache hit — the single most common way file caches like this go wrong.
6. **On every subsequent load from cache, re-hash before use.** Not just on write.

**[DECISION] Verify on every load, not once at write time.** SHA-256 over 300 KB is ~1 ms on any device that can run a ZK prover at all — utterly negligible next to proof generation — and it removes an entire class of bugs and attacks: filesystem corruption, another app or a rooted user tampering with app storage, a restored backup, a half-migrated cache directory. A "verified once, trusted forever" index would be a state machine to maintain and get wrong, for no measurable gain. If profiling ever shows this matters, cache the verification keyed by (path, size, mtime, inode) — but do not start there.

### 6.4 Manifest caching

- Persist the raw manifest bytes plus its `ETag` and fetch time.
- **Soft TTL: 24 h.** Within TTL, serve from cache without network I/O. Past TTL, revalidate with `If-None-Match` (a `304` is a few hundred bytes; cheap enough to do daily).
- **Stale-while-error: unbounded.** If revalidation fails for any reason, keep using the cached manifest indefinitely and continue. There is no scenario where "the catalog is 3 months old" is worse than "ZK presentation is broken because we couldn't reach a server" — cached circuits keep working regardless (§7.5), and a stale manifest cannot make a pinned client unsafe.
- Reject and keep the previous copy if the new document fails validation (unknown `manifestVersion`, wrong `catalog`, unparseable). A bad publish must not brick clients.
- **Never fetch the manifest on the critical path of a presentation if a cached copy exists**, even a stale one. Adding a network round-trip while a user waits at a consent screen is a UX regression, and the pinned-hash design means the manifest adds no safety there anyway.

### 6.5 Prefetch, and why it is a privacy control

`prefetch()` **SHOULD** be called from wallet bootstrap / credential-issuance completion, on unmetered connectivity where the platform exposes that, for every spec in the SDK's compatibility table.

Two reasons, the second being the interesting one:

1. **Availability:** a 300 KB download at the moment a verifier asks for a proof is a visible stall, and fails outright offline.
2. **Privacy:** fetching a circuit *at presentation time* leaks a timing signal to the service operator (and any network observer) that this device is about to make a ZK presentation, and `num_attributes` hints at its shape. For a feature whose entire purpose is privacy, that is a poor look. Prefetching at bootstrap decorrelates the fetch from any presentation. Note this in the client code comment, or someone will later "optimise" it to lazy-fetch. See also §8.6.

`PrefetchReport` should distinguish already-cached / fetched / failed per spec, so the sample app's privacy-mode UI can honestly tell the user whether ZK presentation is currently possible.

### 6.6 Offline / cache-miss behaviour matrix

| Manifest | Artifact | Behaviour |
|---|---|---|
| cached (fresh or stale) | cached | **Full function.** No network. The steady state. |
| cached | missing, online | Download, verify, store, proceed. |
| cached | missing, offline | `resolve` succeeds, `load` throws `ZkCircuitUnavailable(offline)`. Presentation layer reports "ZK presentation unavailable offline"; privacy mode declines the presentation (parent plan §7). |
| absent, offline | cached | **Full function**, if the handle is pinned in the SDK table (§7.3) — the pin supplies id, hash, and params, so the manifest is unnecessary. **This case is a direct payoff of strict pinning and MUST work.** |
| absent, offline | missing | `resolve` returns null → not ZK-capable right now. |
| absent, online | — | Fetch manifest, then proceed as above. |

The fourth row is worth stating explicitly to the implementer: a fresh install that has never reached the service but ships a pinned table and somehow has the artifact (sideloaded, restored, or bundled per §7.7) must still work. Do not let a manifest fetch become an unconditional precondition.

### 6.7 Compression boundary

**[DECISION] `ZkCircuitClient` stores and returns bytes exactly as served (zstd-compressed). It does not decompress.**

Because: (a) `artifact.hash` is defined over the served bytes, so storing them as-is keeps the cache trivially re-verifiable; (b) zstd is not bit-deterministic across encoder versions, so a decompress-then-recompress round trip cannot be relied on to reproduce the same hash; (c) the Rust crate already links zstd and the existing consumers decompress at prover-init time, so decompression naturally belongs to the plugin/FFI layer; (d) it keeps zstd out of both SDKs' dependency lists.

The plugin **MAY** verify `artifact.uncompressed.hash` after decompressing, as defence in depth against a zstd decoder mismatch. Recommended for the first interop bring-up, when you most want to know exactly which layer is wrong; can be relaxed to a debug-build check afterwards.

### 6.8 Android storage

- **Location:** `context.noBackupFilesDir/siros/zk-circuits/sha256/<hex>` (+ `manifest/` alongside).
- **Not `cacheDir`:** Android may purge `cacheDir` at any moment, including between `isCached()` and `load()`. A purge mid-presentation is a confusing, unreproducible failure. These files are large, re-fetchable, and needed at a precise moment — `filesDir` semantics with explicit pruning is the right trade.
- **`noBackupFilesDir` specifically:** circuits are public, re-downloadable, and bulky. Including them in Auto Backup / D2D transfer wastes the user's backup quota and gains nothing. It also keeps them out of any cloud copy, which is a small but free privacy win.
- No encryption, no `EncryptedFile`: this is public data. Encrypting it would imply a confidentiality property that does not exist and cost startup time.
- Concurrency: an in-process `Mutex` keyed by artifact hash for single-flight (§6.10). Cross-process is not a concern for a single-app SDK; do not build a lock file.

### 6.9 iOS/Swift storage

- **Location:** `Library/Application Support/org.siros.sdk/zk-circuits/sha256/<hex>`, via `FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, …)`, creating intermediate directories.
- **Set `isExcludedFromBackup = true`** on the `zk-circuits` directory (`URLResourceValues`). Same reasoning as `noBackupFilesDir`: bulky, re-fetchable, public. Skipping this is also how apps get iCloud-backup-size complaints.
- **Not `Library/Caches/`:** iOS purges it under disk pressure, same mid-presentation failure mode as Android's `cacheDir`.
- Data protection: default (`completeUntilFirstUserAuthentication`) is fine and requires no code. Do **not** use `complete` — the wallet may legitimately need to prefetch or load a circuit while the device is locked (e.g. background refresh), and a protection class that blocks that trades a nonexistent confidentiality benefit for a real availability bug.
- Concurrency: an `actor` owning the store, giving single-flight and serialised writes for free.
- Downloads: `URLSession` with a delegate writing to a temp file. A background-configured `URLSession` is worth considering for `prefetch()` (survives app suspension on a slow link); foreground is fine for on-demand `load()`.

### 6.10 Single-flight, retries, eviction

- **Single-flight:** concurrent `load()`s for the same hash share one download. Two 300 KB downloads racing to the same path is wasted bandwidth and an atomic-rename collision waiting to happen.
- **Retries:** at most 2 retries with jittered exponential backoff (~1 s, ~3 s) on transport errors and 5xx. **No retry** on `404` or on hash mismatch — both mean the client's expectation is wrong, and retrying will not fix it while it will slow down the failure the user is waiting on.
- **Timeouts:** connect 10 s, overall per-artifact 60 s (300 KB on a bad mobile link is genuinely slow), manifest 15 s.
- **Eviction — [DECISION] deliberately minimal:**
  - Never evict automatically during a presentation flow. Ever.
  - `prune()`, called at most daily from a background/maintenance path, removes artifacts that are (a) not referenced by any entry in the current cached manifest **and** (b) not in the SDK compatibility table (§7.3) **and** (c) not accessed in 90 days.
  - Hard cap: 20 artifacts / 50 MB, LRU beyond that, again only from `prune()`. In practice a wallet holds 1–3 circuits, so the cap is a safety net against a pathological loop, not a routine mechanism.
  - `revoked` artifacts (§7.4) are pruned eagerly and unconditionally on the next `prune()`.
  - Rationale for being conservative: the cost of over-retention is ~300 KB of storage; the cost of over-eviction is a broken presentation on a plane. The asymmetry is enormous, so bias hard toward keeping bytes.

### 6.11 Error taxonomy

Typed errors, so the presentation layer and privacy-mode policy can branch without string matching:

| Error | Meaning | Caller's move |
|---|---|---|
| `ZkSpecUnsupported` | No requested spec is in this build's compatibility table. | Cannot do ZK. Privacy mode → decline (parent plan §7). |
| `ZkCircuitUnavailable(reason: offline \| notPublished \| networkError)` | Supported, but bytes not obtainable now. | Retryable later; report honestly to the user. |
| `ZkCircuitIntegrityFailure(expected, actual)` | Bytes did not match the expected hash. | **Fail closed. Do not fall back to non-ZK silently. Log loudly** — this is either a real attack or a publish bug, and both need to be noticed (§8.3). |
| `ZkCircuitRevoked(id)` | Entry is `status: revoked`. | Treat as unsupported. Purge the artifact. |
| `ZkCatalogInvalid` | Manifest unparseable / unknown `manifestVersion` / wrong `catalog`. | Keep previous cached manifest; continue degraded. |

---

## 7. Versioning & compatibility rules

This is the section most likely to be skimmed and most likely to cause a hard-to-debug interop failure. Longfellow circuits have **no forward or backward compatibility whatsoever**; treating them like a normal versioned dependency will produce proofs that no verifier accepts, with no useful error.

### 7.1 The compatibility key

For Longfellow, a circuit is usable by a given wallet build only if **all** of these agree:

1. `system` = `longfellow`
2. `systemVersion` / `params.version` — the prover's `CircuitVersion` enum value, fixed at Rust-crate compile time
3. `params.num_attributes` — must match the attribute count for the presentation being made
4. `params.block_enc_hash`, `params.block_enc_sig` — structural parameters of the circuit
5. **the exact artifact bytes** — the prover is initialised with a specific circuit blob; a different blob with the same declared params is a different circuit
6. **the prover build itself** — the Rust crate is pre-1.0 research code on a patched fork (parent plan §4); a crate upgrade can invalidate a previously-working circuit

Points 5 and 6 are the ones that break intuition. **"I have a v8, 2-attribute circuit" is not sufficient information to conclude "I can prove with it."** The real predicate is "I have the specific artifact this build of my prover was tested against."

### 7.2 Client resolution algorithm

Given the verifier's requested specs, `resolve()` **MUST** proceed in this order:

1. **Filter to known systems.** Drop specs whose `system` this build has no plugin for. (This is what makes adding Vega to the catalog a non-event for shipped clients.)
2. **Consult the SDK compatibility table (§7.3) first — before any network access.** For each requested spec, ask "does this build declare support for it?" If none do → return `null` (`ZkSpecUnsupported`). Ordering matters: a client must never fetch a manifest, discover something interesting, and *then* work out whether it can use it. Capability is a property of the build, not of the catalog.
3. **Locate the artifact.** For a supported spec, take `id` and `expectedHash` from the table. Use the cached manifest (if any) to fill in `artifactUrl` and other metadata; if there is no manifest, fall back to the table's own default URL (which is why the table carries one — §7.3).
4. **Check `status`.** If the manifest says `revoked` → `ZkCircuitRevoked`, even if the table pins it and the bytes are cached. Fail-closed (§7.4).
5. **Return a handle.** Bytes are fetched lazily by `load()`.

### 7.3 The SDK compatibility table (pin table)

**[DECISION] Each SDK build ships a compile-time table of the circuits that build actually supports, including each one's expected artifact hash. The manifest is used to *locate* bytes, never to *authorize* them.**

```kotlin
// Illustrative. Generated or hand-maintained alongside the Rust crate version bump.
internal val LONGFELLOW_SUPPORTED = listOf(
    SupportedCircuit(
        id            = "longfellow-libzk-v1_8_2_4307_2945",
        system        = "longfellow",
        systemVersion = "8",
        numAttributes = 2,
        expectedHash  = "sha256:9f2c1d5e…",
        defaultUrl    = "/v1/artifacts/sha256/9f2c1d5e…",
        proverCrate   = "siros-zk-longfellow 0.1.0",
    ),
)
```

Why this is the right shape:

- **It is the honest encoding of §7.1.** Circuit compatibility is a property of compiled prover code. Putting the supported set *in* that code makes the invariant structural instead of aspirational.
- **It makes a compromised manifest nearly harmless** (§8.3). An attacker who controls the catalog cannot get a strict-mode client to load different bytes for a supported spec, because the client is comparing against a hash baked into the app binary. The worst available outcome is denial of service.
- **It makes offline-with-no-manifest work** (§6.6 row 4), because the pin carries everything `resolve()` needs.
- **It gives one obvious place to update** when the Rust crate is upgraded or a new circuit is qualified. `proverCrate` is there so the pin can be sanity-checked against the linked crate version, and so a reviewer can see at a glance which prover a pin was qualified against.

**Maintenance rule:** an entry is added to the table only after the circuit has been tested end-to-end against a real verifier with that exact prover build, and `source.verifiedBy` on the catalog entry is updated to say so. "It appeared in the manifest" is never sufficient grounds.

### 7.4 `status` semantics

| `status` | Client behaviour |
|---|---|
| `active` | Normal use. |
| `deprecated` | **Still fully usable.** Do not stop using it, do not warn the user, do not auto-migrate. Log at debug for developer visibility. Deprecation is a signal to *maintainers*, not a runtime gate — a shipped build's pin cannot be changed remotely, so treating deprecation as a block would just break working wallets. |
| `revoked` | **MUST NOT be used, including from cache.** Fail closed, purge, report `ZkCircuitRevoked`. |

`revoked` is the only remote kill switch in the design, and it is safe to have precisely because it is fail-closed: a compromised manifest that revokes everything causes an outage, not a bad proof. (A client that had *no* manifest would not see the revocation — accepted limitation, since a client with no manifest has typically never been online, and honouring revocation requires connectivity by definition.)

### 7.5 What auto-updates and what does not — stated plainly

**A client MUST NOT begin using a circuit merely because the catalog advertises it.**

- A new `systemVersion` (v9) appears → **ignored** by existing builds. Using it requires a new prover build, testing, a pin-table entry, and an SDK release. There is no version-preference logic to write, and writing any would be a bug.
- A new `num_attributes` variant appears → **ignored** in strict mode, for the same reason (the prover is initialised for a specific attribute count).
- An artifact's bytes are *replaced* under the same id → **impossible by construction** (§5.4 gate 3). Replacement means a new id.
- An entry is revoked → **honoured** (§7.4), because it is fail-closed.

So the honest accounting of what fetch-and-cache actually bought us over vendoring, which the implementer should understand so they do not oversell it:

1. **Binary size** — the dominant win. No 300 KB–1 MB+ of circuit per SDK consumer, for a feature most wallets will not enable.
2. **Independent artifact qualification** — a circuit can be published, exercised against interop targets, and given `verifiedBy` provenance without cutting an SDK release. The *pin* still needs a release, but the artifact work is decoupled and can proceed in parallel.
3. **A single authoritative place** where "which circuits exist, and where did they come from" is answerable — instead of that knowledge living in three vendored directories and someone's memory.
4. **Cross-consumer reuse** — the same catalog serves both SDKs, the web client, our verifier CLI, and eventually external interop partners.
5. **A revocation path** — nonexistent under vendoring.

It did **not** buy remote capability upgrades, and pretending otherwise would be actively dangerous.

### 7.6 Manifest-trust mode (opt-in, not v1)

A second, opt-in mode exists in the design so that the strict mode's constraint has a documented exit — but it **MUST NOT** be enabled in v1.

In manifest-trust mode, the client accepts any manifest entry whose `system` and `systemVersion` fall inside a **declared supported range** the prover build can genuinely handle (e.g. `longfellow` v8, `num_attributes` 1–8), trusting the manifest's `artifact.hash` rather than a compile-time pin. That would allow adding a new attribute-count variant without an SDK release.

**Preconditions, all required before enabling it:**

1. The manifest is **signed** and the client verifies the signature against a pinned key (§8.7). Without this, manifest-trust mode hands an attacker who controls the catalog the ability to substitute circuit bytes — exactly the attack strict pinning eliminates.
2. The Rust prover is confirmed to *actually* handle the declared range at runtime (i.e. `num_attributes` is a runtime parameter, not baked into the crate build) — **currently unverified**, and quite possibly false.
3. The prover has been hardened against, or at least fuzzed with, adversarial circuit inputs (§8.3).

This is the concrete answer to "when does manifest signing become necessary": not for v1, but as the enabling precondition for this mode, and for any external consumer that lacks our pin table.

### 7.7 Optional bundled fallback

An SDK **MAY** bundle one circuit as a build-time fallback for the "fresh install, offline, needs ZK immediately" case.

**Recommendation: do not, for v1.** It reintroduces the binary-size cost that motivated the service, for a narrow scenario that `prefetch()` at bootstrap (§6.5) already covers in practice. Revisit only if real usage shows first-presentation failures. If it is ever added, the bundled copy **MUST** still be hash-verified against the pin table on load — no exemption for "it came from our own binary", because a bundled asset is still a file on disk in a resource bundle that can be repacked.

---

## 8. Security considerations

### 8.1 Threat model in one line

A public, unauthenticated, read-only distributor of **public, non-secret data**, whose correctness is enforced by the *client's* hash check rather than by the server. The security design follows from taking that seriously.

### 8.2 What is not at risk

Worth stating explicitly, because "ZKP service" invites over-estimating the stakes:

- **No credential secrets, no private keys, no PII, no user identifiers** ever touch this service. It holds circuit blobs, which are public artifacts derived from a public protocol specification.
- **No authentication material** exists to steal — no accounts, no API keys, no tokens, no database.
- **Compromising the service does not compromise any wallet's credentials or keys.** There is no path from serving a bad file to extracting key material from a device.

### 8.3 What *is* at risk: a bad circuit, and the blast radius

If an attacker (or a bad publish) gets a wallet to load circuit bytes other than the intended ones, the plausible consequences, worst-first:

1. **Memory-safety / parser exposure in the prover.** The circuit blob is untrusted input to zstd decompression and then to circuit deserialisation inside a pre-1.0 Rust research crate that was never designed to be hardened against adversarial circuit files. Rust's safety helps a great deal, but `unsafe` exists in the NEON field-arithmetic backends, and a malformed circuit could plausibly cause a panic (DoS), unbounded allocation (OOM), or in the worst case worse. **This, not cryptography, is the sharpest edge.** It is also an argument for treating the pin table as a security control rather than a convenience.
2. **Soundness or zero-knowledge break.** A subtly-crafted circuit could in principle produce proofs that leak more about the credential than intended — i.e. attribute values reaching a verifier that should not have learned them — or that assert statements the wallet did not intend to assert. For a *privacy* feature, this is the consequence that matters most, and it is the reason "it's just a public file" is not a sufficient security posture.
3. **Denial of service / interop breakage.** The overwhelmingly likely real-world outcome: proofs that verifiers reject, or a prover that refuses to initialise. Annoying, visible, not dangerous.

**Why strict hash pinning (§7.3) is the primary control:** a v1 client compares downloaded bytes against a hash compiled into the app binary. An attacker who fully controls the service — DNS, TLS, storage, the manifest — **cannot reach outcomes 1 or 2 at all** against such a client. They can only withhold or corrupt bytes, which the client detects and rejects. That reduces total compromise of this service to a denial-of-service against ZK presentation, which is a genuinely acceptable v1 posture and is the reason manifest signing can wait.

The corollary the implementer must not miss: **`ZkCircuitIntegrityFailure` is a security event.** It must fail closed, must never silently degrade to non-ZK presentation, and must be logged prominently enough that a developer notices. A swallowed integrity failure converts a working control into a decorative one.

### 8.4 Transport

- HTTPS only. HSTS on the service. Clients **MUST** reject non-`https` absolute `artifact.url` values (§2.6).
- Standard platform TLS validation. **[DECISION] No certificate pinning.** It buys little given content hashes already provide end-to-end integrity, and it creates a real availability hazard (a rotated cert bricking shipped wallets) for a service whose compromise is already contained to DoS.
- Send **no** cookies, no `Authorization`, no custom identifying headers. A `User-Agent` of `siros-sdk-{kotlin,swift}/<version>` is fine and useful for debugging; anything device-specific is not (§8.6).

### 8.5 Availability, abuse, and rate limiting

- **Public read, no auth** — matching the TS11 registry's public-read model. **[ASSUMPTION]** per the parent plan's characterisation of that service. Adding auth would require distributing credentials to every wallet (which makes them public anyway) and would obstruct the eventual goal of being useful to external interop partners.
- **Do rate limit anyway.** 300 KB responses make this a bandwidth-amplification target: a trivial script can turn a small request stream into meaningful egress on our bill. `go-trust` already has per-IP token-bucket middleware (`pkg/api/ratelimit.go`) to reuse. Defaults from §4.6 (20 rps, burst 60) are far above any legitimate client's needs — a real wallet makes one manifest request per day and one artifact request per circuit ever.
- Rate limiting **MUST NOT** apply to `/healthz`/`/readyz`.
- `429` responses **MUST** include `Retry-After`; clients **MUST** honour it and **MUST NOT** treat `429` as an integrity failure.
- Set a sane request body limit (this API takes no bodies) and read/write timeouts.

### 8.6 Privacy of the fetch itself

An underappreciated point for a privacy feature: **who fetches which circuit, when, is itself a signal.** A request for a `num_attributes=2` circuit at 14:32 says "this IP is about to make a 2-attribute ZK presentation". Mitigations, in order of value:

1. **Prefetch at bootstrap, not at presentation time** (§6.5) — decorrelates the network event from the privacy-sensitive event. This is the important one.
2. **Do not log client IPs** beyond what is needed for rate limiting, and do not retain them. Metrics should be aggregate counters only; no per-client dimensions, no artifact-hash × IP joins.
3. Immutable, long-`max-age` artifact responses mean a device fetches each circuit **once, ever**, so there is no recurring signal.
4. Note in the repo README that the operator can see aggregate ZK adoption but should not be able to reconstruct per-user presentation timing. If that guarantee ever needs to be stronger, a CDN in front (§4.4) or a third-party mirror weakens the operator's view further.

### 8.7 Manifest signing

**[DECISION] Not implemented in v1. Designed here, with explicit triggers, because the risk is real and naming it beats discovering it later.**

The risk, stated plainly: an attacker who can modify the manifest can change an entry's advertised `artifact.hash` and point `artifact.url` at bytes matching that new hash. For a **manifest-trusting** client, that is a complete circuit-substitution attack (§8.3 outcomes 1 and 2). For a **strict-pinned** client (our v1), it is nothing but DoS. So signing is not "belt and braces" — it is precisely the control that makes non-pinned consumption safe.

Signing is **required** before any of:

- enabling manifest-trust mode (§7.6),
- any external/third-party consumer relying on the catalog (they will not have our pin table, so the manifest *is* their root of trust),
- the catalog being treated as production infrastructure rather than internal/interop tooling.

Design when it happens:

- **JWS** over the canonical manifest bytes (§5.5's determinism is a prerequisite — you cannot sign a non-reproducible document). Prefer a **detached** signature at `/v1/manifest.json.jws` so the JSON stays directly consumable by `curl | jq`, or JSON-serialization JWS with the signature alongside.
- Signing key held **offline or in a CI secret**, distinct from any TLS material. Signing happens at publish (CI) time, not at request time — the service never needs the key, which means compromising the running service still cannot forge a manifest. That property is worth preserving deliberately.
- Key discovery via `/.well-known/jwks.json` **plus** a key pin in the SDK (a JWKS fetched from the same host an attacker controls is not a root of trust on its own; the SDK pin is what makes it one).
- Include `generatedAt` in the signed payload and have clients reject a manifest older than a previously-seen one (rollback protection). Without this, an attacker can replay an old signed manifest to un-revoke a revoked circuit — the one place signing alone is insufficient.
- **Also worth doing at the same time:** sign the artifacts themselves (e.g. `cosign sign-blob`, or just cover them transitively via the signed manifest's hashes — the latter is simpler and sufficient given content addressing).

### 8.8 Supply chain

- `source` provenance (§2.8) on every entry, enforced by CI (§5.4). "Where did these bytes come from" must never be a mystery.
- CODEOWNERS review on `artifacts/` — the bytes are the trust boundary.
- Artifacts are immutable once published (§5.4 gate 3), so a published hash means one thing forever.
- Redistribution licensing for the initial circuit set is already resolved (§9.6) - all sources are open-source-licensed permitting redistribution. The broader "licensing model for the service itself" question (§9.6) remains open but does not block Phase 1.

---

## 9. Open questions / deferred decisions

These need a human decision. None blocks starting Phase 1; each is flagged where it lands.

**9.1 Hostname. [RESOLVED, 2026-08-13]** `api.circuits.siros.org` for the API service (Fly-hosted `go-zk-circuits`), `circuits.siros.org` for the human-facing website (GitHub Pages, separate repo) — see §1.6. Each is its own subdomain, so either can move hosting strategy later by DNS alone (§4.4), independent of the other. **Still needed before an actual `fly deploy`/DNS cutover:** the CNAME record for `api.circuits.siros.org` pointing at the Fly app, and TLS cert provisioning (Fly handles this automatically once the custom domain is added via `fly certs add`).

**9.2 Repo name and location. [RESOLVED for the API service, 2026-08-13]** `github.com/sirosfoundation/go-zk-circuits`, matching `go-trust`/`go-wallet-backend`/`go-tokenauth` — built at `~/work/siros.org/go-zk-circuits`. Artifacts live in the same repo as the service (§4.5/§4.6), which is what makes `go:embed` and atomic rollback work. **New, per §1.6:** the website is a second repo, `github.com/sirosfoundation/circuits.siros.org`, following the `registry.siros.org`/`apt.siros.org` repo-per-subdomain convention. **Still open:** whether `circuits.siros.org` (the repo) actually gets created/pushed under that name — deliberately not done without explicit go-ahead, since creating org infrastructure is a real, hard-to-reverse action.

**9.3 `id` convention, and what Balfanz's verifier actually advertises.** §2.3 recommends matching the verifier's advertised spec id, but the parent plan's Open Question #3 establishes that target is a **black-box private multipaz fork**. The convention (`longfellow-libzk-v1_8_2_…`? multipaz's `LongfellowZkSystem` naming? something else?) must be confirmed from a live request/response run against the deployed instance, or from Dirk directly. `aliases` (§2.3) is the hedge, so this does not block publishing — but the first interop attempt will settle it. **Needed before: Phase 3 interop; not before Phase 1.**

**9.4 What is `circuit_hash` a hash of?** Compressed bytes? Decompressed? A structural hash of the circuit representation rather than of any file? §2.6 deliberately keeps it as opaque system metadata, distinct from `artifact.hash`, so this cannot cause a correctness bug — but knowing the answer would let `circuitctl` cross-check it at publish time, which is a genuinely valuable integrity gate. *Answerable by reading `zk-cred-longfellow` / PR #222.* **Nice to have for Phase 1.**

**9.5 Does Vega need artifacts at all?** Unresearched (parent plan §2.4 / Phase 0). Its "lookup-centric arithmetization" may mean no circuit-file equivalent, though ~464 KB proving keys are reported. §2.9 (metadata-only entries) and the open params bag are the hedges; no schema change should be needed either way, but this should be confirmed when Vega gets its research pass. **Needed before: Vega work, not before.**

**9.6 Redistribution licensing.** **RESOLVED for the near term (2026-08-13):** not a gate for Phase 1/testing — all circuits planned for initial use come from repos under open-source licenses that permit redistribution (`zk-cred-longfellow` is MPL-2.0; its bundled circuit artifacts inherit the same repo license, no separate/incompatible license was found on the artifact files themselves). The longer-term plan is to offer this service itself under an explicit licensing model (details TBD by whoever picks this up) rather than relying case-by-case on each upstream circuit source's license — that broader question stays open, but does **not** block Phase 1 testing/interop work, only a later "public, general-purpose, any circuit" posture. Revisit before onboarding a circuit source whose license terms haven't been checked.

**9.7 Git LFS / repo growth.** In-tree artifacts grow git history permanently. §4.5 suggests LFS past ~200 MB total. Whether to start with LFS (cleaner later, more friction now, and LFS interacts awkwardly with `go:embed` in CI if not fetched) is deferred. *Recommendation: plain git for v1; revisit at the threshold.* **Needed before: nothing immediately.**

**9.8 Should go-wallet-backend proxy this, like `/type-metadata` proxies the TS11 registry?** **[ASSUMPTION]** that such a proxy exists and why. Arguments for a proxy: a single network egress for the wallet, backend-side caching, and it hides circuit-fetch timing from us the operator (§8.6). Against: 300 KB blobs through the backend for no logic gain, extra hop, extra failure mode, extra bandwidth on the backend's bill, and it breaks the "immutable content-addressed URL, cache forever" property unless carefully preserved. *Recommendation: no proxy for v1 — the SDK client talks to the service directly.* **Revisit if: privacy analysis (§8.6) demands it, or the backend already has a caching layer worth reusing.**

**9.9 Should the web client (`wallet-frontend`) migrate to this service?** It currently embeds circuit bytes in its build and would benefit identically. Flagged as **out of this session's scope** (the standing scope note is native SDKs + go-wallet-backend + siros-wscd-manager; wallet-frontend is out), so this needs a separate conversation with whoever owns that codebase. Worth noting that a second independent consumer is the best possible validation of the API design. **Needed before: nothing; opportunistic.**

**9.10 Uncompressed artifact variants?** Should the service also offer decompressed circuits (a `?compression=none` variant or a parallel path) for consumers without zstd? *Recommendation: no — every known consumer already links zstd, and it would double storage while creating two hashes per artifact to keep straight.* Listed only so the decision is on the record. **Deferred indefinitely.**

**9.11 Who owns/operates it.** Fly.io org/app naming, who is on the hook when it is down, whether it joins whatever alerting the other Go services use. Small but real. **Needed before: Phase 2 completes.**

**9.12 Should the verifier side use this too?** Our own `siros-verifier-cli` and any org-run verifier need the *same* circuit bytes as the prover. Reusing this catalog for them is obviously right and needs no API change — but a Go client library (`pkg/client` in the service repo, reusing `pkg/catalog`'s types) would need writing. *Recommendation: yes, and it is cheap — build it in Phase 2 while the types are fresh.* **Needed before: Phase 4 interop testing, ideally earlier.**

---

## 10. Phased implementation plan

Each phase has an exit criterion that is checkable by someone other than its author. Phases 1 and 2 are independent of the parent plan's Rust work and can proceed in parallel with it; Phase 4 is the join point.

### Phase 1 — The real Go service, minimal but production-shaped (target: ~3–5 days)

**[STATUS, 2026-08-13] Built.** Repo scaffolded at `~/work/siros.org/go-zk-circuits` (module `github.com/sirosfoundation/go-zk-circuits`, added to the workspace `go.work`). `pkg/catalog`, `pkg/api`, `pkg/publish`, `cmd/zkc`, `cmd/circuitctl` all implemented and tested (58 unit tests + 2 real-process integration tests, all green; `golangci-lint`/`gosec`/`staticcheck` all clean). The real v8 circuit is published as `longfellow-libzk-v1_8_2_4307_2945` — sourced from the local `multipaz-ppid` copy per the Appendix C decision below, **provenance still unresolved, recorded verbatim in the entry's `notes` field**, not yet safe to treat as cleared for redistribution. Docker image builds and runs standalone (distroless, no runtime deps) and was verified end-to-end: manifest fetch, single-entry lookup + alias redirect, artifact download with hash match, range requests, 404/400/405 error shapes, rate limiting, `/metrics`. **Not yet done:** actual `fly deploy` (blocked on §9.1 hostname / §9.2 repo-name decisions, and on this being a real git repo pushed somewhere — currently local-only, not yet `git init`'d).

**[DECISION, 2026-08-13]** Build this as a real service from day one - no throwaway static-hosting stopgap (the earlier draft of this plan considered GitHub Pages/a bucket as a quick Phase 1 proof-of-concept; explicitly rejected, see §4.2.1). The "prove the schema" goal below is achieved by the real service's own first deploy, not by a separate disposable step.

1. §9.6 (licensing) already resolved for the initial circuit set — not a gate for this phase.
2. Pick hostname (§9.1) and repo (§9.2). Scaffold `go-zk-circuits` from `go-trust`'s conventions: gin, `swaggo/swag`, prometheus `/metrics`, `/healthz` + `/readyz` + `/info`, rate-limit middleware, `golangci-lint`/`gosec`/`staticcheck`, multi-stage Dockerfile with `CGO_ENABLED=0`, Makefile targets, §4.6 layout.
3. Obtain the v8 circuit (`8_2_4307_2945_bb8e6a26…`) from upstream PR #222 / the wallet-frontend branch. Record `source` provenance.
4. `pkg/catalog`: schema types (§2), strict validation, `go:embed` loading, deterministic manifest generation (§5.5).
5. `pkg/api`: `/v1/manifest.json`, `/v1/circuits/{id}.json`, content-addressed `/v1/artifacts/{alg}/{hex}` via `http.ServeContent` (ETag/Range/304 for free), RFC 9457 errors, `405`/`400` handling. Alias redirects and the optional `/v1/circuits?…` filter (§3.5) can wait for Phase 2 if time-pressed - they're not on the critical path to a fetchable v8 circuit.
6. `cmd/circuitctl`: `add`, `verify`, `ls` at minimum (§5.3) - this is how the v8 entry gets into the manifest; there is no hand-written-JSON intermediate step to later migrate away from.
7. CI gates from §5.4, including `circuitctl verify` and the immutability check.
8. Deploy to Fly.io; point the hostname at it.

**Exit:** `make all` green; `circuitctl verify` green in CI; the deployed service passes every curl in §3 for the v8 circuit, including `304` revalidation and a `Range` request; `/metrics` scrapes. The v8 circuit is retrievable from a real, permanent URL by someone who was not in the room - this unblocks Phase 3 of the *parent* plan (Rust + SDK) with a fetchable URL that will not need to move later.

### Phase 2 — Full tooling and remaining API surface (target: ~2–3 days)

**[STATUS, 2026-08-13] Mostly built alongside Phase 1**, since the two were done together in one session: `circuitctl deprecate`/`revoke` done and tested; alias redirects, RFC 9457 errors, and the optional `/v1/circuits?…` filter done and tested. `circuitctl verify-interop`'s schema (`VerificationRecord`) and CLI surface (`circuitctl ls --stale`) are in place per §5.7, but the command itself can't run for real yet — no prover exists (that's Phase 4). **Not done:** `pkg/client` (Go client for verifier-side reuse, §9.12); onboarding non-v8 circuits; settling §9.3's id-convention question (needs a live request/response run against the real interop target).

1. `cmd/circuitctl`: `deprecate`, `revoke` (§5.3), if not already done in Phase 1.
2. Alias redirects, RFC 9457 error detail, optional `/v1/circuits?…` filter (§3.5), if deferred from Phase 1.
3. Ship `pkg/client` (Go client) for verifier-side reuse (§9.12) while the types are fresh.
4. Onboard any additional non-v8 circuits needed for interop testing (§9.3's id-convention question gets settled here, from a live request/response run).

**Exit:** the full API surface from §3 is implemented and covered by `circuitctl verify`; a deliberately hand-corrupted `manifest.json` fails CI.

### Phase 3 — SDK clients, both platforms (target: ~1 week per platform, parallelisable)

1. **Read `Ts11RegistryClient` and the VCTM/MDDL fetchers in both SDKs first** and conform to their conventions (§6 preamble). Resolve every **[ASSUMPTION]** in §6 against real code before writing anything.
2. Kotlin: `ZkCircuitClient` / `ZkCircuitCatalog` / `ZkCircuitStore`, `noBackupFilesDir` storage (§6.8), mutex single-flight, typed errors (§6.11).
3. Swift: the same semantics, `actor`-based store, Application Support + `isExcludedFromBackup` (§6.9), `URLSession` downloads.
4. The compatibility pin table (§7.3) on both platforms, with the Phase 1 v8 circuit as its first and only entry.
5. Resolution algorithm (§7.2), status handling (§7.4), offline matrix (§6.6).
6. Tests: unit tests against a fake catalog; **integration tests against a locally-running real service** (`make test-integration` exists as a pattern) covering hash-mismatch rejection, atomic-write-then-rename, single-flight, 304 revalidation, stale-while-error, revoked-refusal, and every row of §6.6. The hash-mismatch and revoked cases are the security-relevant ones — assert them explicitly.

**Exit:** on both platforms, a cold client resolves and fetches the v8 circuit, verifies it, serves it from cache on the next call with no network, keeps working with the service unreachable, and **refuses** bytes whose hash does not match the pin. Test suites green in CI.

### Phase 4 — Join with the prover (depends on parent plan Phase 2)

1. Wire `ZkCircuitClient.load()` into `LongfellowZkProofSystem`'s prover-init path (the parent plan's Phase 3).
2. Confirm the fetched artifact actually initialises the Rust prover — the first real end-to-end validation that the *bytes we publish* are the *bytes the prover wants*. Expect surprises here; this is where a `circuit_hash` misunderstanding (§9.4) or a compression-boundary error (§6.7) would surface.
3. Optionally verify `uncompressed.hash` post-decompression during bring-up (§6.7).
4. `prefetch()` from wallet bootstrap (§6.5).
5. Update `source.verifiedBy` on the catalog entry once a proof verifies against a real verifier.

**Exit:** a ZK proof generated from a circuit that was **fetched and cached**, never vendored, verified by a real verifier.

### Phase 5 — Hardening, only if warranted

Pull these forward individually if their triggers fire; none is needed to be useful.

- Manifest signing (§8.7) — **gated on §7.6 or an external consumer.**
- Manifest-trust mode (§7.6) — gated on signing plus confirming the prover handles a runtime attribute-count range.
- Pagination (§3.7) — gated on 512 KB / 500 entries.
- CDN in front of `/v1/artifacts/*` or migration to Option A (§4.4) — gated on the corpus/egress/SLO triggers.
- Vega entries (§9.5) — gated on Vega research.
- wallet-frontend migration (§9.9) — gated on a scope conversation.

---

## Appendix A — Summary of decisions

| # | Decision | Where | One-line rationale |
|---|---|---|---|
| 1 | Generic `params` bag, not a discriminated union | §2.1 | Matches shipped `ZkSystemSpec` end-to-end; absorbs unknown Vega shape without a schema break. |
| 2 | Scalar-only params | §2.2 | Lossless across Kotlin/Swift/multipaz without a JSON tree walker. |
| 3 | `id` matches the verifier's advertised spec id; `aliases` as the hedge | §2.3 | Avoids a translation layer against a black-box interop target. |
| 4 | Three hashes kept explicitly distinct | §2.6 | `params.circuit_hash` ≠ transport hash; conflating them is a silent interop bug. |
| 5 | Every endpoint static-host-implementable | §3.1 | Keeps the hosting decision reversible at zero client cost. |
| 6 | Content-addressed artifacts, `immutable` caching | §3.6 | The address *is* the integrity guarantee; makes caching free and correct. |
| 7 | No pagination; `next` reserved | §3.7 | Catalog is tiny; protocol stays paginated, deployment doesn't. |
| 8 | **Go service on Fly.io with `go:embed`** | §4.4 | Zero new infra concepts for this org; atomic git-derived publish+rollback; reversible by DNS. |
| 9 | PR-based publishing via `circuitctl`, no upload API | §5.1 | Review is the real control; git is the audit log; no new auth surface. |
| 10 | Deterministic manifest generation, CI-enforced | §5.5 | Prevents hand-editing, enables signing later, stops ETag churn. |
| 11 | Client stores compressed bytes, does not decompress | §6.7 | Keeps the cache re-verifiable; zstd isn't bit-deterministic. |
| 12 | Verify hash on **every** load, not once | §6.3 | ~1 ms; removes a whole class of cache/tamper bugs. |
| 13 | `noBackupFilesDir` / Application Support, never Caches | §6.8/§6.9 | OS cache purges cause unreproducible mid-presentation failures. |
| 14 | Conservative eviction, never during presentation | §6.10 | Over-retention costs 300 KB; over-eviction breaks a presentation offline. |
| 15 | **Compile-time pin table; manifest locates, never authorizes** | §7.3 | Encodes the real invariant; reduces total service compromise to DoS. |
| 16 | No auto-upgrade to newer circuits, ever | §7.5 | Circuit compatibility is a property of the prover build, not of file presence. |
| 17 | `revoked` is the only remote kill switch | §7.4 | Fail-closed, so it is safe even if the manifest is compromised. |
| 18 | Prefetch at bootstrap, not at presentation | §6.5 | Availability *and* decorrelating a privacy-sensitive timing signal. |
| 19 | Public read, no auth, but do rate limit | §8.5 | Matches TS11's model; 300 KB blobs are a bandwidth-amplification target. |
| 20 | Manifest signing designed, deferred | §8.7 | Pinning makes it unnecessary for v1; it is the precondition for §7.6 and external consumers. |
| 21 | No cert pinning | §8.4 | Content hashes already give end-to-end integrity; pinning adds a bricking risk. |
| 22 | No bundled fallback circuit in v1 | §7.7 | Reintroduces the binary-size cost the service exists to remove. |
| 23 | Interop validation is a distinct mechanism (`circuitctl verify-interop`) from integrity validation (`circuitctl verify`) | §5.7 | "Are these the right bytes" and "does this actually produce an accepted proof" are different questions with different dependencies (the latter needs a live prover). |
| 24 | `source.verifiedBy` is structured (`VerificationRecord[]`), not free-text strings | §2.8/§5.7 | Makes interop-verification status a real, parseable informational element of the manifest, not a note only a human can read — while carrying zero trust authority (that's still the pin table's job, §7.3). |
| 25 | Per-entry `published` build-time gate, distinct from `status` | §2.4.1 | Lets an entry with unresolved provenance/licensing sit in the repo — hash-verified, tooled, ready — without blocking every other entry's deploy pipeline on it. |
| 26 | `source.toolchain` (free text) and `source.openSource` (explicit boolean, default false) added to provenance | §2.8.1 | A human reviewer on the website needs "what built this" and "can I use it" answered without leaving the page or knowing SPDX/OSI mappings; neither is inferable from `origin`/`license` alone. |
| 27 | Public website (`circuits.siros.org`) split from the API (`api.circuits.siros.org`) — separate repo, separate hosting | §1.6 | Human-facing design/SEO concerns don't belong on the minimal static-host-compatible API; the site is just another API consumer, using CORS like anyone else. |

## Appendix C — Implementation research addendum (2026-08-13)

Findings from a planning pass scoped to Phase 1 + Phase 2, done in a separate session from the one that will implement. Recorded here so the implementation session does not have to re-derive any of this.

**go-trust conventions — verified against real source, not assumed:**

- Layout, gin wiring (`gin.New()` + explicit `Logger`/`Recovery`, `RegisterAPIRoutes` pattern), swaggo annotation style, `/healthz`/`/readyz` (current) vs `/status`/`/info` (deprecated, `Deprecation`/`Link`/`X-API-Warn` headers), Makefile targets (`fmt vet test lint build swagger` + `test-integration`, confirmed real), and module path convention (`github.com/sirosfoundation/<repo>`) all confirmed exactly as this spec assumed — safe to copy directly. `pkg/testserver/` (an exported `httptest`-based fake server with functional options) is a real, reusable pattern worth copying for anyone who later wants to write a fake `go-zk-circuits` client in another repo's tests.
- `pkg/api/ratelimit.go` is real, but it's a thin wrapper around `golang.org/x/time/rate` (per-IP token bucket via a `map[string]*rate.Limiter` with a cleanup loop), not a hand-rolled algorithm — copy that shape, not something more novel.
- **Gaps — go-trust has no precedent for these, they must be designed fresh, not copied:** (a) no `go:embed` usage anywhere in go-trust; (b) no content-addressed/`ETag`/`Range` file-serving handler (no `http.ServeContent` usage) — plan to use stdlib `http.ServeContent` directly for `/v1/artifacts/{alg}/{hex}`, wrapped via `gin.WrapH` or called from inside a gin handler against `c.Writer`/`c.Request`; (c) no RFC 9457 `problem+json` anywhere — go-trust's errors are ad hoc `gin.H{"error": ...}`, so §3.8's structured errors need a small new shared helper, not a copy-paste.
- **Correction to this spec's own claim:** go-trust's Dockerfile comment says CGO is enabled "for PKCS#11 support via crypto11", but grepping go-trust's actual `.go` files found no direct PKCS#11/crypto11 import — it's a transitive dependency of `go-cryptoutil`, not something go-trust's own code calls. Likely a stale comment. **Do not copy `CGO_ENABLED=1` on that basis** — go-zk-circuits has no PKCS#11 need of its own either way, so §4.6's `CGO_ENABLED=0` + scratch/distroless plan stands, just don't cite go-trust's comment as the reason.
- **CI gap to close explicitly:** go-trust's `make lint` target chains golangci-lint + gosec + staticcheck, but its actual CI workflow (`go.yml`'s `lint` job) only runs golangci-lint — gosec/staticcheck exist in the Makefile but are not enforced in CI today. §5.4 gate 6 ("normal Go CI... lint, gosec, staticcheck") needs those wired into go-zk-circuits' own workflow explicitly; don't assume copying go-trust's CI file gets you gosec/staticcheck enforcement, because go-trust itself doesn't have that today.

**Fly.io deployment convention — corrects an implicit assumption:** `go-trust`, `go-wallet-backend`, `go-tokenauth`, and `go-grc` each ship their **own standalone `fly.toml`** and deploy via their own `flyctl deploy`; none of them are orchestrated through `sirosid-dev` (that repo's `fly-up`/`fly-down` orchestration is specifically for spinning up full sirosid demo/test-environment stacks, not for every standalone Go microservice). §4.6's "single app, 1-2 machines, 256MB, `min_machines_running=1`" plan is therefore correctly scoped as its own standalone `fly.toml` + its own CI deploy job — no dependency on `sirosid-dev` tooling needed or expected.

**§9.1/9.2 partially answered:** no existing `fly.toml` convention forces a particular hostname/org structure beyond `FLY_ORG = "sirosfoundation"` (confirmed the org name at least). Hostname and exact repo name are still open per §9.1/§9.2, but "does this need to plug into sirosid-dev's orchestration" is now answered: no.

**v8 circuit sourcing — [DECISION, 2026-08-13]:** The exact artifact (`8_2_4307_2945_bb8e6a26d2700ddad968562d1c4aee83067772fee6f889748a0bc64f2c694ad5`, 326,208 bytes, valid zstd) already exists locally in `~/work/siros.org/multipaz-ppid` (four duplicate copies under that tree, plus sibling v8 variants for other attribute counts and older v6/v7 circuits). **Decided: use these bytes for Phase 1** rather than waiting on the still-open, unmerged `abetterinternet/zk-cred-longfellow` PR #222.

**Unresolved provenance flag this creates — MUST be checked before `circuitctl add` is run for real, not just for local dev:** `multipaz-ppid` is a flat, non-git, unversioned local copy of the **private `balfanz/multipaz` fork branch `ppid`** — our black-box interop verifier target, source otherwise inaccessible (parent plan Open Question #3). It has an Apache-2.0 `LICENSE` file at its root, but with no commit history in the local copy there is no way to confirm that license file actually governs this specific artifact, or how this copy was obtained. This is a **different provenance chain than §9.6's licensing resolution**, which was based on `zk-cred-longfellow` being MPL-2.0 (a public, separate repo). Concretely, before publishing this artifact for real:

- Confirm with whoever has visibility into the private fork (Dirk Balfanz, per the parent plan) whether these specific bytes are redistributable, and under what terms.
- If confirmed, `source.origin`/`source.license` on the catalog entry should say so honestly (e.g. `origin: "local copy from balfanz/multipaz ppid branch, confirmed redistributable by <person> on <date>"`), not silently borrow the zk-cred-longfellow MPL-2.0 characterization — that would misattribute provenance.
- If not confirmed by the time Phase 1 needs to ship, fall back to waiting on PR #222 (slower, but already license-resolved) rather than publishing unresolved-provenance bytes.
- This is Phase 1 step 3's real blocker, not a Phase 5 nice-to-have — flagging it here so it isn't missed.

---

## Appendix B — Reader's guide by role

- **Implementing the service (Go):** §2, §3, §4.5–4.6, §5, then §9.1/9.2/9.6 for the decisions you need from a human first.
- **Implementing the SDK client:** §6 and §7 in full (§6.3, §7.2, §7.3, §7.5 are the ones that must not be approximated), plus §2.6's hash distinction and §3.8's "status code is authoritative, body is advisory".
- **Publishing a circuit:** §5, plus §2.4–2.8 for what metadata you must supply.
- **Reviewing this design:** Appendix A, then §4.4 and §7.5 — the hosting recommendation and the honest accounting of what fetch-and-cache actually buys are the two places most worth pushing back on.
