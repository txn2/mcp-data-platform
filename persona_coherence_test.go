// This file adds the persona-coherence gate over the configuration and the
// persona examples this repo ships (issue #1174).
//
// It exists because the defect it catches shipped undetected: every persona
// example in this repo granted `search` and withheld `fetch`, so a deployment
// built from one could discover that an answer existed and never read it. No
// gate, test, log line, or startup check noticed, because the platform's
// graceful degradation is silent by design — the instruction baseline omits
// guidance for a tool the caller cannot reach (instructions.reuseBullet), which
// is correct and which also means the only symptom is an unauthorized audit row
// if an agent guesses the tool name unprompted.
//
// The runtime check is persona.CheckCoherence, wired into startup
// (Platform.validatePersonaCoherence) and the admin persona write path. This
// gate points the same rule table at the YAML this repo ships — config files
// through the real loader, documentation examples through their fenced blocks —
// so neither can regress into the shape the check exists to catch.
//
// Run: go test -run TestShippedPersonaCoherence .
package mcp_data_platform_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// coherenceConfigGlobs match the platform config files this gate loads through
// platform.LoadConfig.
//
// The benchmark arms are included. They are the reason the runtime check
// exists: every arm of both published studies granted `search` and denied
// `fetch`, so the studies measured search-only, single-hop delivery without
// saying so (19 fetch attempts across 4,173 archived transcripts, 19 denials).
// #1176 corrected the four search-granting arms and guards them with
// bench/config.TestSearchArmsGrantFetch, which additionally requires
// `list_connections` — a bench-specific delivery requirement rather than a
// platform coherence rule, and the reason that guard stays where it is. This
// gate is the complement: it evaluates all three rules through the real
// persona filter, so it also covers an arm that grants `memory_capture` or
// `apply_knowledge` and drops `search` — a shape the bench guard cannot see,
// because it only fires on arms that grant `search` in the first place.
//
// configs/examples/kubernetes/ is deliberately absent: those files are
// Kubernetes manifests, and the platform YAML they carry is the value of a
// ConfigMap `data:` key rather than a config the loader can read. They are
// covered by the fenced-example sweep below only if the YAML appears in
// documentation; in the manifests themselves the persona grants "*", which no
// rule can fire on.
var coherenceConfigGlobs = []string{
	"configs/*.yaml",
	"bench/config/*.yaml",
}

// coherenceDocRoots are the trees whose fenced YAML examples this gate parses.
var coherenceDocRoots = []string{"docs", "README.md", "CLAUDE.md"}

// coherenceRegisteredTools is the tool set a shipped example is judged against:
// every tool the coherence rules pair, as if registered.
//
// A live deployment scopes the check to what it actually registered, so a
// deployment with no search toolkit never warns about withholding `fetch`. An
// example has no deployment behind it, so the strict reading is the useful one:
// if this config ran on a fully featured platform, would its personas be
// coherent? An example that only holds together on a stripped deployment is not
// an example worth shipping.
var coherenceRegisteredTools = []string{
	"search", "fetch", "memory_capture", "apply_knowledge",
}

func TestShippedPersonaCoherence(t *testing.T) {
	configs := shippedConfigPaths(t)

	for _, path := range configs {
		t.Run(path, func(t *testing.T) {
			cfg, err := platform.LoadConfig(path)
			require.NoError(t, err, "loading %s", path)
			// Personas are the access boundary, so every shipped config
			// defines at least one. A config whose personas decoded empty
			// would pass every rule below without being read.
			require.NotEmpty(t, cfg.Personas.Definitions,
				"%s defines no personas — the gate would pass vacuously on it", path)
			reportCoherence(t, coherenceOf(t, cfg.Personas.Definitions))
		})
	}

	examples := fencedPersonaExamples(t)
	require.NotEmpty(t, examples, "no fenced persona example found under %v — the gate would pass vacuously", coherenceDocRoots)

	for _, ex := range examples {
		t.Run(ex.where, func(t *testing.T) {
			reportCoherence(t, ex.findings)
		})
	}
}

