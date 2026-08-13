package publish

import (
	"fmt"
	"regexp"
	"strconv"
)

// longfellowFilenamePattern matches the upstream circuit filename convention
// `<version>_<numAttributes>_<blockEncHash>_<blockEncSig>_<circuitHash>`
// (spec §2.5), e.g. "8_2_4307_2945_bb8e6a26d2700ddad968562d1c4aee83067772fee6f889748a0bc64f2c694ad5".
var longfellowFilenamePattern = regexp.MustCompile(`^([0-9]+)_([0-9]+)_([0-9]+)_([0-9]+)_([0-9a-fA-F]+)$`)

// LongfellowParams is the parsed form of a longfellow circuit filename.
type LongfellowParams struct {
	Version       int
	NumAttributes int
	BlockEncHash  int
	BlockEncSig   int
	CircuitHash   string
	Filename      string
}

// ParseLongfellowFilename parses the upstream naming convention. Spec §5.3
// step 4: this is the only self-describing metadata these artifacts have,
// so a parse failure here means the file was renamed and must block the publish.
func ParseLongfellowFilename(filename string) (*LongfellowParams, error) {
	m := longfellowFilenamePattern.FindStringSubmatch(filename)
	if m == nil {
		return nil, fmt.Errorf("filename %q does not match the longfellow convention <version>_<numAttributes>_<blockEncHash>_<blockEncSig>_<circuitHash>", filename)
	}
	version, _ := strconv.Atoi(m[1])
	numAttributes, _ := strconv.Atoi(m[2])
	blockEncHash, _ := strconv.Atoi(m[3])
	blockEncSig, _ := strconv.Atoi(m[4])
	return &LongfellowParams{
		Version:       version,
		NumAttributes: numAttributes,
		BlockEncHash:  blockEncHash,
		BlockEncSig:   blockEncSig,
		CircuitHash:   m[5],
		Filename:      filename,
	}, nil
}

// DefaultID returns the id convention spec §2.3/§2.11 illustrates:
// "longfellow-libzk-v1_<version>_<numAttributes>_<blockEncHash>_<blockEncSig>".
// This is a provisional convention (spec §9.3 — the real interop verifier's
// advertised spec id is not yet confirmed); circuitctl always allows an
// explicit --id override, and aliases exist precisely to hedge this guess.
func (p *LongfellowParams) DefaultID() string {
	return fmt.Sprintf("longfellow-libzk-v1_%d_%d_%d_%d", p.Version, p.NumAttributes, p.BlockEncHash, p.BlockEncSig)
}

// ToParams renders the parsed filename as the params bag spec §2.5 defines,
// scalar-only per §2.2 (numbers as float64, matching how encoding/json would
// decode a canonical manifest — see catalog.validateParams).
func (p *LongfellowParams) ToParams() map[string]any {
	return map[string]any{
		"version":           float64(p.Version),
		"num_attributes":    float64(p.NumAttributes),
		"block_enc_hash":    float64(p.BlockEncHash),
		"block_enc_sig":     float64(p.BlockEncSig),
		"circuit_hash":      p.CircuitHash,
		"upstream_filename": p.Filename,
	}
}

// CrossCheckParam fails loudly if an explicitly-passed --param contradicts
// the filename (spec §5.3 step 4), rather than silently preferring one.
func (p *LongfellowParams) CrossCheckParam(key, explicitValue string) error {
	derived := p.ToParams()
	dv, ok := derived[key]
	if !ok {
		return nil // not a filename-derived key; nothing to cross-check
	}
	var derivedStr string
	switch v := dv.(type) {
	case float64:
		derivedStr = strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		derivedStr = v
	}
	if derivedStr != explicitValue {
		return fmt.Errorf("--param %s=%s contradicts the filename-derived value %q — the filename is the authoritative source for this key", key, explicitValue, derivedStr)
	}
	return nil
}
