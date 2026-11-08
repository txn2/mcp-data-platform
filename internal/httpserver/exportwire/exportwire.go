// Package exportwire mounts the routes that export an asset beside the
// content route it is read through: the PDF the renderer prints (#1983) and
// the self-contained HTML file a download is (#2530). Both read the complete
// mux, so the composition root calls MountFor once the mux is assembled.
package exportwire

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpserver/pdfhttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/standalonehttp"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// MountFor mounts the PDF and standalone download routes on mux.
func MountFor(mux *http.ServeMux, p *platform.Platform) {
	pdfhttp.MountFor(mux, p)
	standalonehttp.MountFor(mux, p)
}