// TestShippedPersonaCoherenceDetectsBreakage is the negative control. Without
// it a bug in the config load or the fence walk (a glob that matches nothing, a
// persona map that decodes empty) would make the gate above pass on a repo full
// of broken personas, which is the exact failure mode this ticket exists to
// close.
func TestShippedPersonaCoherenceDetectsBreakage(t *testing.T) {
	const broken = `apiVersion: v1
personas:
  discoverer:
    display_name: "Discoverer"
    roles: ["discoverer"]
    tools:
      allow: ["search", "trino_*"]
  scribe:
    display_name: "Scribe"
    roles: ["scribe"]
    tools:
      allow: ["*"]
      deny: ["search"]
  whole:
    display_name: "Whole"
    roles: ["whole"]
    tools:
      allow: ["*"]
`

	path := filepath.Join(t.TempDir(), "broken.yaml")
	require.NoError(t, os.WriteFile(path, []byte(broken), 0o600))
	cfg, err := platform.LoadConfig(path)
	require.NoError(t, err)

	// Both parse paths the gate uses must catch the same breakage: the real
	// loader for config files, and the fragment decoder for doc fences.
	viaLoader := coherenceOf(t, cfg.Personas.Definitions)
	viaFence := coherenceOfFence(t, broken)

	for name, findings := range map[string][]persona.CoherenceFinding{"loader": viaLoader, "fence": viaFence} {
		t.Run(name, func(t *testing.T) {
			byPersona := map[string][]string{}
			for _, f := range findings {
				byPersona[f.Persona] = append(byPersona[f.Persona], f.Granted+" without "+f.Missing)
			}

			require.Equal(t, []string{"search without fetch"}, byPersona["discoverer"],
				"a persona granting search without fetch must be flagged")
			require.ElementsMatch(t,
				[]string{"apply_knowledge without search", "memory_capture without search"},
				byPersona["scribe"],
				"a persona writing knowledge it cannot retrieve must be flagged")
			require.NotContains(t, byPersona, "whole", "a persona granting everything must not be flagged")

			for _, f := range findings {
				require.NotEmpty(t, f.Why, "finding must say why the pair matters")
				require.Contains(t, f.Remedy, f.Missing, "remedy must name the missing tool")
				require.Contains(t, f.Remedy, f.Persona, "remedy must name the persona")
			}
		})
	}
}

// reportCoherence fails the test once per finding, printing the persona, the
// broken pair, the reason, and the fix.
func reportCoherence(t *testing.T, findings []persona.CoherenceFinding) {
	t.Helper()
	for _, f := range findings {
		t.Errorf("persona %q grants %q without %q: %s\n  remedy: %s",
			f.Persona, f.Granted, f.Missing, f.Why, f.Remedy)
	}
}

// coherenceOf registers the given persona definitions and evaluates the rule
// table over them.
func coherenceOf(t *testing.T, defs map[string]platform.PersonaDef) []persona.CoherenceFinding {
	t.Helper()

	reg := persona.NewRegistry()
	for name, def := range defs {
		require.NoError(t, reg.Register(&persona.Persona{
			Name: name,
			Tools: persona.ToolRules{
				Allow: def.Tools.Allow,
				Deny:  def.Tools.Deny,
			},
		}))
	}
	return persona.CheckRegistryCoherence(reg, coherenceRegisteredTools)
}

// personaFence is the shape a documentation fence is decoded into. It mirrors
// platform.PersonasConfig: an inline map of persona name to definition, with
// the two non-persona keys named explicitly so `role_mapping:` does not decode
// as a persona called "role_mapping".
//
// Fences are fragments, not config files — most carry no apiVersion and only
// the keys the surrounding prose is about — so they are decoded directly rather
// than through platform.LoadConfig, whose strict unknown-key check is scoped to
// whole configs.
type personaFence struct {
	Personas struct {
		Definitions map[string]struct {
			Tools struct {
				Allow []string `yaml:"allow"`
				Deny  []string `yaml:"deny"`
			} `yaml:"tools"`
		} `yaml:",inline"`
		DefaultPersona string    `yaml:"default_persona"`
		RoleMapping    yaml.Node `yaml:"role_mapping"`
	} `yaml:"personas"`
}

