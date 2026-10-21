# Circuit capabilities

> `circuit-distribution-service-spec.md`, which the code cites as "the spec",
> is not in this repository. This document is where the `capabilities`
> extension is specified; when the spec file is added to the repo, this
> becomes a subsection of its §2.4 entry schema.

The catalog's `system`, `id` and `docTypes` say which proof system and which
circuit an entry is. They cannot say which functions, parameters and
credential the circuit supports, because that lives in the free-form,
scalar-only `params`. `capabilities` is an optional, general, machine-readable
declaration of that. Consumers (wallets) interpret it themselves; this
repository only ensures that what is in the manifest is well-formed.

## Schema (additive, `manifestVersion` stays 1)

`capabilities` is an optional field of a catalog entry. A client that does not
know it ignores it. An entry without it is valid and declares nothing. Entries and the generated
manifest that do not carry it are byte-for-byte unchanged by its introduction.
An explicit `"capabilities": null` is not the same as omitting the member: it
is rejected (by entry loading, so `circuitctl verify` fails, and by the
`--capabilities` file parser), so a placeholder can never be mistaken for a
validated declaration.

```json
"capabilities": {
  "schema": 1,
  "presentation": "zkp_req",
  "credential": { "format": "mso_mdoc", "type": "eu.europa.ec.eudi.nym_auth.1", "issuance_profile": "nym_auth-spec-2.1" },
  "functions": [
    { "id": "pseudonym", "max": 1, "always": true,
      "params": { "config": { "enum": ["known-offender-matching-ssc"] },
                  "ver": { "type": "integer" },
                  "prf": { "enum": ["sha-256"] } } },
    { "id": "age_check", "max": 1,
      "params": { "ge": { "type": "full-date" }, "le": { "type": "full-date" } } }
  ]
}
```

| Field | Rule |
|---|---|
| `schema` | Required, must be `1`. Independent of `manifestVersion`. |
| `presentation` | Required, non-empty opaque string: the presentation format served (for example `zkp_req`). |
| `credential.format`, `credential.type` | Required, non-empty opaque strings: the credential format and its type (mdoc doctype or SD-JWT VC `vct`). |
| `credential.issuance_profile` | Optional, non-empty opaque string (an empty string is rejected): an issuance profile the credential must follow for the circuit to accept it; a constraint on the input credential, not an interop profile of the circuit. The key was named `profile` before; that name is now rejected as an unknown field. |
| `functions` | Required, at least one. |
| `functions[].id` | Required, non-empty, unique within the entry. |
| `functions[].max` | Optional integer >= 1: the most instances of the function one proof can carry. Absent means unbounded. |
| `functions[].always` | Optional boolean: the circuit's statement always includes this function; a proof from this circuit always carries it. |
| `functions[].params` | Optional map param name -> constraint. A param that is not listed is not supported. |

A constraint has exactly one of:

- `{"enum": [...]}`: the value must be a string equal to one of the listed
  values. At least one value; values non-empty and unique.
- `{"type": "<name>"}`: the value must be of that type. Types: `full-date`
  (a string, a valid `YYYY-MM-DD` date), `string`, `integer` (a JSON number
  with no fraction).

Anything else is rejected: an explicit JSON `null` for any member (omit the
member instead; a null `max` would otherwise silently mean unbounded), a
constraint carrying both `enum` and `type` even if one is empty, unknown keys at any level (so an unknown constraint
kind is an error, never silently "no constraint"), unknown type names,
`schema` other than 1. Function ids, param names and enum values are opaque to
the catalog; the mechanism carries no knowledge of any function family, so new
families and circuit kinds add entries, not schema.

`circuitctl add --capabilities <file.json>` sets the field on a new entry,
`circuitctl set-capabilities <id> --capabilities <file.json>` replaces it on an
existing one and `circuitctl set-capabilities <id> --clear` removes it. Both
validate the file and regenerate the manifest. `circuitctl verify` re-validates
every entry's capabilities.

## Trust

`capabilities` is descriptive, like `source.verifiedBy`. It authenticates
nothing. Which circuits a wallet will actually use is still governed by its own
pin table (spec §7.3). A wrong claim costs a failed proof, not a wrong
acceptance.

## Entry fields that go with capabilities (nym_auth rulebook v0.4)

These are optional, additive entry fields (`manifestVersion` stays 1):

- `proofSystem`: the proof-system identity a verifier lists in
  `proof_system_values` (`longfellow-libzk-v1`, `vega-v1`). It is distinct from
  `system`, which stays the client's adapter key (`longfellow-nymauth`,
  `vega-nymauth`). A client that registers a circuit for `zkp_req` must read
  `proofSystem` and must not fall back to `system`. Present in both the
  manifest entry and `/v1/circuits/{id}.json`.
- `aliases` may contain `:` so a URN can be an alias (for example
  `urn:circuits:longfellow:nym_auth:v1`, the rulebook's example `system_id`).
  The `id` charset is unchanged. The SD-JWT nym_auth entries carry no URN alias:
  the rulebook v0.4 defines no SD-JWT binding.
- The `pseudonym` function lists `config`, `ver` and `prf`, as the matcher
  (siros-dc-matcher 0.11.0) requires. A constraint cannot be an integer enum, so
  `ver` is `{"type": "integer"}`; the matcher refuses any `ver` other than 1
  itself. The SD-JWT entries declare credential format `dc+sd-jwt`, so an
  `mso_mdoc` request is never served by them.

`circuitctl add --proof-system <v>` sets `proofSystem` on a new entry.
