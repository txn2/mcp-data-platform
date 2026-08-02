// Package config holds the benchmark arm configuration files. This test-only
// package guards structural invariants over them, in the same spirit as
// pool.TestArmConfigsDefineExactlySize.
package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// personaDef is the subset of a persona definition this guard reads.
type personaDef struct {
	Tools struct {
		Allow []string `yaml:"allow"`
	} `yaml:"tools"`
}

// armConfig is the subset of an arm config this guard reads. The personas map
// mixes persona definitions with the default_persona string, so values decode
// lazily per key.
type armConfig struct {
	Personas map[string]yaml.Node `yaml:"personas"`
}

// TestSearchArmsGrantFetch: any arm persona that grants search must also grant
// fetch and list_connections. search returns references, fetch is the only
// tool that dereferences them, and the platform's own not-found errors steer
// agents to list_connections by name — the three are one delivery surface. An
// allow-list with search but without fetch measures a delivery architecture
// the platform does not ship: every arm of both published studies ran that way
// (19 fetch attempts across 4,173 archived transcripts, 19 denials, #1176).
// This guard keeps the defect from returning in future arm configs.
func TestSearchArmsGrantFetch(t *testing.T) {
	t.Parallel()
	configs, err := filepath.Glob("platform.bench.*.yaml")
	if err != nil {
		t.Fatalf("glob arm configs: %v", err)
	}
	// A glob that matches nothing would pass every assertion below without
	// reading a single config.
	if len(configs) == 0 {
		t.Fatal("no platform.bench.*.yaml configs found")
	}

	searchArms := make(map[string]bool)
	for _, path := range configs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var cfg armConfig
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for name, node := range cfg.Personas {
			if name == "default_persona" {
				continue
			}
			var p personaDef
			if err := node.Decode(&p); err != nil {
				t.Fatalf("decode persona %q in %s: %v", name, path, err)
			}
			if !slices.Contains(p.Tools.Allow, "search") {
				continue
			}
			searchArms[filepath.Base(path)] = true
			for _, required := range []string{"fetch", "list_connections"} {
				if !slices.Contains(p.Tools.Allow, required) {
					t.Errorf("%s persona %q grants search but not %s; the arm would measure a delivery architecture the platform does not ship (#1176)", path, name, required)
				}
			}
		}
	}

	// The invariant is vacuous if the search-granting arms stop being
	// detected (a rename, a parse regression). Pin the four known ones.
	for _, want := range []string{
		"platform.bench.a2.yaml",
		"platform.bench.a3.yaml",
		"platform.bench.pk.yaml",
		"platform.bench.pk-gateoff.yaml",
	} {
		if !searchArms[want] {
			t.Errorf("%s was not detected as a search-granting arm; the invariant above no longer covers it", want)
		}
	}
}
