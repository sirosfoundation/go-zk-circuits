// Package zkcircuits embeds the catalog data (circuit metadata + artifact
// blobs) directly into the service binary (spec §4.4/§4.5: go:embed is the
// v1 storage strategy — atomic, git-reviewable, no runtime storage config).
//
// This file must live at the module root because Go's go:embed patterns
// cannot reference paths outside the directory of the file containing the
// directive ("../" is not allowed) — cmd/zkc lives in a different
// directory than artifacts/ and catalog/, so the embedding has to happen
// here and be imported by cmd/zkc, not the other way around.
package zkcircuits

import "embed"

//go:embed artifacts catalog/circuits catalog/manifest.json
var EmbeddedFS embed.FS
