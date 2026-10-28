package whauth

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Verify reports whether req is authentic for src at now, compiling the source
// on every call. The receiver compiles once and calls the Verifier; the tests
// here judge one request per source and read better through this.
func Verify(src whsource.Source, req Request, now time.Time) error {
	v, err := Compile(src)
	if err != nil {
		return ErrBadSignature
	}
	return v.Verify(req, now)
}
