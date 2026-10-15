package catalog

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func intp(n int) *int { return &n }

func strp(s string) *string { return &s }

func validCaps() *Capabilities {
	return &Capabilities{
		Schema:       1,
		Presentation: "circuit_zk",
		Credential:   &CredentialCap{Format: "mso_mdoc", Type: "doc.type", IssuanceProfile: strp("spec-2.1")},
		Functions: []FunctionCap{
			{ID: "f-always", Always: true, Params: map[string]ParamConstraint{
				"cfg": {Enum: []string{"a"}},
			}},
			{ID: "f-age", Max: intp(1), Params: map[string]ParamConstraint{
				"ge": {Type: ParamTypeFullDate},
			}},
		},
	}
}

func TestValidateCapabilities_Valid(t *testing.T) {
	require.NoError(t, ValidateCapabilities(validCaps()))
}

func TestValidateCapabilities_Rules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *Capabilities)
		want   string
	}{
		{"schema must be 1", func(c *Capabilities) { c.Schema = 2 }, "schema is 2"},
		{"schema zero", func(c *Capabilities) { c.Schema = 0 }, "schema is 0"},
		{"presentation required", func(c *Capabilities) { c.Presentation = "" }, "presentation is required"},
		{"credential required", func(c *Capabilities) { c.Credential = nil }, "credential is required"},
		{"credential.format required", func(c *Capabilities) { c.Credential.Format = "" }, "credential.format is required"},
		{"credential.type required", func(c *Capabilities) { c.Credential.Type = "" }, "credential.type is required"},
		{"credential.issuance_profile non-empty", func(c *Capabilities) { c.Credential.IssuanceProfile = strp("") }, "credential.issuance_profile must not be empty"},
		{"functions non-empty", func(c *Capabilities) { c.Functions = nil }, "at least one function"},
		{"function id required", func(c *Capabilities) { c.Functions[0].ID = "" }, "id is required"},
		{"function id unique", func(c *Capabilities) { c.Functions[1].ID = "f-always" }, "duplicate function id"},
		{"max at least 1", func(c *Capabilities) { c.Functions[1].Max = intp(0) }, "max must be at least 1"},
		{"param name non-empty", func(c *Capabilities) { c.Functions[1].Params[""] = ParamConstraint{Type: ParamTypeString} }, "param name must not be empty"},
		{"constraint both", func(c *Capabilities) {
			c.Functions[1].Params["ge"] = ParamConstraint{Type: ParamTypeString, Enum: []string{"a"}}
		}, "got both"},
		{"constraint neither", func(c *Capabilities) { c.Functions[1].Params["ge"] = ParamConstraint{} }, "got neither"},
		{"unknown type", func(c *Capabilities) { c.Functions[1].Params["ge"] = ParamConstraint{Type: "regex"} }, `unknown type "regex"`},
		{"enum non-empty", func(c *Capabilities) { c.Functions[0].Params["cfg"] = ParamConstraint{Enum: []string{}} }, "at least one value"},
		{"enum value non-empty", func(c *Capabilities) { c.Functions[0].Params["cfg"] = ParamConstraint{Enum: []string{"a", ""}} }, "must not be empty"},
		{"enum value unique", func(c *Capabilities) { c.Functions[0].Params["cfg"] = ParamConstraint{Enum: []string{"a", "a"}} }, "listed twice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validCaps()
			tc.mutate(c)
			require.ErrorContains(t, ValidateCapabilities(c), tc.want)
		})
	}
}

