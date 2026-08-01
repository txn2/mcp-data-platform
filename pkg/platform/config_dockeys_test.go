package platform

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This test closes the documentation-coverage gap fixed in #1084: every
// yaml-tagged field reachable from the root Config must be named by at least one
// page under docs/, so adding a config key without documenting it fails the
// build rather than surfacing as a review finding two releases later.
//
// A key counts as documented when a page names it in a code span
// (`resources.managed.max_versions`) or writes it as a YAML key in an example.
// docs/llms.txt and docs/llms-full.txt deliberately do NOT count: they are
// generated from the prose pages, so a key documented only there is documented
// nowhere a reader goes.

// undocumentedConfigKeys allowlists yaml-tagged config keys deliberately absent
// from docs/, each with the reason. Anything not listed must be documented.
// Empty today: every reachable key is documented. Entries belong here only for
// keys a reader should never set, such as a deprecated alias kept for loading.
var undocumentedConfigKeys = map[string]string{}

var (
	// docCodeSpanRe captures the contents of a markdown code span, which is how
	// the documentation names a config key.
	docCodeSpanRe = regexp.MustCompile("`([A-Za-z0-9_.<>|*\\[\\]-]+:?)`")
	// docYAMLKeyRe captures a key line in a fenced YAML example, including one
	// written as a list item (`- strip_prefix: ...`) or commented out.
	docYAMLKeyRe = regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?#?[ \t]*([a-z0-9_]+):`)
)

// documentedKeyNames returns every identifier the prose documentation names,
// reduced to its final dotted segment so `resources.managed.max_versions`
// documents `max_versions`.
func documentedKeyNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	for _, f := range docFiles(t, root) {
		if !strings.HasSuffix(f, ".md") {
			continue
		}
		data, err := os.ReadFile(f) //nolint:gosec // test reads project docs
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		content := string(data)
		for _, m := range docCodeSpanRe.FindAllStringSubmatch(content, -1) {
			span := strings.TrimSuffix(m[1], ":")
			parts := strings.Split(span, ".")
			names[parts[len(parts)-1]] = true
		}
		for _, m := range docYAMLKeyRe.FindAllStringSubmatch(content, -1) {
			names[m[1]] = true
		}
	}
	return names
}

// configKeyPaths walks a struct type and records every yaml-tagged field as
// name -> dotted path. Pointer, slice and map element types are followed so keys
// nested under a map value (an MCP app's csp block) are reached.
func configKeyPaths(t reflect.Type, prefix string, seen map[reflect.Type]bool, out map[string]string) {
	t = derefType(t)
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	defer delete(seen, t)

	for f := range t.Fields() {
		tag := f.Tag.Get("yaml")
		if tag == "-" || (tag == "" && !f.Anonymous) {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if strings.Contains(tag, "inline") || name == "" {
			configKeyPaths(f.Type, prefix, seen, out)
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if _, ok := out[name]; !ok {
			out[name] = path
		}
		configKeyPaths(f.Type, path, seen, out)
	}
}

// derefType unwraps pointers, slices, arrays and maps to the element type, so a
// []PromptConfig or map[string]AppConfig still yields its fields.
func derefType(t reflect.Type) reflect.Type {
	for {
		switch t.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
		default:
			return t
		}
	}
}

// keyDocReport is the outcome of auditing config keys against the docs.
type keyDocReport struct {
	// Undocumented holds the dotted path of each key documented nowhere and not
	// allowlisted.
	Undocumented []string
	// StaleAllowlist holds allowlist entries that name no config field, are now
	// documented, or carry no justification.
	StaleAllowlist []string
}

// auditConfigKeys compares reachable config keys against the documented names
// and the allowlist. Kept pure so the drift rules are testable without the
// filesystem.
func auditConfigKeys(keys map[string]string, documented map[string]bool, allowlist map[string]string) keyDocReport {
	var report keyDocReport
	for name, path := range keys {
		if documented[name] {
			continue
		}
		if _, allowed := allowlist[name]; allowed {
			continue
		}
		report.Undocumented = append(report.Undocumented, path)
	}
	for name, reason := range allowlist {
		switch {
		case reason == "":
			report.StaleAllowlist = append(report.StaleAllowlist, name+" (no justification)")
		case keys[name] == "":
			report.StaleAllowlist = append(report.StaleAllowlist, name+" (names no config field)")
		case documented[name]:
			report.StaleAllowlist = append(report.StaleAllowlist, name+" (now documented)")
		}
	}
	sort.Strings(report.Undocumented)
	sort.Strings(report.StaleAllowlist)
	return report
}

// reachableConfigKeys returns every yaml key reachable from the root Config as
// name -> dotted path.
func reachableConfigKeys() map[string]string {
	keys := make(map[string]string)
	configKeyPaths(reflect.TypeFor[Config](), "", map[reflect.Type]bool{}, keys)
	return keys
}

// TestEveryConfigKeyIsDocumented fails when a yaml-tagged field reachable from
// the root Config is named by no page under docs/, and when an allowlist entry
// has gone stale.
func TestEveryConfigKeyIsDocumented(t *testing.T) {
	report := auditConfigKeys(reachableConfigKeys(),
		documentedKeyNames(t, repoRootFromTest(t)), undocumentedConfigKeys)

	for _, path := range report.Undocumented {
		t.Errorf("config key %s is documented nowhere under docs/: document it, or add it to undocumentedConfigKeys with a justification", path)
	}
	for _, entry := range report.StaleAllowlist {
		t.Errorf("undocumentedConfigKeys entry is stale: %s", entry)
	}
}

// TestAuditConfigKeys covers the drift rules themselves, including the allowlist
// paths that carry no entries today.
func TestAuditConfigKeys(t *testing.T) {
	keys := map[string]string{
		"documented":   "a.documented",
		"missing":      "b.missing",
		"deprecated":   "c.deprecated",
		"also_missing": "d.also_missing",
	}
	documented := map[string]bool{"documented": true, "gone": true}

	t.Run("undocumented keys are reported by path, sorted", func(t *testing.T) {
		got := auditConfigKeys(keys, documented, map[string]string{"deprecated": "legacy alias"})
		want := []string{"b.missing", "d.also_missing"}
		if len(got.Undocumented) != len(want) {
			t.Fatalf("Undocumented = %v, want %v", got.Undocumented, want)
		}
		for i := range want {
			if got.Undocumented[i] != want[i] {
				t.Errorf("Undocumented[%d] = %q, want %q", i, got.Undocumented[i], want[i])
			}
		}
		if len(got.StaleAllowlist) != 0 {
			t.Errorf("StaleAllowlist = %v, want none", got.StaleAllowlist)
		}
	})

	t.Run("allowlist entries must be live, justified and still undocumented", func(t *testing.T) {
		got := auditConfigKeys(keys, documented, map[string]string{
			"deprecated": "",           // no justification
			"gone":       "removed",    // documented now
			"phantom":    "never real", // names no field
		})
		if len(got.StaleAllowlist) != 3 {
			t.Fatalf("StaleAllowlist = %v, want three entries", got.StaleAllowlist)
		}
	})
}
