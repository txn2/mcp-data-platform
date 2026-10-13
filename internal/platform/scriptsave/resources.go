package scriptsave

import (
	"context"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptres"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// pinResources resolves every res: load of source under the saver's access
// and records the version of each file it reads, which the version the save
// creates keeps (#2113). It returns why a load refuses the save, or "".
func (g *Gate) pinResources(ctx context.Context, req Request, source string, res *Result) string {
	pins, problems := scriptres.Pin(ctx, g.Resources, source, callerClaims(req.Caller))
	if len(problems) > 0 {
		return "a load names a file that cannot be read, so the source was not saved: " + strings.Join(problems, " | ")
	}
	res.ResourcePins = pins
	return ""
}

// libraries is where a test or a replay at the save reads its loads: the
// library versions, and each file as it is now under the saver's access,
// which is the version the save pins.
func (g *Gate) libraries(caller Caller) scriptlib.Source {
	claims := callerClaims(caller)
	return scriptres.With(g.Libraries, g.Resources, nil, &claims)
}

// callerClaims is the saver as the managed-resource rules read them: a run of
// the version presents the saver's roles.
func callerClaims(c Caller) resource.Claims {
	return resource.BuildClaims(c.Email, c.Email, c.Persona, c.Roles, c.Admin)
}
