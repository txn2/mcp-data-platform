package sourcerule

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type fakeSources map[string]whsource.Source

func (f fakeSources) Get(_ context.Context, name string) (whsource.Source, error) {
	if name == "broken" {
		return whsource.Source{}, errors.New("db down")
	}
	s, ok := f[name]
	if !ok {
		return whsource.Source{}, whsource.ErrNotFound
	}
	return s, nil
}

func rule() *Rule {
	return &Rule{
		Sources: fakeSources{
			"shop":         {Name: "shop"},
			"form-contact": {Name: "form-contact", Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_contact"}}},
			"form-other":   {Name: "form-other", Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_broken"}}},
		},
		FormOwner: func(_ context.Context, id string) (string, error) {
			if id == "scr_broken" {
				return "", errors.New("db down")
			}
			return "Jane@example.com", nil
		},
	}
}

// #2332: an administrator names any source; anyone else only the source of
// a form they own.
func TestRule(t *testing.T) {
	ctx := context.Background()
	r := rule()
	require.NoError(t, r.Check(ctx, "shop", "admin@example.com", true))
	require.NoError(t, r.Check(ctx, "form-contact", "jane@example.com", false), "the form's owner")

	require.ErrorIs(t, r.Check(ctx, "shop", "jane@example.com", false), ErrSourceRefused)
	err := r.Check(ctx, "form-contact", "bo@example.com", false)
	require.ErrorIs(t, err, ErrSourceRefused)
	assert.Contains(t, err.Error(), "a form you do not own")
	require.ErrorContains(t, r.Check(ctx, "missing", "admin@example.com", true), `no webhook source is named "missing"`)
	err = r.Check(ctx, "broken", "admin@example.com", true)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSourceRefused, "a read that failed is not a refusal")
	require.ErrorContains(t, r.Check(ctx, "form-other", "jane@example.com", false), "reading the form source's script")
	require.ErrorIs(t, (&Rule{}).Check(ctx, "shop", "admin@example.com", true), ErrSourceRefused)
	require.ErrorIs(t, (&Rule{Sources: r.Sources}).Check(ctx, "form-contact", "jane@example.com", false), ErrSourceRefused)
}

// A webhook handler names one source (#2434): an empty one is refused, by
// anyone, naming the setting; and a deployment with no rule (no database,
// so no webhook sources) refuses every source rather than panicking.
func TestRule_RefusesNoSourceAndNoRule(t *testing.T) {
	ctx := context.Background()
	for _, admin := range []bool{false, true} {
		err := rule().Check(ctx, "", "jane@example.com", admin)
		require.ErrorIs(t, err, ErrSourceRefused)
		assert.Contains(t, err.Error(), "event_source is required")
	}

	var none *Rule
	err := none.Check(ctx, "shop", "admin@example.com", true)
	require.ErrorIs(t, err, ErrSourceRefused)
	assert.Contains(t, err.Error(), "this deployment has no webhook sources")
	assert.Nil(t, NewRule(nil, nil))
}

func TestOwnerOf(t *testing.T) {
	owner := OwnerOf(func(_ context.Context, id string) (*script.Script, error) {
		switch id {
		case "gone":
			return nil, nil //nolint:nilnil // a store answers a missing script with neither

		case "broken":
			return nil, errors.New("db down")
		}
		return &script.Script{ID: id, OwnerEmail: "jane@example.com"}, nil
	})
	got, err := owner(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, "jane@example.com", got)
	got, err = owner(context.Background(), "gone")
	require.NoError(t, err)
	assert.Empty(t, got, "a script that does not exist owns nothing")
	_, err = owner(context.Background(), "broken")
	require.ErrorContains(t, err, "reading script broken")
}
