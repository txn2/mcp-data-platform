package platform

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This test enforces that every platform-config YAML example in the docs stays
// in sync with what the code actually parses. It is the CI gate that keeps the
// class of drift fixed in #757/#769/#770/#775 from recurring:
//
//   - Phantom keys (keys no typed Config field reads) fail detectUnknownFields.
//   - Toolkit blocks that omit the enabled/instances nesting fail the shape
//     check (a bare instance name directly under a kind never loads).
//
// A block is treated as a platform-config example when it has at least one
// top-level key and every top-level key maps to a known Config field. That
// naturally excludes Kubernetes manifests, docker-compose, and CI YAML (whose
// top-level keys are not Config fields) and indented fragments (no top-level
// key). To deliberately exclude a config-shaped block, put an HTML comment
// containing "config-test: skip" on the line immediately before the fence.

// reservedToolkitKeys are the only keys the registry loader reads directly
// under a toolkit kind (pkg/registry/loader.go). Any other direct child is a
// misplaced instance name that silently never loads.
var reservedToolkitKeys = map[string]bool{
	"enabled":   true,
	"instances": true,
	"default":   true,
	"config":    true,
}

var (
	fencedYAMLRe = regexp.MustCompile("(?s)```ya?ml[^\n]*\n(.*?)\n```")
	// Top-level keys may legitimately contain '-' and '.' (e.g. Kubernetes
	// `runs-on:`, docker-compose `x-anchors:`). Capturing them ensures such a
	// key marks its block as non-config so we don't misclassify and flag it.
	topKeyRe = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+):`)
)

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	// file = <root>/pkg/platform/config_docyaml_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// knownConfigTopKeys reflects the Config struct's yaml tags so the known-key
// set stays authoritative as fields are added or removed. Inline fields are
// excluded because they absorb arbitrary keys.
func knownConfigTopKeys() map[string]bool {
	keys := make(map[string]bool)
	for _, f := range reflect.VisibleFields(reflect.TypeFor[Config]()) {
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" || strings.Contains(tag, "inline") {
			continue
		}
		keys[name] = true
	}
	return keys
}

func docFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	// README at the repo root.
	if _, err := os.Stat(filepath.Join(root, "README.md")); err == nil {
		files = append(files, filepath.Join(root, "README.md"))
	}
	// Everything under docs/ that can carry fenced YAML.
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".txt") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs: %v", err)
	}
	return files
}

// topLevelKeys returns the set of column-0 keys in a YAML block.
func topLevelKeys(block string) []string {
	matches := topKeyRe.FindAllStringSubmatch(block, -1)
	keys := make([]string, 0, len(matches))
	for _, m := range matches {
		keys = append(keys, m[1])
	}
	return keys
}

// blockTargetsOtherSchema reports whether a config block declares an
// apiVersion other than the current one — a legacy version handled by a
// converter, or an unsupported/future version. Such blocks illustrate a
// different schema and must not be validated against the current Config
// (LoadConfigFromBytes would reject an unknown apiVersion outright).
func blockTargetsOtherSchema(block string) bool {
	version := PeekVersion([]byte(block))
	if version == "" {
		return false // no apiVersion → current schema
	}
	info, err := resolveVersion(DefaultRegistry(), version)
	return err != nil || info.Converter != nil
}

func isPlatformConfigBlock(block string, known map[string]bool) bool {
	keys := topLevelKeys(block)
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys {
		if !known[k] {
			return false
		}
	}
	return true
}

// checkToolkitShape asserts no toolkit kind carries a bare instance name as a
// direct child (the #775 defect). Returns a human-readable problem or "".
func checkToolkitShape(block string) string {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(block), &doc); err != nil {
		return "" // parse errors are reported by the strict/load checks
	}
	toolkits, ok := doc["toolkits"].(map[string]any)
	if !ok {
		return ""
	}
	for kind, v := range toolkits {
		kindMap, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for key := range kindMap {
			if !reservedToolkitKeys[key] {
				return "toolkit kind " + kind + " has bare child " + key +
					" (instance configs must be nested under instances:)"
			}
		}
	}
	return ""
}

func TestDocsYAMLExamplesMatchConfig(t *testing.T) {
	root := repoRootFromTest(t)
	known := knownConfigTopKeys()

	for _, f := range docFiles(t, root) {
		data, err := os.ReadFile(f) //nolint:gosec // test reads project docs
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		rel, _ := filepath.Rel(root, f)
		content := string(data)

		for _, m := range fencedYAMLRe.FindAllStringSubmatchIndex(content, -1) {
			blockStart, blockEnd := m[2], m[3]
			block := content[blockStart:blockEnd]

			// Honor an explicit skip marker on the preceding lines.
			preceding := content[:m[0]]
			if lastLine := lastNonEmptyLine(preceding); strings.Contains(lastLine, "config-test: skip") {
				continue
			}

			if !isPlatformConfigBlock(block, known) {
				continue
			}
			if blockTargetsOtherSchema(block) {
				continue
			}

			expanded := []byte(expandEnvVars(block))
			if unknown := detectUnknownFields(expanded); len(unknown) > 0 {
				t.Errorf("%s: config example has unrecognized keys: %s\n---\n%s\n---",
					rel, strings.Join(unknown, "; "), block)
			}
			if _, err := LoadConfigFromBytes(expanded); err != nil {
				t.Errorf("%s: config example fails to load: %v\n---\n%s\n---", rel, err, block)
			}
			if problem := checkToolkitShape(block); problem != "" {
				t.Errorf("%s: %s\n---\n%s\n---", rel, problem, block)
			}
		}
	}
}

// TestShippedExampleConfigStrictClean asserts the canonical example config
// (configs/platform.yaml) carries no unrecognized top-level keys and loads.
// This is the config a new operator copies first, so it must be exemplary.
func TestShippedExampleConfigStrictClean(t *testing.T) {
	root := repoRootFromTest(t)
	data, err := os.ReadFile(filepath.Join(root, "configs", "platform.yaml")) //nolint:gosec // test reads project example
	if err != nil {
		t.Fatalf("reading configs/platform.yaml: %v", err)
	}
	expanded := []byte(expandEnvVars(string(data)))
	if unknown := detectUnknownFields(expanded); len(unknown) > 0 {
		t.Errorf("configs/platform.yaml has unrecognized keys: %s", strings.Join(unknown, "; "))
	}
	if _, err := LoadConfigFromBytes(expanded); err != nil {
		t.Errorf("configs/platform.yaml fails to load: %v", err)
	}
}

// TestBlockTargetsOtherSchema guards the doc test against hard-failing on a
// config-shaped example that declares a non-current apiVersion.
func TestBlockTargetsOtherSchema(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  bool
	}{
		{"no apiVersion is current schema", "server:\n  name: p\n", false},
		{"explicit v1 is current schema", "apiVersion: v1\nserver:\n  name: p\n", false},
		{"unsupported future version skipped", "apiVersion: v99\nserver:\n  name: p\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := blockTargetsOtherSchema(c.block); got != c.want {
				t.Fatalf("blockTargetsOtherSchema = %v, want %v", got, c.want)
			}
		})
	}
}

// TestIsPlatformConfigBlock_HyphenatedForeignKey ensures a block that mixes a
// config-known key with a hyphenated non-config key is NOT misclassified as a
// platform config (which would flag the hyphenated key and break CI).
func TestIsPlatformConfigBlock_HyphenatedForeignKey(t *testing.T) {
	known := knownConfigTopKeys()
	// `runs-on:` is a GitHub Actions key; the regex must capture it so the
	// block is recognized as non-config.
	block := "server:\n  name: p\nruns-on: ubuntu-latest\n"
	if isPlatformConfigBlock(block, known) {
		t.Fatal("block with a hyphenated foreign key must not be treated as platform config")
	}
	// A pure-config block is still recognized.
	if !isPlatformConfigBlock("server:\n  name: p\naudit:\n  enabled: true\n", known) {
		t.Fatal("pure config block should be recognized")
	}
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
