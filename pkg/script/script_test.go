package script_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"ok", "daily-sales_1", ""},
		{"empty", "", "name is required"},
		{"uppercase", "DailySales", "lowercase"},
		{"leading hyphen", "-daily", "lowercase"},
		{"space", "daily sales", "lowercase"},
		{"too long", string(make([]byte, 200)), "at most"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := script.ValidateName(tc.input)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidateSourceAndTags(t *testing.T) {
	assert.ErrorContains(t, script.ValidateSource(""), "required")
	assert.NoError(t, script.ValidateSource("x = 1"))

	oversize := make([]byte, script.MaxSourceBytes+1)
	assert.ErrorContains(t, script.ValidateSource(string(oversize)), "over the")

	assert.NoError(t, script.ValidateTags([]string{"a", "b"}))
	assert.ErrorContains(t, script.ValidateTags(make([]string, 21)), "too many tags")
	assert.ErrorContains(t, script.ValidateTags([]string{string(make([]byte, 200))}), "exceeds")
}

func TestValidateScopeAndStatus(t *testing.T) {
	assert.NoError(t, script.ValidateScope(script.ScopeGlobal))
	assert.ErrorContains(t, script.ValidateScope("team"), "invalid scope")
	assert.NoError(t, script.ValidateStatus(script.StatusActive))
	assert.ErrorContains(t, script.ValidateStatus("retired"), "invalid status")
	assert.NoError(t, script.ValidateStatusTransition(script.StatusDraft, script.StatusDeprecated))
	assert.ErrorContains(t, script.ValidateStatusTransition(script.StatusSuperseded, script.StatusActive), "invalid status transition")
}

// TestValidate_WholeRecord proves the record-level check catches the
// combination a per-field check misses: a persona-scoped script naming no
// persona is invalid even though every individual field is fine.
func TestValidate_WholeRecord(t *testing.T) {
	base := func() *script.Script {
		return &script.Script{
			Name: "daily", Scope: script.ScopePersonal, Source: "x = 1",
			Status: script.StatusDraft,
		}
	}
	assert.NoError(t, base().Validate())

	personaNoPersona := base()
	personaNoPersona.Scope = script.ScopePersona
	assert.ErrorContains(t, personaNoPersona.Validate(), "at least one persona")

	withPersona := base()
	withPersona.Scope = script.ScopePersona
	withPersona.Personas = []string{"analyst"}
	assert.NoError(t, withPersona.Validate())

	refusals := []struct {
		name    string
		mutate  func(*script.Script)
		wantErr string
	}{
		{"name", func(s *script.Script) { s.Name = "" }, "name is required"},
		{"scope", func(s *script.Script) { s.Scope = "team" }, "invalid scope"},
		{"source", func(s *script.Script) { s.Source = "" }, "source is required"},
		{"params", func(s *script.Script) { s.Params = []script.Param{{Name: "Day", Type: script.ParamTypeDate}} }, "lowercase letter"},
		{"tags", func(s *script.Script) { s.Tags = make([]string, 21) }, "too many tags"},
		{"status", func(s *script.Script) { s.Status = "retired" }, "invalid status"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			sc := base()
			tc.mutate(sc)
			assert.ErrorContains(t, sc.Validate(), tc.wantErr)
		})
	}
}

func TestExecutable(t *testing.T) {
	sc := &script.Script{}
	assert.False(t, sc.Executable())
	sc.ApprovedVersionID = "sver_1"
	assert.True(t, sc.Executable())
}

// TestApplyStatusTransition_ActivationNeedsAnApprovedVersion pins the rule that
// keeps status a report of the execution gate rather than a way around it.
func TestApplyStatusTransition_ActivationNeedsAnApprovedVersion(t *testing.T) {
	now := time.Now().UTC()

	sc := &script.Script{Status: script.StatusDraft}
	assert.ErrorContains(t, sc.ApplyStatusTransition(script.StatusActive, "", now),
		"no approved version")
	assert.Equal(t, script.StatusDraft, sc.Status, "a refused transition must not land")

	sc.ApprovedVersionID = "sver_1"
	require.NoError(t, sc.ApplyStatusTransition(script.StatusActive, "", now))
	assert.Equal(t, script.StatusActive, sc.Status)
}

func TestApplyStatusTransition_LifecycleStamps(t *testing.T) {
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	sc := &script.Script{Status: script.StatusDraft}
	require.NoError(t, sc.ApplyStatusTransition(script.StatusDeprecated, "", now))
	require.NotNil(t, sc.DeprecatedAt)
	assert.Equal(t, now, *sc.DeprecatedAt)

	// Reactivating clears the deprecation stamp, so a reactivated script does
	// not read as both active and deprecated.
	sc.ApprovedVersionID = "sver_1"
	require.NoError(t, sc.ApplyStatusTransition(script.StatusActive, "", now))
	assert.Nil(t, sc.DeprecatedAt)

	require.NoError(t, sc.ApplyStatusTransition(script.StatusSuperseded, "daily-v2", now))
	assert.Equal(t, "daily-v2", sc.SupersededBy)

	// No-ops and unknown values.
	assert.NoError(t, sc.ApplyStatusTransition("", "", now))
	assert.NoError(t, sc.ApplyStatusTransition(script.StatusSuperseded, "", now))
	assert.ErrorContains(t, sc.ApplyStatusTransition("retired", "", now), "invalid status")
}

// TestVisibleTo is the one definition of script visibility, shared by the read
// path and the list predicate so a script can never be listable but unreadable.
func TestVisibleTo(t *testing.T) {
	cases := []struct {
		name    string
		sc      script.Script
		email   string
		persona string
		want    bool
	}{
		{"global to anyone", script.Script{Scope: script.ScopeGlobal}, "bob@example.com", "analyst", true},
		{"global with no persona", script.Script{Scope: script.ScopeGlobal}, "bob@example.com", "", true},
		{"persona to a holder", script.Script{Scope: script.ScopePersona, Personas: []string{"analyst", "eng"}}, "bob@example.com", "analyst", true},
		{"persona to a non-holder", script.Script{Scope: script.ScopePersona, Personas: []string{"eng"}}, "bob@example.com", "analyst", false},
		{"persona with no persona at all", script.Script{Scope: script.ScopePersona, Personas: []string{"eng"}}, "bob@example.com", "", false},
		{"personal to its owner", script.Script{Scope: script.ScopePersonal, OwnerEmail: "bob@example.com"}, "bob@example.com", "analyst", true},
		{"personal to anyone else", script.Script{Scope: script.ScopePersonal, OwnerEmail: "jane@example.com"}, "bob@example.com", "analyst", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.sc.VisibleTo(tc.email, tc.persona))
		})
	}
}
