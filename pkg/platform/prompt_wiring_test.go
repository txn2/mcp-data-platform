package platform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/portalstore"
	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// wiringPromptStore is a minimal in-memory prompt.Store + prompt.Searcher for the
// finalizeSetup wiring test. Search always succeeds (returning no rows), so the
// manage_prompt ranking reflects only whether an embedder is wired.
type wiringPromptStore struct {
	prompts map[string]*prompt.Prompt
}

func newWiringPromptStore() *wiringPromptStore {
	return &wiringPromptStore{prompts: make(map[string]*prompt.Prompt)}
}

func (m *wiringPromptStore) Create(_ context.Context, p *prompt.Prompt) error {
	m.prompts[p.Name] = p
	return nil
}

func (m *wiringPromptStore) Get(_ context.Context, name string) (*prompt.Prompt, error) {
	return m.prompts[name], nil //nolint:nilnil // interface contract
}

func (m *wiringPromptStore) GetPersonal(_ context.Context, ownerEmail, name string) (*prompt.Prompt, error) {
	for _, p := range m.prompts {
		if p.Scope == prompt.ScopePersonal && p.OwnerEmail == ownerEmail && p.Name == name {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *wiringPromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	for _, p := range m.prompts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *wiringPromptStore) Update(_ context.Context, p *prompt.Prompt) error {
	m.prompts[p.Name] = p
	return nil
}

func (m *wiringPromptStore) Delete(_ context.Context, name string) error {
	delete(m.prompts, name)
	return nil
}

func (m *wiringPromptStore) DeleteByID(_ context.Context, id string) error {
	for name, p := range m.prompts {
		if p.ID == id {
			delete(m.prompts, name)
		}
	}
	return nil
}

func (m *wiringPromptStore) List(_ context.Context, f prompt.ListFilter) ([]prompt.Prompt, error) { //nolint:revive // interface impl
	var out []prompt.Prompt
	for _, p := range m.prompts {
		if f.Scope != "" && p.Scope != f.Scope {
			continue
		}
		if f.OwnerEmail != "" && p.OwnerEmail != f.OwnerEmail {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func (m *wiringPromptStore) Count(_ context.Context, _ prompt.ListFilter) (int, error) {
	return len(m.prompts), nil
}

func (*wiringPromptStore) Search(_ context.Context, _ prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return nil, nil
}

var (
	_ prompt.Store    = (*wiringPromptStore)(nil)
	_ prompt.Searcher = (*wiringPromptStore)(nil)
)

// wiringEmbedder is a configured (non-noop) embedder that returns a fixed
// non-zero vector, so EmbedForSearch produces a query vector and manage_prompt
// search reports "hybrid" ranking whenever the embedder is actually wired.
type wiringEmbedder struct{}

func (*wiringEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0, 0, 0}, nil
}

func (*wiringEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0, 0}
	}
	return out, nil
}

func (*wiringEmbedder) Dimension() int { return 4 }
func (*wiringEmbedder) Kind() string   { return "stub" }

// TestBindPromptCollaborators_WiresEmbedderAndShareStore is the assembled-system
// proof (CLAUDE.md rule 5) that finalizeSetup's binding actually connects the
// Platform's embedding provider and portal share store into the prompt layer:
// the collaborators are observed reaching the layer through its own public
// serving surfaces, not asserted on a hand-built Handle. It exercises the real
// production wiring method (bindPromptCollaborators), so a future edit that drops
// a SetEmbedder/SetShareStore call or the portalStore nil-guard fails here.
func TestBindPromptCollaborators_WiresEmbedderAndShareStore(t *testing.T) {
	ctx := context.Background()

	store := newWiringPromptStore()
	// A personal prompt owned by sarah, shared with bob via the portal share store.
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal,
		OwnerEmail: "sarah@example.com", Content: "shared {x}", Enabled: true,
	}

	p := &Platform{
		config:        &Config{Admin: AdminConfig{Persona: "admin"}},
		prompts:       promptlayer.New(promptlayer.Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()}),
		embeddingProv: &wiringEmbedder{},
		portalStore: portalstore.NewFromStores(portalstore.Stores{
			Share: &stubShareStore{promptRefs: []portal.SharedPromptRef{
				{PromptID: "p1", ShareID: "s1", SharedBy: "sarah@example.com", Permission: portal.PermissionViewer},
			}},
		}, nil, portalstore.Config{}),
	}

	// Before binding, neither collaborator has reached the prompt layer: bob sees
	// no shared prompt, and search ranking would be lexical.
	require.Empty(t, p.prompts.ListVisible(ctx, "bob@example.com", nil),
		"no shared prompt is served before finalizeSetup binds the share store")

	// The real production wiring.
	p.bindPromptCollaborators()

	// Share store wired: bob now sees the shared prompt under its bare name.
	names := map[string]bool{}
	for _, pr := range p.prompts.ListVisible(ctx, "bob@example.com", nil) {
		names[pr.Name] = true
	}
	assert.True(t, names["report"],
		"finalizeSetup must wire the portal share store into the prompt layer")

	// Embedder wired: manage_prompt search reports hybrid ranking, observed
	// through the assembled tool + server (the only surface that reads the
	// embedder).
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	p.prompts.RegisterTool(server)
	session, cleanup := connectTestClientForWiring(t, server)
	defer cleanup()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "manage_prompt",
		Arguments: map[string]any{"command": "list", "query": "sales"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	var parsed struct {
		Ranking string `json:"ranking"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &parsed))
	assert.Equal(t, "hybrid", parsed.Ranking,
		"finalizeSetup must wire the embedding provider into the prompt layer")
}

// TestPromptSpecsFromConfig verifies the config-translation that initPromptStore
// feeds the prompt layer: operator PromptConfig entries map field-for-field to
// the owner's caller-neutral PromptSpec shape.
func TestPromptSpecsFromConfig(t *testing.T) {
	specs := promptSpecsFromConfig([]PromptConfig{
		{
			Name:        "a",
			Description: "da",
			Content:     "ca",
			Arguments:   []PromptArgumentConfig{{Name: "x", Description: "dx", Required: true}},
		},
		{Name: "b", Content: "cb"},
	})

	require.Len(t, specs, 2)
	assert.Equal(t, "a", specs[0].Name)
	assert.Equal(t, "da", specs[0].Description)
	assert.Equal(t, "ca", specs[0].Content)
	require.Len(t, specs[0].Arguments, 1)
	assert.Equal(t, "x", specs[0].Arguments[0].Name)
	assert.Equal(t, "dx", specs[0].Arguments[0].Description)
	assert.True(t, specs[0].Arguments[0].Required)
	assert.Equal(t, "b", specs[1].Name)
	assert.Empty(t, specs[1].Arguments)
}

// TestPromptDelegators verifies the four public Platform accessors external
// callers use forward to the prompt layer owner.
func TestPromptDelegators(t *testing.T) {
	store := newWiringPromptStore()
	p := &Platform{
		prompts: promptlayer.New(promptlayer.Config{
			Store: store, AdminPersona: "admin", Registry: registry.NewRegistry(),
		}),
	}

	// PromptStore returns the list_changed-notifying wrapper over the injected
	// store (#927), so it is not identity-equal; assert delegation by writing
	// through it and observing the write land in the injected store.
	require.NoError(t, p.PromptStore().Create(context.Background(), &prompt.Prompt{Name: "delegated"}))
	assert.Contains(t, store.prompts, "delegated", "PromptStore delegates writes to the owner's Store()")

	tracked := func() map[string]bool {
		m := map[string]bool{}
		for _, i := range p.AllPromptInfos() {
			m[i.Name] = true
		}
		return m
	}

	p.RegisterRuntimePrompt(&prompt.Prompt{Name: "g", Scope: prompt.ScopeGlobal})
	assert.True(t, tracked()["g"], "RegisterRuntimePrompt delegates and AllPromptInfos reflects it")

	p.UnregisterRuntimePrompt("g")
	assert.False(t, tracked()["g"], "UnregisterRuntimePrompt delegates")
}

// connectTestClientForWiring connects an in-memory MCP client to a server for the
// wiring test and returns the session; the caller must call cleanup().
func connectTestClientForWiring(t *testing.T, server *mcp.Server) (session *mcp.ClientSession, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)

	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}
}