func TestParseCapabilities_RejectsUnknownKeys(t *testing.T) {
	cases := map[string]string{ //#nosec G101 -- JSON fixtures, no credentials
		"constraint kind not known": `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t"},"functions":[{"id":"x","params":{"p":{"regex":"a.*"}}}]}`,
		"extra top-level field":     `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t"},"functions":[{"id":"x"}],"extra":1}`,
		"extra function field":      `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t"},"functions":[{"id":"x","min":1}]}`,
		"extra credential field":    `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t","issuer":"i"},"functions":[{"id":"x"}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCapabilities([]byte(doc))
			require.ErrorContains(t, err, "unknown field")
		})
	}
}

func TestParseCapabilities_IssuanceProfile(t *testing.T) {
	const base = `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t"%s},"functions":[{"id":"x"}]}`

	c, err := ParseCapabilities([]byte(fmt.Sprintf(base, `,"issuance_profile":"spec-2.1"`)))
	require.NoError(t, err)
	require.NotNil(t, c.Credential.IssuanceProfile)
	require.Equal(t, "spec-2.1", *c.Credential.IssuanceProfile)

	c, err = ParseCapabilities([]byte(fmt.Sprintf(base, "")))
	require.NoError(t, err, "issuance_profile is optional")
	require.Nil(t, c.Credential.IssuanceProfile)

	_, err = ParseCapabilities([]byte(fmt.Sprintf(base, `,"issuance_profile":""`)))
	require.ErrorContains(t, err, "issuance_profile must not be empty")

	// The pre-rename key is now an unknown field, with a pointer to the new name.
	_, err = ParseCapabilities([]byte(fmt.Sprintf(base, `,"profile":"compact"`)))
	require.ErrorContains(t, err, `unknown field "profile"`)
	require.ErrorContains(t, err, "renamed credential.issuance_profile")
}

func TestParseCapabilities_NullAndGarbage(t *testing.T) {
	_, err := ParseCapabilities([]byte("null"))
	require.ErrorContains(t, err, "null")
	_, err = ParseCapabilities([]byte("{"))
	require.Error(t, err)
	_, err = ParseCapabilities([]byte(`{"schema":1}`))
	require.ErrorContains(t, err, "presentation is required")
}

func TestParseCapabilities_RoundTrip(t *testing.T) {
	doc, err := json.Marshal(validCaps())
	require.NoError(t, err)
	c, err := ParseCapabilities(doc)
	require.NoError(t, err)
	require.Equal(t, validCaps(), c)
}

func TestValidateEntry_CapabilitiesOptionalAndChecked(t *testing.T) {
	e := sampleEntry("e1")
	require.NoError(t, ValidateEntry(&e), "absent capabilities is fine")

	e.Capabilities = validCaps()
	require.NoError(t, ValidateEntry(&e))

	e.Capabilities.Schema = 9
	require.ErrorContains(t, ValidateEntry(&e), "capabilities:")
}

func TestCircuitDescriptor_AbsentCapabilitiesNotEmitted(t *testing.T) {
	out, err := json.Marshal(sampleEntry("e1"))
	require.NoError(t, err)
	require.NotContains(t, string(out), "capabilities")
}

func TestCircuitDescriptor_UnmarshalRejectsMalformedCapabilities(t *testing.T) {
	var e CircuitDescriptor
	err := json.Unmarshal([]byte(`{"id":"x","capabilities":{"schema":1,"bogus":true}}`), &e)
	require.ErrorContains(t, err, "unknown field")
}

func TestCircuitDescriptor_UnmarshalRejectsNullCapabilities(t *testing.T) {
	for _, doc := range []string{
		`{"id":"x","capabilities":null}`,
		`{"id":"x","capabilities": null }`,
		`{"id":"x","Capabilities":null}`,
	} {
		var e CircuitDescriptor
		err := json.Unmarshal([]byte(doc), &e)
		require.ErrorContains(t, err, "got null", doc)
	}
	// Same rejection when the entry sits inside a manifest.
	var m Manifest
	err := json.Unmarshal([]byte(`{"circuits":[{"id":"x","capabilities":null}]}`), &m)
	require.ErrorContains(t, err, "got null")
}

func TestCircuitDescriptor_UnmarshalAbsentCapabilitiesIsNil(t *testing.T) {
	var e CircuitDescriptor
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","system":"s"}`), &e))
	require.Nil(t, e.Capabilities)
	require.Equal(t, "x", e.ID)
	require.Equal(t, "s", e.System)
}

func TestParseCapabilities_RejectsNullMembersAndAmbiguousConstraints(t *testing.T) {
	base := func(cred, fn string) string {
		return `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t"` + cred + `},"functions":[` + fn + `]}`
	}
	cases := map[string]string{
		"max null":              base("", `{"id":"f","max":null}`),
		"always null":           base("", `{"id":"f","always":null}`),
		"issuance_profile null": base(`,"issuance_profile":null`, `{"id":"f"}`),
		"params null":           base("", `{"id":"f","params":null}`),
		"constraint null":       base("", `{"id":"f","params":{"p":null}}`),
		"enum null + type":      base("", `{"id":"f","params":{"p":{"enum":null,"type":"string"}}}`),
		"enum + empty type":     base("", `{"id":"f","params":{"p":{"enum":["a"],"type":""}}}`),
		"enum null in list":     base("", `{"id":"f","params":{"p":{"enum":["a",null]}}}`),
		"functions null":        `{"schema":1,"presentation":"p","credential":{"format":"f","type":"t"},"functions":null}`,
		"credential null":       `{"schema":1,"presentation":"p","credential":null,"functions":[{"id":"f"}]}`,
	}
	for name, doc := range cases {
		_, err := ParseCapabilities([]byte(doc))
		require.Error(t, err, name)
		// the same documents are rejected when loaded as part of an entry
		var e CircuitDescriptor
		require.Error(t, json.Unmarshal([]byte(`{"id":"x","capabilities":`+doc+`}`), &e), name)
	}
	_, err := ParseCapabilities([]byte(base("", `{"id":"f","max":3,"always":true,"params":{"p":{"enum":["a"]},"q":{"type":"string"}}}`)))
	require.NoError(t, err)
}
