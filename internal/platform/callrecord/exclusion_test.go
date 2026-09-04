package callrecord

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPersonaExclusionMatchesTheNamesItWasGiven(t *testing.T) {
	t.Parallel()

	e := NewPersonaExclusion([]string{"ingest-service", "etl"})
	assert.True(t, e.Excludes("ingest-service"))
	assert.True(t, e.Excludes("etl"))
	assert.False(t, e.Excludes("data-engineer"))
}

func TestPersonaExclusionIsForgivingAboutCaseAndSpace(t *testing.T) {
	t.Parallel()

	// An operator naming a persona here is naming it from memory of the
	// personas block, so the fold applies to both sides of the comparison.
	e := NewPersonaExclusion([]string{"  Ingest-Service  "})
	assert.True(t, e.Excludes("ingest-service"))
	assert.True(t, e.Excludes("INGEST-SERVICE"))
	assert.True(t, e.Excludes(" ingest-service "))
}

func TestPersonaExclusionDropsAnEmptyName(t *testing.T) {
	t.Parallel()

	// Kept, an empty name would exclude every call made without a persona at
	// all, which is the opposite of naming one.
	e := NewPersonaExclusion([]string{"", "   ", "ingest-service"})
	assert.False(t, e.Excludes(""))
	assert.False(t, e.Excludes("   "))
	assert.Equal(t, []string{"ingest-service"}, e.Personas())
}

func TestPersonaExclusionExcludesNothingByDefault(t *testing.T) {
	t.Parallel()

	// The zero value and an empty configuration are the same deployment: the
	// one that catalogs every call, exactly as before this existed.
	for name, e := range map[string]PersonaExclusion{
		"zero value": {},
		"no names":   NewPersonaExclusion(nil),
		"empty name": NewPersonaExclusion([]string{" "}),
	} {
		assert.False(t, e.Excludes("data-engineer"), name)
		assert.False(t, e.Excludes(""), name)
		assert.Empty(t, e.Personas(), name)
	}
}

func TestPersonaExclusionNormalizesForTheSweep(t *testing.T) {
	t.Parallel()

	// Sorted and deduplicated: the sweep binds this array on every replica,
	// and one name written twice is one name.
	e := NewPersonaExclusion([]string{"zeta", "Alpha", "alpha", " ZETA "})
	assert.Equal(t, []string{"alpha", "zeta"}, e.Personas())
}

func TestPersonaExclusionHandsOutACopy(t *testing.T) {
	t.Parallel()

	// The names go to a database driver, and the rule is not the driver's to
	// alter.
	e := NewPersonaExclusion([]string{"ingest-service"})
	e.Personas()[0] = "everything"
	assert.Equal(t, []string{"ingest-service"}, e.Personas())
}

func TestNormalizePersonaIsTheFoldEverySideUses(t *testing.T) {
	t.Parallel()

	// The startup check asks this the same way the rule does, so a name the
	// exclusion accepts is never reported as one it does not.
	assert.Equal(t, "ingest-service", normalizePersona("  Ingest-Service  "))
	assert.Equal(t, "", normalizePersona("   "))
	assert.Equal(t, normalizePersona("INGEST"), normalizePersona("ingest"))
}

func TestWarnUnknownExcludedNamesOnlyWhatMatchesNothing(t *testing.T) {
	known := []string{"Ingest-Service", "analyst"}
	tests := map[string]struct {
		configured []string
		warned     []string
	}{
		"a known persona":       {configured: []string{"ingest-service"}},
		"a known persona cased": {configured: []string{"INGEST-SERVICE"}},
		"nothing configured":    {configured: nil},
		"a misspelled name":     {configured: []string{"ingset-service"}, warned: []string{"ingset-service"}},
		"one of each":           {configured: []string{"analyst", "etl"}, warned: []string{"etl"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			defer slog.SetDefault(prev)

			WarnUnknownExcluded(tc.configured, known)

			logged := buf.String()
			assert.Equal(t, len(tc.warned), strings.Count(logged, "unknown persona"), logged)
			for _, want := range tc.warned {
				assert.Contains(t, logged, want)
			}
		})
	}
}

func TestWarnUnknownExcludedSanitizesTheNameItLogs(t *testing.T) {
	// The name comes from configuration and reaches a log sink, so it is
	// sanitized like every other caller-supplied value the platform logs.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	WarnUnknownExcluded([]string{"etl\nlevel=ERROR msg=forged"}, []string{"analyst"})

	assert.NotContains(t, buf.String(), "\nlevel=ERROR")
}
