package resourcewrite_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// TestMoveFolderAndDeleteFolder: the writer moves a folder under the caller's
// authority, every file beneath it with it, and refuses what the caller may
// not do; a store that keeps no folders says so on delete (#2836).
func TestMoveFolderAndDeleteFolder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	lib := resource.ScopeFilter{Scope: resource.ScopeGlobal}
	for _, name := range []string{"a.pdf", "b.png"} {
		_, err := f.writer.Create(ctx, resource.NewResource{
			Scope: resource.ScopeGlobal, Path: "mail/2026-11-04/inbox-7-3", Filename: name, DisplayName: name,
			Tags: []string{}, Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
		}, admin())
		require.NoError(t, err)
	}
	require.NoError(t, f.writer.MoveFolder(ctx, resource.FolderRename{Library: lib, From: "mail/2026-11-04/inbox-7-3", To: "mail/2026-11-04/abc"}, admin()))
	listed, _, err := f.store.List(ctx, resource.Filter{Scopes: []resource.ScopeFilter{lib}, Path: "mail/2026-11-04/abc"})
	require.NoError(t, err)
	assert.Len(t, listed, 2)

	err = f.writer.MoveFolder(ctx, resource.FolderRename{Library: lib, From: "mail/2026-11-04/abc", To: "mail/x"}, analyst())
	require.Error(t, err, "a caller who cannot write the library cannot move its folders")
	assert.Contains(t, err.Error(), "moving folder mail/2026-11-04/abc")

	err = f.writer.DeleteFolder(ctx, resource.NewFolder{Library: lib, Path: "mail"}, admin())
	require.ErrorIs(t, err, resource.ErrFoldersUnsupported)
	assert.Contains(t, err.Error(), "deleting folder mail")
}
