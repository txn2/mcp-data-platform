package configwarn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

// TestCheckPendingRenames pins #2608: before 000239 runs, each site whose dev
// environment has a domain is named in a configuration warning with its
// domains and the name the environment is given, and counted; a failed read is
// logged, never startup's failure.
func TestCheckPendingRenames(t *testing.T) {
	ctx := context.Background()
	scrape := withMetrics(t)
	logs := captureLog(t)
	checkPendingRenames(ctx, func() ([]migrate.DevRename, error) {
		return []migrate.DevRename{
			{Site: "docs", Domains: "dev.example.com", NewName: "development"},
			{Site: "shop", Domains: "a.example.com, b.example.com", NewName: "dev-previous"},
		}, nil
	})
	out := logs.String()
	for _, want := range []string{
		"code=site_environment_renamed", "site=docs", "renamed_to=development",
		`domains="a.example.com, b.example.com"`, "renamed_to=dev-previous", "An environment cannot be renamed back",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(scrape(), `config_validation_warnings_total{code="site_environment_renamed"} 2`) {
		t.Error("the two warnings are not counted")
	}

	logs.Reset()
	checkPendingRenames(ctx, func() ([]migrate.DevRename, error) { return nil, errors.New("db down") })
	if out := logs.String(); !strings.Contains(out, "reading what migration 000239 renames failed") || strings.Contains(out, "code=site_environment_renamed") {
		t.Errorf("a failed read logs %q", out)
	}
}

// A database whose migration version cannot be read is warned of nothing.
func TestCheckPendingMigrations_AnUnreadableDatabaseWarnsOfNothing(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logs := captureLog(t)
	CheckPendingMigrations(context.Background(), db)
	if strings.Contains(logs.String(), "site_environment_renamed") {
		t.Errorf("warned of a rename on a database it could not read: %s", logs.String())
	}
}
