package resourceupload

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Route is the pattern a link is PUT to, and NamedRoute the one a link issued
// with no filename is PUT to, the file's name after the token (#2844).
const (
	Route      = "PUT " + RoutePrefix + "{token}"
	NamedRoute = "PUT " + RoutePrefix + "{token}/{filename}"
)

// uploadedFile is what one PUT to a link filed.
type uploadedFile struct {
	ResourceID  string `json:"resource_id" example:"0b9c6a3e-2f1d-4c8e-9a7b-5d4e3f2a1b0c"`
	Reference   string `json:"reference" example:"mcp:resource:0b9c6a3e-2f1d-4c8e-9a7b-5d4e3f2a1b0c"`
	URI         string `json:"uri" example:"mcp://user/u-1/site-sources/site.zip"`
	Filename    string `json:"filename" example:"site.zip"`
	ContentType string `json:"content_type" example:"application/zip"`
	SizeBytes   int64  `json:"size_bytes" example:"209715200"`
	// FilesRemaining is how many more files the link takes (#2844).
	FilesRemaining int `json:"files_remaining" example:"3"`
}

// Mount registers the link's route on mux. The route takes no session or key:
// the link is the credential.
func (s *Service) Mount(mux *http.ServeMux) {
	if s == nil {
		return
	}
	mux.HandleFunc(Route, s.put)
	mux.HandleFunc(NamedRoute, s.putNamed)
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
// @Description  Files the request body as a managed resource in the folder the link was issued for (manage_resource action=upload_link or the portal's Resources page, #2639, #2844), as the person who asked for it, with the checks a portal upload gets: the deployment's upload limit, the denied types and the link's if_exists. The link is the credential: it takes no session or key, takes as many files as it was issued for (max_files), and expires when it was issued to (at most a day). A link issued with a filename is PUT to as it is; one issued without is PUT to with each file's name after the token (/resources/uploads/{token}/{filename}). A PUT that files nothing (a body past the size, a refused type) gives its place back, so it can be sent again. An unknown, full, revoked or expired link answers 404, told apart to no one.
// @Tags         Resources
// @Accept       octet-stream
// @Produce      json
// @Param        token  path  string  true  "The link's token"
// @Success      201  {object}  resourceupload.uploadedFile
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Router       /resources/uploads/{token} [put]
func (s *Service) put(w http.ResponseWriter, r *http.Request) {
	res, remaining, err := s.Upload(r.Context(), r.PathValue("token"), r.PathValue("filename"), r.Body, r.ContentLength)
	switch {
	case errors.Is(err, ErrNoLink):
		httpjson.WriteError(w, http.StatusNotFound, ErrNoLink.Error())
	case errors.Is(err, ErrBadName):
		httpjson.WriteError(w, http.StatusBadRequest, strings.TrimSuffix(err.Error(), ": "+ErrBadName.Error()))
	case errors.Is(err, ErrTooLarge):
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, ErrTooLarge.Error()+"; ask for a new link with a larger size_bytes")
	case err != nil:
		// The write's refusal is the caller's to read: a type, a name, an
		// address. Nothing in it came from the link's token.
		slog.WarnContext(r.Context(), "upload link: the write failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "the file was not filed: "+err.Error())
	default:
		httpjson.WriteJSON(w, http.StatusCreated, uploaded(res, remaining))
	}
}

// putNamed is the route a link issued with no filename is PUT to; its own
// handler so the API description gives each route the parameters it has.
//
// @Summary      Upload a named file to an upload link
// @Description  The form of PUT /resources/uploads/{token} for a link issued with no filename (#2844): the file is filed under the name after the token, which must be a plain file name.
// @Tags         Resources
// @Accept       octet-stream
// @Produce      json
// @Param        token     path  string  true  "The link's token"
// @Param        filename  path  string  true  "The file's name"
// @Success      201  {object}  resourceupload.uploadedFile
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Router       /resources/uploads/{token}/{filename} [put]
func (s *Service) putNamed(w http.ResponseWriter, r *http.Request) { s.put(w, r) }

func uploaded(res *resource.Resource, remaining int) uploadedFile {
	return uploadedFile{
		ResourceID: res.ID, URI: res.URI, Filename: res.Filename,
		Reference:   knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID}.URN(),
		ContentType: res.MIMEType, SizeBytes: res.SizeBytes, FilesRemaining: max(remaining, 0),
	}
}
