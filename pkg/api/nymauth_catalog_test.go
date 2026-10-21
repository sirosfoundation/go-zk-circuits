package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirosfoundation/g119612/pkg/logging"
	"github.com/stretchr/testify/require"

	zkcircuits "github.com/sirosfoundation/go-zk-circuits"
	"github.com/sirosfoundation/go-zk-circuits/pkg/api"
	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// The nym_auth entries describe themselves per nym_auth rulebook v0.4 /
// siros-dc-matcher 0.11.0. These tests run against the REAL embedded catalog
// (the data that is deployed), served through the real handlers.

type nymWant struct {
	proofSystem string
	system      string
	alias       string // "" = none
	format      string
}

var nymWants = map[string]nymWant{
	"nymauth-longfellow-mdoc-v1":  {"longfellow-libzk-v1", "longfellow-nymauth", "urn:circuits:longfellow:nym_auth:v1", "mso_mdoc"},
	"nymauth-longfellow-sdjwt-v1": {"longfellow-libzk-v1", "longfellow-nymauth", "", "dc+sd-jwt"},
	"nymauth-vega-mdoc-v1":        {"vega-v1", "vega-nymauth", "urn:circuits:vega:nym_auth:v1", "mso_mdoc"},
	"nymauth-vega-sdjwt-v1":       {"vega-v1", "vega-nymauth", "", "dc+sd-jwt"},
}

func realServer(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	sc := api.NewServerContext(logging.DefaultLogger(), zkcircuits.EmbeddedFS, "https://zk-circuits.test")
	require.NoError(t, sc.LoadCatalog())
	r := gin.New()
	api.RegisterAPIRoutes(r, sc)
	return r
}

func get(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestNymAuthEntries_ManifestAndPerCircuitAgree(t *testing.T) {
	r := realServer(t)
	w := get(t, r, "/v1/manifest.json")
	require.Equal(t, http.StatusOK, w.Code)
	var m catalog.Manifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	require.Equal(t, 1, m.ManifestVersion)

	inManifest := map[string]catalog.CircuitDescriptor{}
	for _, e := range m.Circuits {
		inManifest[e.ID] = e
	}
	for id, want := range nymWants {
		me, ok := inManifest[id]
		require.True(t, ok, id)

		pw := get(t, r, "/v1/circuits/"+id+".json")
		require.Equal(t, http.StatusOK, pw.Code, id)
		var pe catalog.CircuitDescriptor
		require.NoError(t, json.Unmarshal(pw.Body.Bytes(), &pe))

		// proofSystem is a top-level JSON member in both documents.
		var raw map[string]any
		require.NoError(t, json.Unmarshal(pw.Body.Bytes(), &raw))
		require.Equal(t, want.proofSystem, raw["proofSystem"], id)
		require.Equal(t, want.proofSystem, me.ProofSystem, id)
		require.Equal(t, me.ProofSystem, pe.ProofSystem, id)
		require.Equal(t, want.system, me.System, "system is the SDK adapter key and must not change")
		require.Equal(t, me.Aliases, pe.Aliases, id)

		require.NotNil(t, me.Capabilities)
		require.Equal(t, "zkp_req", me.Capabilities.Presentation, id)
		require.Equal(t, want.format, me.Capabilities.Credential.Format, id)

		if want.alias == "" {
			require.Empty(t, me.Aliases, id)
			continue
		}
		require.Equal(t, []string{want.alias}, me.Aliases, id)
		aw := get(t, r, "/v1/circuits/"+want.alias+".json")
		require.Equal(t, http.StatusMovedPermanently, aw.Code, id)
		require.Equal(t, "/v1/circuits/"+id+".json", aw.Header().Get("Location"))
	}
}

func TestNymAuthEntries_PseudonymFunctionMatchesMatcherTable(t *testing.T) {
	r := realServer(t)
	for id := range nymWants {
		var e catalog.CircuitDescriptor
		require.NoError(t, json.Unmarshal(get(t, r, "/v1/circuits/"+id+".json").Body.Bytes(), &e))
		fns := map[string]catalog.FunctionCap{}
		for _, f := range e.Capabilities.Functions {
			fns[f.ID] = f
		}
		require.NotContains(t, fns, "pseudonym-v1-sha256", id)
		p, ok := fns["pseudonym"]
		require.True(t, ok, id)
		require.True(t, p.Always)
		require.Equal(t, []string{"known-offender-matching-ssc"}, p.Params["config"].Enum)
		require.Equal(t, catalog.ParamConstraint{Type: catalog.ParamTypeInteger}, p.Params["ver"])
		require.Equal(t, []string{"sha-256"}, p.Params["prf"].Enum)
		require.Len(t, p.Params, 3)
		require.Contains(t, fns, "age_check")
	}
}
