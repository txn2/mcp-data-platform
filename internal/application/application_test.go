package application

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	ok := Application{Name: "shop", OwnerEmail: "jane@example.com"}
	assert.NoError(t, ok.Validate())
	for name, a := range map[string]Application{
		"name":        {Name: "Shop", OwnerEmail: "j@e"},
		"description": {Name: "shop", OwnerEmail: "j@e", Description: string(make([]byte, MaxDescription+1))},
		"schema half": {Name: "shop", OwnerEmail: "j@e", Connection: "orders"},
		"owner":       {Name: "shop"},
		"part kind":   {Name: "shop", OwnerEmail: "j@e", Parts: []Part{{Kind: "table", ID: "x"}}},
	} {
		assert.Error(t, a.Validate(), name)
	}
}

func TestDiffAndPinOf(t *testing.T) {
	prev := []Pin{{Kind: KindScript, PartID: "a", Version: 1}, {Kind: KindAsset, PartID: "b", Version: 2}}
	next := []Pin{{Kind: KindScript, PartID: "a", Version: 3}, {Kind: KindScript, PartID: "c", Version: 1}}
	assert.Equal(t, []PinChange{
		{Kind: KindScript, PartID: "a", From: 1, To: 3},
		{Kind: KindScript, PartID: "c", From: 0, To: 1},
		{Kind: KindAsset, PartID: "b", From: 2, To: 0},
	}, Diff(prev, next))
	assert.Empty(t, Diff(prev, prev))
	r := &Release{Pins: next}
	assert.Equal(t, 3, r.PinOf(KindScript, "a"))
	assert.Zero(t, r.PinOf(KindAsset, "a"))
	var none *Release
	assert.Zero(t, none.PinOf(KindScript, "a"))
}

func TestMayEditAndKinds(t *testing.T) {
	a := Application{OwnerEmail: "jane@example.com"}
	assert.True(t, a.MayEdit(Actor{Email: "jane@example.com"}))
	assert.True(t, a.MayEdit(Actor{Admin: true}))
	assert.False(t, a.MayEdit(Actor{Email: "joe@example.com"}))
	assert.False(t, (&Application{}).MayEdit(Actor{}), "an unidentified caller edits no ownerless application")
	for _, k := range Kinds {
		assert.True(t, ValidKind(k))
	}
	assert.False(t, ValidKind("table"))
}

// TestSchemaPartID reads a schema part's id, "<connection>/<schema>" or the
// schema alone (#2539), and schema is a kind of part.
func TestSchemaPartID(t *testing.T) {
	assert.Equal(t, "orders/shop", SchemaPartID("orders", "shop"))
	conn, schema := ParseSchemaPartID(" orders / shop ")
	assert.Equal(t, [2]string{"orders", "shop"}, [2]string{conn, schema})
	conn, schema = ParseSchemaPartID("shop")
	assert.Equal(t, [2]string{"", "shop"}, [2]string{conn, schema})
	assert.True(t, ValidKind(KindSchema))
	assert.Contains(t, Kinds, KindSchema)
}
