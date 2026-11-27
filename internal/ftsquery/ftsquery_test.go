package ftsquery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForPicksTheRuleByRanking(t *testing.T) {
	if got, want := For("$2", true), "plainto_tsquery('english', $2)"; got != want {
		t.Fatalf("For(hybrid) = %q, want %q", got, want)
	}
	if got, want := For("?", false), "replace(plainto_tsquery('english', ?)::text, ' & ', ' | ')::tsquery"; got != want {
		t.Fatalf("For(lexical) = %q, want %q", got, want)
	}
}

// TestEverySearchBuildsItsQueryHere is the gate behind "one rule" (#2838): a
// search that spells plainto_tsquery itself is how nine searches came to
// require every word of an agent's sentence. The session search spells the
// any-word form out in one constant the statement gate prepares, and its own
// test holds it to AnyWord.
func TestEverySearchBuildsItsQueryHere(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := map[string]bool{
		filepath.Join("internal", "ftsquery", "ftsquery.go"):              true,
		filepath.Join("internal", "platform", "sessionview", "search.go"): true,
	}
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "ui", "build":
				return filepath.SkipDir
			}
			if rel == filepath.Join("test", "load") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowed[rel] {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // a file of this repository
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		if strings.Contains(string(data), "plainto_tsquery") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these files build a tsquery with plainto_tsquery instead of ftsquery.AllWords/AnyWord/For: %v", offenders)
	}
}
