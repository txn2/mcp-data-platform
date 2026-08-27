//go:build integration

package resource_test

// The move taken end to end against a real database: the PATCH route a person
// presses Save on, the reference-serving route a reader's browser calls, and
// the prompt attachment resolver, over a PostgreSQL store.
//
// move_integration_test.go runs the same surfaces over a map. That answers
// whether they are wired to each other; it cannot answer whether the statements
// they issue are ones PostgreSQL will run, which is what #1506 was.

import (
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// newRealDBMovePlatform is newMovePlatform against Postgres.
func newRealDBMovePlatform(t *testing.T) *movePlatform {
	t.Helper()
	return newMovePlatformOn(t, resource.NewPostgresStore(testdb.New(t)))
}

// TestTheMoveRoute_RealDB_RefilesTheResource is the feature as a person
// performs it: one PATCH, and the file is the persona's.
func TestTheMoveRoute_RealDB_RefilesTheResource(t *testing.T) {
	p := newRealDBMovePlatform(t)

	rec := p.move(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("move: status %d, body %q", rec.Code, rec.Body.String())
	}

	moved := p.row(t)
	if moved.Scope != resource.ScopePersona || moved.ScopeID != "ops" {
		t.Errorf("the file is filed under %s/%s", moved.Scope, moved.ScopeID)
	}
	if moved.URI != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("the resource's own URI is %q", moved.URI)
	}
}

// TestTheMoveRoute_RealDB_AnAssetKeepsRenderingItsReference is the promise the
// move makes to everything that already points at the file.
func TestTheMoveRoute_RealDB_AnAssetKeepsRenderingItsReference(t *testing.T) {
	p := newRealDBMovePlatform(t)

	beforeCode, before := p.renderedReference(t)
	if beforeCode != http.StatusOK {
		t.Fatalf("before the move: status %d", beforeCode)
	}

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d, body %q", rec.Code, rec.Body.String())
	}

	afterCode, after := p.renderedReference(t)
	if afterCode != http.StatusOK || after != before {
		t.Errorf("after the move: status %d, body %q, want the same bytes as before", afterCode, after)
	}
}

// TestTheMoveRoute_RealDB_TheVacatedAddressStillResolves is the alias read that
// could not run before #1506: text citing the old address keeps resolving.
func TestTheMoveRoute_RealDB_TheVacatedAddressStillResolves(t *testing.T) {
	p := newRealDBMovePlatform(t)
	old := p.res.URI

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d, body %q", rec.Code, rec.Body.String())
	}

	got, err := p.store.GetByURI(t.Context(), old)
	if err != nil {
		t.Fatalf("the vacated address no longer resolves: %v", err)
	}
	if got.ID != p.res.ID {
		t.Errorf("the vacated address resolves to %q", got.ID)
	}
}

// TestTheMoveRoute_RealDB_APromptKeepsServingItsAttachment is the same promise
// for prompt attachments, which key on the resource id.
func TestTheMoveRoute_RealDB_APromptKeepsServingItsAttachment(t *testing.T) {
	p := newRealDBMovePlatform(t)

	before := p.servedAttachment(t)
	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d, body %q", rec.Code, rec.Body.String())
	}

	after := p.servedAttachment(t)
	if after.Availability != attachserve.AvailableEmbedded {
		t.Fatalf("the attachment resolved as %q", after.Availability)
	}
	if after.Text != before.Text {
		t.Errorf("the attachment served %q, want the same material as before", after.Text)
	}
	if after.URI != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("the attachment reports the address %q, want the one the file now holds", after.URI)
	}
}
