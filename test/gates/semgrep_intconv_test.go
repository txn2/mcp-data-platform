package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// intconvRule and intconvFixture are the rule #2425 added to the diff-scoped
// gate and the fixture make semgrep-diff holds it to.
const (
	intconvRule    = ".semgrep-diff/go-intconv.yml"
	intconvFixture = ".semgrep-diff/go-intconv.go"
)

// intconvBase is a pool file with its bound in a validator, the way
// pkg/toolkits/postgres/config.go held it on #2424.
const intconvBase = `package p

import "errors"

const maxConns = 100

type Config struct{ MaxConns int }

type pool struct{ MaxConns int32 }

func (c Config) Validate() error {
	if c.MaxConns < 1 || c.MaxConns > maxConns {
		return errors.New("max_conns out of range")
	}
	return nil
}
`

// pool2424 is the line CodeQL flagged on #2424: the bound is in Validate,
// which CodeQL does not follow.
const pool2424 = intconvBase + `
func poolOf(c Config) pool {
	pc := pool{}
	pc.MaxConns = int32(c.MaxConns) // #nosec G115 -- bounded by Validate
	return pc
}
`

// pool2424Fixed is the fix #2424 shipped: the check beside the conversion.
const pool2424Fixed = intconvBase + `
func poolOf(c Config) (pool, error) {
	if c.MaxConns < 1 || c.MaxConns > maxConns {
		return pool{}, errors.New("max_conns out of range")
	}
	pc := pool{}
	pc.MaxConns = int32(c.MaxConns)
	return pc, nil
}
`

// TestSemgrepDiffRefusesUnboundedNarrowing holds the #2425 rule to the #2424
// shape through the real gate script, passes the fix, and leaves a conversion
// on a line the branch did not change alone.
func TestSemgrepDiffRefusesUnboundedNarrowing(t *testing.T) {
	requireSemgrep(t)
	// #nosec G304 -- constant rule and fixture paths of this package.
	rule, err := os.ReadFile(filepath.Join("..", "..", intconvRule))
	require(t, err)
	// #nosec G304 -- constant rule and fixture paths of this package.
	fixture, err := os.ReadFile(filepath.Join("..", "..", intconvFixture))
	require(t, err)

	cases := []struct {
		name   string
		base   string
		change string
		expect expect
	}{
		{
			name: "the #2424 shape is refused", base: intconvBase, change: pool2424,
			expect: expect{want: []string{"[narrowing-int-conversion-unbounded]", "beside"}},
		},
		{
			name: "the fix #2424 shipped passes", base: intconvBase, change: pool2424Fixed,
			expect: expect{pass: true, mustNot: []string{"[narrowing-int-conversion-unbounded]"}},
		},
		{
			name: "an unchanged line holding the shape is not reported", base: pool2424,
			change: pool2424 + "\nfunc unrelated() {}\n",
			expect: expect{pass: true, mustNot: []string{"[narrowing-int-conversion-unbounded]"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newGitRepo(t, semgrepDiffScript, map[string]string{
				intconvRule:          string(rule),
				intconvFixture:       string(fixture),
				"internal/p/pool.go": tc.base,
			})
			writeFile(t, root, "internal/p/pool.go", tc.change)
			out, passed := runScript(t, root, []string{semgrepDiffScript, ".semgrep-diff/"}, nil)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}

// TestSemgrepDiffFailsARuleThatMissesItsFixture pins that the gate holds each
// rule to the fixture beside it before scanning: a rule edited so it no longer
// matches the shape it was written for fails make semgrep-diff, rather than
// passing every diff in silence. The fixture itself, which holds the refused
// shape on purpose, is never reported as a finding.
func TestSemgrepDiffFailsARuleThatMissesItsFixture(t *testing.T) {
	requireSemgrep(t)
	// #nosec G304 -- constant rule and fixture paths of this package.
	rule, err := os.ReadFile(filepath.Join("..", "..", intconvRule))
	require(t, err)
	// #nosec G304 -- constant rule and fixture paths of this package.
	fixture, err := os.ReadFile(filepath.Join("..", "..", intconvFixture))
	require(t, err)

	t.Run("the fixture as written passes and is not scanned", func(t *testing.T) {
		root := newGitRepo(t, semgrepDiffScript, map[string]string{intconvRule: string(rule)})
		writeFile(t, root, intconvFixture, string(fixture))
		runIn(t, root, "git", "add", "-A")
		out, passed := runScript(t, root, []string{semgrepDiffScript, ".semgrep-diff/"}, nil)
		assertOutput(t, out, passed, expect{pass: true, mustNot: []string{"go-intconv.go:"}})
	})
	t.Run("a rule that stopped matching fails", func(t *testing.T) {
		broken := strings.Replace(string(rule), "pattern: int32($X)", "pattern: int64($X)", 1)
		root := newGitRepo(t, semgrepDiffScript, map[string]string{
			intconvRule:    broken,
			intconvFixture: string(fixture),
		})
		out, passed := runScript(t, root, []string{semgrepDiffScript, ".semgrep-diff/"}, nil)
		assertOutput(t, out, passed, expect{want: []string{"does not match its fixture"}})
	})
}