// coherenceOfFence decodes one fenced YAML block and evaluates the rule table
// over the personas it defines.
func coherenceOfFence(t *testing.T, fence string) []persona.CoherenceFinding {
	t.Helper()

	var doc personaFence
	require.NoError(t, yaml.Unmarshal([]byte(fence), &doc), "decoding fence:\n%s", fence)

	reg := persona.NewRegistry()
	for name, def := range doc.Personas.Definitions {
		require.NoError(t, reg.Register(&persona.Persona{
			Name: name,
			Tools: persona.ToolRules{
				Allow: def.Tools.Allow,
				Deny:  def.Tools.Deny,
			},
		}))
	}
	return persona.CheckRegistryCoherence(reg, coherenceRegisteredTools)
}

// fencedExample is one ```yaml block containing a `personas:` key, with the
// findings it produced.
type fencedExample struct {
	where    string
	findings []persona.CoherenceFinding
}

// yamlFencePattern captures the body of a fenced yaml block in markdown.
var yamlFencePattern = regexp.MustCompile("(?s)```ya?ml\n(.*?)\n```")

// fencedPersonaExamples walks the documentation trees and returns every fenced
// YAML block that defines personas, checked. Blocks without a top-level
// `personas:` key are skipped: they illustrate pattern syntax or a persona's
// context overrides in isolation, and carry no tool grant to judge.
func fencedPersonaExamples(t *testing.T) []fencedExample {
	t.Helper()

	var out []fencedExample
	for _, path := range markdownFiles(t) {
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from a walk of this repo
		require.NoError(t, err)

		for i, m := range yamlFencePattern.FindAllStringSubmatch(string(data), -1) {
			body := m[1]
			if !strings.Contains(body, "personas:") {
				continue
			}
			// A fence indented inside a list item or a ConfigMap `data:` value
			// is still valid YAML once its common indent is removed.
			body = dedent(body)
			var probe personaFence
			if err := yaml.Unmarshal([]byte(body), &probe); err != nil {
				// A fence that is not parseable YAML is prose about YAML
				// (an error message, a diff). Nothing to judge.
				continue
			}
			if len(probe.Personas.Definitions) == 0 {
				continue
			}
			out = append(out, fencedExample{
				where:    path + "#fence" + strconv.Itoa(i+1),
				findings: coherenceOfFence(t, body),
			})
		}
	}
	return out
}

// shippedConfigPaths expands coherenceConfigGlobs to a sorted list, failing
// when a glob matches nothing so a moved directory cannot quietly shrink the
// gate's coverage to whatever still happens to match.
func shippedConfigPaths(t *testing.T) []string {
	t.Helper()

	var paths []string
	for _, glob := range coherenceConfigGlobs {
		matches, err := filepath.Glob(glob)
		require.NoError(t, err, "bad glob %q", glob)
		require.NotEmpty(t, matches, "no config matched %q — the gate would pass vacuously", glob)
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	return paths
}

// markdownFiles returns every .md file under coherenceDocRoots, sorted.
func markdownFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	for _, root := range coherenceDocRoots {
		info, err := os.Stat(root)
		require.NoError(t, err, "doc root %q missing", root)
		if !info.IsDir() {
			files = append(files, root)
			continue
		}
		require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				files = append(files, path)
			}
			return nil
		}))
	}
	sort.Strings(files)
	return files
}

// dedent removes the longest common leading-space prefix from every non-blank
// line, so a fence indented under a list item or a ConfigMap key parses.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " "))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent <= 0 {
		return s
	}
	for i, line := range lines {
		if len(line) >= indent {
			lines[i] = line[indent:]
		}
	}
	return strings.Join(lines, "\n")
}
