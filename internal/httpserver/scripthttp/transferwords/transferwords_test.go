package transferwords

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/producedview"
)

func TestCreated_IsTheLiveAssetsAndCollectionsTheRunsMade(t *testing.T) {
	items := []producedview.Item{
		{TargetKind: producedview.TargetAsset, TargetID: "a1", Created: true},
		{TargetKind: producedview.TargetCollection, TargetID: "c1", Created: true},
		{TargetKind: producedview.TargetAsset, TargetID: "a2"},
		{TargetKind: producedview.TargetAsset, TargetID: "a3", Created: true, Deleted: true},
		{TargetKind: "resource", TargetID: "r1", Created: true},
	}
	got := Created(items)
	assert.Len(t, got, 2)
	assert.Equal(t, "1 asset and 1 collection", Count(got))
	assert.Equal(t, "2 assets", Counts(2, 0))
	assert.Equal(t, "3 collections", Counts(0, 3))
}

func TestKeptWithAndSameAddress(t *testing.T) {
	mine := []producedview.Item{{OwnerEmail: "Jane@Example.com"}}
	assert.Equal(t, "jane@example.com", KeptWith(mine, "jane@example.com"))
	mixed := []producedview.Item{{OwnerEmail: "Jane@Example.com"}, {OwnerEmail: "bob@example.com"}}
	assert.Equal(t, "their current owners", KeptWith(mixed, "jane@example.com"))
	assert.False(t, SameAddress("", ""), "an unattributed row names nobody")
}

func TestSentence(t *testing.T) {
	assert.Equal(t, " The 2 assets its runs wrote now belong to jane@example.com too.",
		Sentence(Account{Moved: true, Assets: 2}, "jane@example.com"))
	assert.Equal(t, " The 1 collection its runs wrote already belong to jane@example.com.",
		Sentence(Account{Collections: 1}, "jane@example.com"))
	assert.Contains(t, Sentence(Account{Assets: 1, KeptWith: []string{""}}, "jane@example.com"), "stay with nobody")
}

func TestSentence_KeptOwners(t *testing.T) {
	one := Sentence(Account{Collections: 3, KeptWith: []string{"carol@example.com", "Carol@Example.com"}}, "jane@example.com")
	assert.Contains(t, one, "stay with carol@example.com.", "one address in two spellings is one owner")
	several := Sentence(Account{Assets: 2, KeptWith: []string{"carol@example.com", "bob@example.com"}}, "jane@example.com")
	assert.Contains(t, several, "stay with their current owners.")
}
