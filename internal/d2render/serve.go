package d2render

import (
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Serve answers ?render=svg on a content route for a D2 source with the SVG
// rendered from it (&theme=dark for the dark drawing), and reports whether it
// answered. The source is the one the route already read and authorized, its
// references rewritten, so an icon names the reference route. The drawing is
// served through blobserve like any stored SVG: an attachment under the
// sandbox policy, which the portal reads as text and cleans before drawing.
func Serve(w http.ResponseWriter, r *http.Request, name, contentType string, source []byte) bool {
	q := r.URL.Query()
	if q.Get("render") != "svg" || contenttype.Normalize(contentType) != contenttype.D2 {
		return false
	}
	svg, err := Render(r.Context(), string(source), q.Get("theme") == "dark")
	switch {
	case errors.Is(err, ErrTooLarge):
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, "the diagram is too large to draw")
	case errors.Is(err, ErrTimeout):
		httpjson.WriteError(w, http.StatusServiceUnavailable, "the diagram did not finish drawing in time")
	case err != nil:
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "the diagram could not be drawn: "+err.Error())
	default:
		blobserve.Serve(w, r, blobserve.Options{Name: name + ".svg", ContentType: contenttype.SVG, Data: svg, Revalidate: true})
	}
	return true
}
