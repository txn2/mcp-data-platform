package platform

import (
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// adoptingConnMgr is a recordingConnMgr that also implements
// toolkit.ConnectionAdopter.
type adoptingConnMgr struct {
	recordingConnMgr
}

func (m *adoptingConnMgr) AdoptConnection(name string, config map[string]any) error {
	m.events = append(m.events, "adopt:"+name)
	m.addConfigs = append(m.addConfigs, config)
	return m.addErr
}

// TestReloadConnectionLocal_UpsertAdoptsOnAnAdopter: a peer's announcement of a
// saved connection is handed whole to a toolkit that installs what the saving
// replica stored, whether or not it holds the connection (#1714).
func TestReloadConnectionLocal_UpsertAdoptsOnAnAdopter(t *testing.T) {
	const name = "erp"
	cfg := map[string]any{"endpoint_url": "https://erp.example.com/graphql"}
	for _, has := range []bool{false, true} {
		tk := &adoptingConnMgr{recordingConnMgr{mockToolkit: mockToolkit{kind: graphqlkit.Kind}, has: has}}
		reg := registry.NewRegistry()
		if err := reg.Register(tk); err != nil {
			t.Fatalf("register toolkit: %v", err)
		}
		p := &Platform{
			toolkitRegistry: reg,
			connectionStore: configurableConnStore{inst: &ConnectionInstance{Kind: graphqlkit.Kind, Name: name, Config: cfg}},
		}

		p.reloadConnectionLocal(graphqlkit.Kind, name, ReloadUpsert.String())

		if !slices.Equal(tk.events, []string{"adopt:erp"}) {
			t.Errorf("has=%v: events = %v, want [adopt:erp]", has, tk.events)
		}
	}
}
