package scriptsave

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// checkLibraryReach returns why a source's "resources" exports refuse the save,
// or "": one names a library the person saving it may not write (#2080).
//
// A run of the version presents the saver's roles, so this is the check the
// run's own write makes, made before there is a run: the resource rule
// (resource.CanWriteScope) against the saver's persona and roles. A run whose
// author has since lost the authority is refused again when it writes.
//
// The global library takes a saver who holds a write grant on one of its
// folders (#2684): the folder a run writes is its key, known only when it
// writes, so the write is checked against the grant then.
func checkLibraryReach(ctx context.Context, req Request, source string) string {
	report := scriptrun.Validate(source)
	claims := callerClaims(req.Caller).WithFolderGrants(ctx)
	var refused []string
	for _, lib := range report.ResourceLibraries {
		if lib.Own() {
			continue
		}
		scope := resource.Scope(lib.Scope)
		if resource.CanWriteScope(claims, scope, lib.ScopeID) {
			continue
		}
		if scope == resource.ScopeGlobal && len(claims.GrantedFolders()) > 0 {
			continue
		}
		refused = append(refused, lib.Label())
	}
	if len(refused) == 0 {
		return ""
	}
	return fmt.Sprintf("the script exports to %s, which %s may not write to, so the source was not saved: "+
		"a run presents the roles of the person who saved it, and writes only where they may",
		strings.Join(refused, " and "), cmp.Or(req.Caller.Email, "the person saving it"))
}
