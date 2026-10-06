package searchfed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// pagePrompts holds a global prompt and one personal to someone else.
type pagePrompts struct{ prompt.Store }

func (pagePrompts) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	rows := map[string]*prompt.Prompt{
		"global": {ID: "global", Name: "g", Scope: prompt.ScopeGlobal, Status: prompt.StatusApproved, Enabled: true},
		"janes":  {ID: "janes", Name: "j", Scope: prompt.ScopePersonal, OwnerEmail: "jane@example.com", Status: prompt.StatusApproved, Enabled: true},
	}
	return rows[id], nil
}

// TestPageReferenceOpenerAppliesEachKindsRule pins #2028: a fetched page's
// references are judged by the rules the portal applies to the same list.
func TestPageReferenceOpenerAppliesEachKindsRule(t *testing.T) {
	opens := pageReferenceOpener(Config{AssetStore: outputAssets{}, ResourceStore: outputResources{}, PromptStore: pagePrompts{}})
	bob := knowledge.Caller{UserID: "u-bob", Email: "bob@example.com"}
	cases := []struct {
		ref  knowledgepage.EntityRef
		want bool
	}{
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetAsset, AssetID: "mine"}, true},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetAsset, AssetID: "theirs"}, false},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetAsset, AssetID: "gone"}, false},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: "res-1"}, true},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetPrompt, PromptID: "global"}, true},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetPrompt, PromptID: "janes"}, false},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetCollection, CollectionID: "c"}, false},
		{knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: "kp"}, true},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, opens(context.Background(), tc.ref, bob), tc.ref.URN())
	}
	admin := knowledge.Caller{Email: "root@example.com", IsAdmin: true}
	assert.True(t, opens(context.Background(), cases[1].ref, admin), "an administrator opens every asset")

	none := pageReferenceOpener(Config{})
	assert.False(t, none(context.Background(), cases[4].ref, bob), "a deployment with no prompt store opens no prompt")
}
