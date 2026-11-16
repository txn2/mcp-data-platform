package resourceupload

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Route is the pattern a link is PUT to.
const Route = "PUT " + RoutePrefix + "{token}"

// uploadedFile is what one PUT to a link filed.
type uploadedFile struct {
	ResourceID  string `json:"resource_id" example:"0b9c6a3e-2f1d-4c8e-9a7b-5d4e3f2a1b0c"`
	Reference   string `json:"reference" example:"mcp:resource:0b9c6a3e-2f1d-4c8e-9a7b-5d4e3f2a1b0c"`
	URI         string `json:"uri" example:"mcp://user/u-1/site-sources/site.zip"`
	Filename    string `json:"filename" example:"site.zip"`
	ContentType string `json:"content_type" example:"application/zip"`
	SizeBytes   int64  `json:"size_bytes" example:"209715200"`
}

// Mount registers the link's route on mux. The route takes no session or key:
// the link is the credential.
func (s *Service) Mount(mux *http.ServeMux) {
	if s == nil {
		return
	}
	mux.HandleFunc(Route, s.put)
}

// MountFrom registers the link's route on mux from the toolkit among kits that
// issues links (the portal toolkit's MountUploadlinks).
func MountFrom[T any](mux *http.ServeMux, kits []T) {
	for _, k := range kits {
		if m, ok := any(k).(interface{ MountUploadLinks(mux *http.ServeMux) }); ok {
			m.MountUploadLinks(mux)
		}
	}
}

// put files the file PUT to a link.
//
// @Summary      Upload a file to an upload link
// @Description  Files the request body as the managed resource the link was issued for (manage_resource action=upload_link, #2639), as the person who asked for it, with the checks a portal upload gets: the deployment's upload limit, the denied types and the link's if_exists. The link is the credential: it takes no session or key, is good for one PUT, and expires minutes after it was issued. A body past the size the link was issued for is refused. An unknown, used or expired link answers 404, the three told apart to no one.
// @Tags         Resources
// @Accept       octet-stream
// @Produce      json
// @Param        token  path  string  true  "The link's token"
// @Success      201  {object}  map[string]any  "resource_id, reference, uri, filename, content_type and size_bytes of the file filed"
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Router       /resources/uploads/{token} [put]
func (s *Service) put(w http.ResponseWriter, r *http.Request) {
	res, err := s.Upload(r.Context(), r.PathValue("token"), r.Body, r.ContentLength)
	switch {
	case errors.Is(err, ErrNoLink):
		httpjson.WriteError(w, http.StatusNotFound, ErrNoLink.Error())
	case errors.Is(err, ErrTooLarge):
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, ErrTooLarge.Error()+"; ask for a new link with a larger size_bytes")
	case err != nil:
		// The write's refusal is the caller's to read: a type, a name, an
		// address. Nothing in it came from the link's token.
		slog.WarnContext(r.Context(), "upload link: the write failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "the file was not filed: "+err.Error())
	default:
		httpjson.WriteJSON(w, http.StatusCreated, uploaded(res))
	}
}

func uploaded(res *resource.Resource) uploadedFile {
	return uploadedFile{
		ResourceID: res.ID, URI: res.URI, Filename: res.Filename,
		Reference:   knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID}.URN(),
		ContentType: res.MIMEType, SizeBytes: res.SizeBytes,
	}
}
