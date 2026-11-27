package resourcewrite

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// MoveFolder renames a folder of one library, moving every resource beneath it
// in one transaction and aliasing each one's old address, as the folder route
// does (#2836: the attachment repair moves a message's folder to its new
// address).
func (w *Writer) MoveFolder(ctx context.Context, move resource.FolderRename, claims resource.Claims) error {
	if _, err := resource.MoveFolder(ctx, w.deps, &claims, move); err != nil {
		return fmt.Errorf("moving folder %s: %w", move.From, err)
	}
	return nil
}

// DeleteFolder removes an empty stored folder and the empty folders beneath
// it. A folder still holding a file is refused (#2836: a source's deleted
// attachments leave their folders behind otherwise).
func (w *Writer) DeleteFolder(ctx context.Context, f resource.NewFolder, claims resource.Claims) error {
	if err := resource.DeleteFolder(ctx, w.deps, &claims, f); err != nil {
		return fmt.Errorf("deleting folder %s: %w", f.Path, err)
	}
	return nil
}
