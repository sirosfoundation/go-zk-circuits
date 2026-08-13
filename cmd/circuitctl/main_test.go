package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitLeadingPositional_PositionalFirst(t *testing.T) {
	pos, rest, err := splitLeadingPositional([]string{"file.zst", "--system", "longfellow", "--origin", "o"}, nil)
	require.NoError(t, err)
	require.Equal(t, "file.zst", pos)
	require.Equal(t, []string{"--system", "longfellow", "--origin", "o"}, rest)
}

func TestSplitLeadingPositional_BoolFlagDoesNotSwallowNextFlag(t *testing.T) {
	// Regression test: a boolean flag (no value) sitting between two
	// value-taking flags previously consumed the next flag's NAME as its
	// own "value", stranding that flag's real value as a bare token that
	// got misidentified as a second positional argument. Caught by manual
	// testing against `circuitctl add ... --open-source --added-by X`,
	// not by any test — hence this one.
	pos, rest, err := splitLeadingPositional([]string{
		"file.zst", "--license", "MPL-2.0", "--open-source", "--added-by", "demo@example.invalid",
	}, map[string]bool{"open-source": true})
	require.NoError(t, err)
	require.Equal(t, "file.zst", pos)
	require.Equal(t, []string{"--license", "MPL-2.0", "--open-source", "--added-by", "demo@example.invalid"}, rest)
}

func TestSplitLeadingPositional_BoolFlagAtEnd(t *testing.T) {
	pos, rest, err := splitLeadingPositional([]string{"file.zst", "--added-by", "a", "--unpublished"}, map[string]bool{"unpublished": true})
	require.NoError(t, err)
	require.Equal(t, "file.zst", pos)
	require.Equal(t, []string{"--added-by", "a", "--unpublished"}, rest)
}

func TestSplitLeadingPositional_RejectsMultiplePositionals(t *testing.T) {
	_, _, err := splitLeadingPositional([]string{"a", "b"}, nil)
	require.Error(t, err)
}

func TestSplitLeadingPositional_RejectsNoPositionals(t *testing.T) {
	_, _, err := splitLeadingPositional([]string{"--system", "longfellow"}, nil)
	require.Error(t, err)
}
