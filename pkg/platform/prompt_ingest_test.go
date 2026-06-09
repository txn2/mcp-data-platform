package platform

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// newIngestTestPlatform builds a platform with an in-memory prompt store, a
// toolkit registry carrying one PromptDescriber toolkit, and the given
// platform-level static prompt infos already collected into promptInfos.
func newIngestTestPlatform(platformInfos, toolkitInfos []registry.PromptInfo) (*Platform, *mockPlatformPromptStore) {
	store := newMockPlatformPromptStore()
	reg := registry.NewRegistry()
	_ = reg.Register(&mockToolkitWithPrompts{
		mockToolkit: mockToolkit{kind: "knowledge", name: "primary"},
		prompts:     toolkitInfos,
	})
	p := &Platform{
		config:          &Config{Admin: AdminConfig{Persona: "admin"}},
		mcpServer:       mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil),
		promptStore:     store,
		toolkitRegistry: reg,
		promptInfos:     platformInfos,
	}
	return p, store
}

func TestIngestStaticPrompts(t *testing.T) {
	platformInfos := []registry.PromptInfo{
		{Name: "explore-data", Description: "Discover data", Content: "Explore {topic}", Category: "workflow"},
	}
	toolkitInfos := []registry.PromptInfo{
		{Name: "capture-knowledge", Description: "Record insights", Content: "Capture", Category: "toolkit"},
	}
	p, store := newIngestTestPlatform(platformInfos, toolkitInfos)

	p.ingestStaticPrompts(context.Background())

	// Both the platform and toolkit static prompts are ingested as read-only,
	// approved, global system rows ready for indexing.
	for _, name := range []string{"explore-data", "capture-knowledge"} {
		got, _ := store.Get(context.Background(), name)
		if got == nil {
			t.Fatalf("prompt %q not ingested", name)
		}
		if got.Source != prompt.SourceSystem {
			t.Errorf("%q source = %q, want %q", name, got.Source, prompt.SourceSystem)
		}
		if got.Status != prompt.StatusApproved {
			t.Errorf("%q status = %q, want %q", name, got.Status, prompt.StatusApproved)
		}
		if got.Scope != prompt.ScopeGlobal {
			t.Errorf("%q scope = %q, want %q", name, got.Scope, prompt.ScopeGlobal)
		}
		if !got.Enabled {
			t.Errorf("%q should be enabled", name)
		}
	}

	// Idempotent: a second run does not duplicate rows.
	p.ingestStaticPrompts(context.Background())
	all, _ := store.List(context.Background(), prompt.ListFilter{Source: prompt.SourceSystem})
	if len(all) != 2 {
		t.Errorf("after re-ingest, system rows = %d, want 2", len(all))
	}
}

func TestIngestStaticPrompts_SkipsNonSystemName(t *testing.T) {
	infos := []registry.PromptInfo{{Name: "shared-name", Description: "from config", Content: "x"}}
	p, store := newIngestTestPlatform(infos, nil)

	// A user/admin already owns this global name (non-system).
	store.prompts["shared-name"] = &prompt.Prompt{
		ID: "user-1", Name: "shared-name", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceOperator, Description: "user owned", Content: "user content",
	}

	p.ingestStaticPrompts(context.Background())

	got, _ := store.Get(context.Background(), "shared-name")
	if got.Source != prompt.SourceOperator || got.Description != "user owned" {
		t.Errorf("ingest clobbered a non-system prompt: %+v", got)
	}
}

func TestIngestStaticPrompts_PrunesStaleSystemRows(t *testing.T) {
	infos := []registry.PromptInfo{{Name: "current", Description: "d", Content: "c"}}
	p, store := newIngestTestPlatform(infos, nil)

	// A system row from a prior build that is no longer registered.
	store.prompts["removed"] = &prompt.Prompt{
		ID: "sys-removed", Name: "removed", Scope: prompt.ScopeGlobal, Source: prompt.SourceSystem,
	}

	p.ingestStaticPrompts(context.Background())

	if got, _ := store.Get(context.Background(), "removed"); got != nil {
		t.Errorf("stale system prompt %q was not pruned", "removed")
	}
	if got, _ := store.Get(context.Background(), "current"); got == nil {
		t.Error("current static prompt should remain")
	}
}

func TestIngestStaticPrompts_StoreErrorsAreNonFatal(t *testing.T) {
	infos := []registry.PromptInfo{{
		Name: "p1", Description: "d", Content: "c",
		Arguments: []registry.PromptArgumentInfo{{Name: "topic", Description: "subject", Required: true}},
	}}

	// Create failure: ingest logs and continues; nothing is stored.
	p, store := newIngestTestPlatform(infos, nil)
	store.createErr = assert.AnError
	p.ingestStaticPrompts(context.Background())
	if got, _ := store.Get(context.Background(), "p1"); got != nil {
		t.Error("nothing should be stored when create fails")
	}

	// Lookup failure: ingest logs and continues without panicking.
	p2, store2 := newIngestTestPlatform(infos, nil)
	store2.getErr = assert.AnError
	p2.ingestStaticPrompts(context.Background())

	// Prune list failure: the upsert still creates the row.
	p3, store3 := newIngestTestPlatform(infos, nil)
	store3.listErr = assert.AnError
	p3.ingestStaticPrompts(context.Background())
	if got, _ := store3.Get(context.Background(), "p1"); got == nil {
		t.Error("p1 should be created even when the prune list fails")
	}

	// Prune delete failure: ingest logs and continues.
	p4, store4 := newIngestTestPlatform(infos, nil)
	store4.prompts["stale"] = &prompt.Prompt{
		ID: "x", Name: "stale", Scope: prompt.ScopeGlobal, Source: prompt.SourceSystem,
	}
	store4.deleteErr = assert.AnError
	p4.ingestStaticPrompts(context.Background())
}

func TestHandlePromptUpdate_SystemRowReadOnly(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["sys-prompt"] = &prompt.Prompt{
		ID: "s1", Name: "sys-prompt", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceSystem, Status: prompt.StatusApproved, Enabled: true,
	}
	r, _, _ := p.handlePromptUpdate(adminCtx(), managePromptInput{Name: "sys-prompt", Description: "hacked"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "read-only")
	assert.Equal(t, "", store.prompts["sys-prompt"].Description, "system row must be unchanged")
}

func TestHandlePromptDelete_SystemRowReadOnly(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["sys-prompt"] = &prompt.Prompt{
		ID: "s1", Name: "sys-prompt", Scope: prompt.ScopeGlobal, Source: prompt.SourceSystem,
	}
	r, _, _ := p.handlePromptDelete(adminCtx(), managePromptInput{Name: "sys-prompt"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "read-only")
	assert.Contains(t, store.prompts, "sys-prompt", "system row must not be deleted")
}
