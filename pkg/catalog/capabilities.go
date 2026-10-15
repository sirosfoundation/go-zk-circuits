package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// CapabilitiesSchema is the only capabilities schema version this build
// understands. It is independent of ManifestVersion: capabilities is an
// optional, additive field, so adding it did not bump manifestVersion.
const CapabilitiesSchema = 1

// Param constraint type names (see docs/capabilities.md).
const (
	ParamTypeFullDate = "full-date" // RFC 3339 full-date, YYYY-MM-DD
	ParamTypeString   = "string"
	ParamTypeInteger  = "integer"
)

// Capabilities declares, as machine-readable metadata, what a circuit proves
// (docs/capabilities.md). Consumers interpret it themselves; this package only
// checks that it is well-formed. It is descriptive metadata, like
// Source.VerifiedBy: it authenticates nothing, and which circuit a client
// actually uses stays governed by that client's own pin table (spec §7.3).
//
// Function ids, param names and values are opaque strings to the catalog;
// the mechanism carries no knowledge of any particular function family.
type Capabilities struct {
	Schema       int            `json:"schema"`
	Presentation string         `json:"presentation"`
	Credential   *CredentialCap `json:"credential"`
	Functions    []FunctionCap  `json:"functions"`
}

// CredentialCap is the credential a circuit proves over: its format, its
// type (an mdoc doctype or an SD-JWT VC vct) and, optionally, an issuance
// profile the credential must follow for the circuit to accept it. The
// issuance profile is a constraint on the input credential, not an
// interoperability profile of the circuit. It is a pointer so that an
// explicitly empty string can be told apart from absence and rejected.
type CredentialCap struct {
	Format          string  `json:"format"`
	Type            string  `json:"type"`
	IssuanceProfile *string `json:"issuance_profile,omitempty"`
}

// FunctionCap is one function a circuit supports. Max bounds the number of
// instances of the function per proof (nil = unbounded). Always means the
// circuit's statement always includes the function, so the circuit can only
// serve requests that ask for it. A param that is not listed in Params is
// not supported.
type FunctionCap struct {
	ID     string                     `json:"id"`
	Max    *int                       `json:"max,omitempty"`
	Always bool                       `json:"always,omitempty"`
	Params map[string]ParamConstraint `json:"params,omitempty"`
}

// ParamConstraint restricts a param's value: exactly one of Enum (the value
// must be one of these strings) or Type (the value must be of this type).
type ParamConstraint struct {
	Enum []string `json:"enum,omitempty"`
	Type string   `json:"type,omitempty"`
}

// UnmarshalJSON decodes strictly: an unknown key anywhere inside
// capabilities (for example a constraint kind this build does not know) is an
// error rather than being silently dropped, so a typo or a newer schema can
// never be read as "no constraint".
func (c *Capabilities) UnmarshalJSON(data []byte) error {
	type plain Capabilities // drops this method, avoiding recursion
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p plain
	if err := dec.Decode(&p); err != nil {
		if strings.Contains(err.Error(), `unknown field "profile"`) {
			return fmt.Errorf("capabilities: %w (if this is credential.profile: it was renamed credential.issuance_profile)", err)
		}
		return fmt.Errorf("capabilities: %w", err)
	}
	*c = Capabilities(p)
	return nil
}

// ParseCapabilities strictly decodes and validates a capabilities document
// (the file given to circuitctl --capabilities).
func ParseCapabilities(data []byte) (*Capabilities, error) {
	var c *Capabilities
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("capabilities document is null")
	}
	if err := ValidateCapabilities(c); err != nil {
		return nil, err
	}
	return c, nil
}

// ValidateCapabilities checks a Capabilities value against the rules in docs/capabilities.md.
func ValidateCapabilities(c *Capabilities) error {
	if c.Schema != CapabilitiesSchema {
		return fmt.Errorf("schema is %d, this build understands %d", c.Schema, CapabilitiesSchema)
	}
	if c.Presentation == "" {
		return fmt.Errorf("presentation is required")
	}
	if c.Credential == nil {
		return fmt.Errorf("credential is required")
	}
	if c.Credential.Format == "" {
		return fmt.Errorf("credential.format is required")
	}
	if c.Credential.Type == "" {
		return fmt.Errorf("credential.type is required")
	}
	if c.Credential.IssuanceProfile != nil && *c.Credential.IssuanceProfile == "" {
		return fmt.Errorf("credential.issuance_profile must not be empty when present")
	}
	if len(c.Functions) == 0 {
		return fmt.Errorf("functions must list at least one function")
	}
	seen := map[string]bool{}
	for i := range c.Functions {
		f := &c.Functions[i]
		if f.ID == "" {
			return fmt.Errorf("functions[%d]: id is required", i)
		}
		if seen[f.ID] {
			return fmt.Errorf("functions[%d]: duplicate function id %q", i, f.ID)
		}
		seen[f.ID] = true
		if f.Max != nil && *f.Max < 1 {
			return fmt.Errorf("function %q: max must be at least 1, got %d", f.ID, *f.Max)
		}
		for name, pc := range f.Params {
			if name == "" {
				return fmt.Errorf("function %q: param name must not be empty", f.ID)
			}
			if err := validateParamConstraint(&pc); err != nil {
				return fmt.Errorf("function %q param %q: %w", f.ID, name, err)
			}
		}
	}
	return nil
}

func validateParamConstraint(pc *ParamConstraint) error {
	hasEnum, hasType := pc.Enum != nil, pc.Type != ""
	switch {
	case hasEnum && hasType:
		return fmt.Errorf("constraint must have exactly one of enum or type, got both")
	case !hasEnum && !hasType:
		return fmt.Errorf("constraint must have exactly one of enum or type, got neither")
	case hasType:
		switch pc.Type {
		case ParamTypeFullDate, ParamTypeString, ParamTypeInteger:
			return nil
		}
		return fmt.Errorf("unknown type %q", pc.Type)
	}
	if len(pc.Enum) == 0 {
		return fmt.Errorf("enum must list at least one value")
	}
	seen := map[string]bool{}
	for _, v := range pc.Enum {
		if v == "" {
			return fmt.Errorf("enum values must not be empty")
		}
		if seen[v] {
			return fmt.Errorf("enum value %q is listed twice", v)
		}
		seen[v] = true
	}
	return nil
}
