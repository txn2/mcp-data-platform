package scriptexec

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The managed-resource destination of platform.export (#1663): an output written
// as the file at a path in the platform's own library, created the first time the
// script writes that path and recorded as the next version of it every run after.
//
// What separates it from the portal destination is whose address it is. A portal
// output is identified by the output NAME, which belongs to the script: nothing
// else can name it, and nothing should. A library output is identified by its
// PATH, which a person browsing the library, a table registration, and a second
// script can all name -- so a script can be the thing that keeps one known file
// current, rather than the owner of a private series of assets.

// writeResource lands one output in the managed resource its key addresses.
func (w *outputWriter) writeResource(
	ctx context.Context, req scriptrun.ExportRequest, identity scriptrun.OutputIdentity, data []byte,
) (*scriptrun.ExportResult, script.RunOutput, error) {
	if w.deps.Lander == nil {
		return nil, script.RunOutput{}, fmt.Errorf("output %q cannot be written to %q: this deployment has no "+
			"managed-resource library, which needs a database and an S3 connection for resource storage",
			req.Name, req.Destination.Name)
	}
	path, filename, err := script.SplitLibraryKey(req.Key)
	if err != nil {
		return nil, script.RunOutput{}, fmt.Errorf("output %q cannot be written to %q: %w",
			req.Name, req.Destination.Name, err)
	}
	// The path, not the output name, is what identifies a library file, for the
	// reason an object key identifies a delivered one: two output names can be
	// given one path, and the second write would record itself as a version of
	// the first one's file while the run recorded both as written.
	if prior, taken := w.delivered[libraryAddress(req.Key)]; taken {
		return nil, script.RunOutput{}, fmt.Errorf("output %q would be written to %s in the library, where this "+
			"run already wrote %q; give it its own key", req.Name, req.Key, prior)
	}

	landing, err := w.deps.Lander.Land(ctx, toolkit.ResourceDestination{
		Path: path, Filename: filename,
		DisplayName:   req.Name,
		Description:   fmt.Sprintf("Output of the managed script %s. Each run records a new version.", w.script.Name),
		Tags:          []string{"script", w.script.Name},
		ChangeSummary: fmt.Sprintf("%s v%d, run %s", w.script.Name, w.run.Version, w.run.ID),
	}, bytes.NewReader(data), identity.ContentType, w.claims)
	if err != nil {
		return nil, script.RunOutput{}, fmt.Errorf("writing output %q to the library: %w", req.Name, err)
	}
	w.delivered[libraryAddress(req.Key)] = req.Name
	slog.Info("scripts: wrote an output to the managed-resource library",
		logKeyRunID, w.run.ID, "output", req.Name, "uri", landing.URI,
		"version", landing.Version, "created", landing.Created, "bytes", landing.SizeBytes)

	out := script.RunOutput{
		Name: req.Name, Destination: req.Destination.Name,
		Key:             req.Key,
		ResourceID:      landing.ResourceID,
		ResourceURI:     landing.URI,
		ResourceVersion: landing.Version,
		Format:          req.Format, RowCount: len(req.Rows), Document: req.Body != nil,
		Bytes:        len(data),
		TableChanges: landing.TableChanges,
	}
	return &scriptrun.ExportResult{
		Key:             req.Key,
		ResourceID:      landing.ResourceID,
		ResourceRef:     landing.Reference,
		ResourceURI:     landing.URI,
		ResourceVersion: landing.Version,
		Bytes:           len(data),
		TableChanges:    landing.TableChanges,
	}, out, nil
}

// libraryAddress identifies one library file within a run: the path it is filed
// at. The library itself is not part of it because a run's library outputs all
// land in the one the run acts for.
func libraryAddress(key string) string {
	return "library\x00" + key
}

// runClaims is the managed-resource identity a run's own writes are made under.
//
// The principal is the script, and the address is the person the run acts for,
// which is the version's AUTHOR rather than the script's current owner (#1419):
// the run presents the author's roles, so the person it files work under has to
// be the same one, or a transferred script would combine one person's authority
// with another's library.
//
// The roles are the author's, and admin is NOT asserted: that resolution belongs
// to the persona layer, and claiming it here would hand every run an authority
// nothing granted it. A run's library output lands in the author's own library,
// which needs no admin arm.
func runClaims(sc *script.Script, v *script.Version) resource.Claims {
	return resource.BuildClaims(sc.Principal(), sc.OwnerEmail, "", v.AuthorRoles, false).
		ActingFor(v.Author)
}
