package sitehttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// A site's files in the portal (#2590): dev's tree, one file's text, a save of
// one file, a delete, an upload and one file's change between two releases.
// A write is refused before its body is read unless the caller may publish to
// dev. Every write is a publish to dev with the release the editor read as its base,
// the method an agent's patch and a publish with base go through, so a hand
// edit is an ordinary release with the same checks, lineage and rollback, and a
// change made against an older release is refused rather than overwriting.

const (
	paramPath = "path"
	paramBase = "base"
	// uploadMemory is how much of a multipart upload is held in memory before
	// the rest goes to temporary files.
	uploadMemory = 32 << 20
)

// FileTextView is one file's text as dev serves it.
type FileTextView struct {
	Environment string `json:"environment" example:"dev"`
	Release     int    `json:"release" example:"14"`
	Path        string `json:"path" example:"pricing/index.html"`
	ContentType string `json:"content_type" example:"text/html; charset=utf-8"`
	Body        string `json:"body"`
}

// FileSaveBody saves one file into dev.
type FileSaveBody struct {
	Path string `json:"path" example:"pricing/index.html"`
	// Content is the whole file: text, or base64 with encoding "base64".
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty" example:"utf-8"`
	// Base is the release the editor read, which dev must still serve.
	Base int    `json:"base" example:"14"`
	Note string `json:"note,omitempty"`
	// DryRun checks the save, renders a rendered site, and stores nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

// FileDiffView is one file's change between two releases.
type FileDiffView struct {
	From int    `json:"from" example:"14"`
	To   int    `json:"to" example:"15"`
	Path string `json:"path"`
	Diff string `json:"diff"`
}

func (a *portalAPI) registerFiles(mux *http.ServeMux) {
	mux.HandleFunc("GET "+pathSite+"/files", a.files)
	mux.HandleFunc("POST "+pathSite+"/files", a.uploadFiles)
	mux.HandleFunc("GET "+pathSite+"/file", a.fileText)
	mux.HandleFunc("PUT "+pathSite+"/file", a.saveFile)
	mux.HandleFunc("DELETE "+pathSite+"/file", a.deleteFile)
	mux.HandleFunc("GET "+pathSite+"/file/diff", a.fileDiff)
}

func devRef(r *http.Request, p string) site.Ref {
	return site.Ref{Site: r.PathValue(paramSite), Environment: site.EnvDev, Path: p}
}

// files handles GET /api/v1/portal/sites/{site}/files.
//
// @Summary      A site's files
// @Description  The files dev serves, or its source on a rendered site, under a folder (path ending in /, or "" for every file), one page at a time. The people who manage the site read the source; someone dev is shared with reads what it serves.
// @Tags         Sites
// @Produce      json
// @Param        site    path      string  true   "Site name or id"
// @Param        path    query     string  false  "A folder, ending in /"
// @Param        offset  query     int     false  "Files to skip"
// @Param        limit   query     int     false  "Files on the page, at most 1000"
// @Success      200     {object}  sitesvc.TreeListing
// @Failure      403     {object}  httpjson.ProblemDetail
// @Failure      404     {object}  httpjson.ProblemDetail
// @Failure      422     {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/files [get]
func (a *portalAPI) files(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := a.svc().Tree(r.Context(), c.actor, devRef(r, q.Get(paramPath)), offset, limit)
	if err != nil {
		failFile(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// fileText handles GET /api/v1/portal/sites/{site}/file.
//
// @Summary      One file's text
// @Description  The text of one file dev serves, with the release it was read from, which a save names as its base. A file that is not text is refused.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Param        path  query     string  true  "The file"
// @Success      200   {object}  FileTextView
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/file [get]
func (a *portalAPI) fileText(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	out, err := a.svc().ReadFile(r.Context(), c.actor, devRef(r, r.URL.Query().Get(paramPath)))
	if err != nil {
		failFile(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, FileTextView{Environment: out.Environment, Release: out.Release, Path: out.Path, ContentType: out.ContentType, Body: out.Body})
}

// saveFile handles PUT /api/v1/portal/sites/{site}/file.
//
// @Summary      Save one file
// @Description  Writes one file, new or changed, as a new release of dev derived from base, which must be the release dev serves now: a save against an older release is refused with 409 and nothing is written. With dry_run it stores nothing and reports how many served files would change, which on a rendered site is how many pages a layout touches. The people who manage the site only.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string        true  "Site name or id"
// @Param        body  body      FileSaveBody  true  "The file"
// @Success      200   {object}  sitesvc.Published
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/file [put]
func (a *portalAPI) saveFile(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok || !a.mayWrite(w, r, c) {
		return
	}
	var in FileSaveBody
	limit := 2*a.d.Runtime.Live.Get().Limits().MaxFileBytes + maxBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not valid JSON, or the file is larger than a release takes")
		return
	}
	// A save names the release it read: without one, a publish would replace
	// the whole file set with this one file.
	if in.Base <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "base: the release dev serves, which the file was read from")
		return
	}
	body := []byte(in.Content)
	if in.Encoding == "base64" {
		var err error
		if body, err = base64.StdEncoding.DecodeString(in.Content); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "content: not base64")
			return
		}
	}
	out, err := a.svc().Publish(r.Context(), c.actor, sitesvc.PublishRequest{
		Site: r.PathValue(paramSite), Environment: site.EnvDev, Source: "edit " + in.Path, Note: in.Note,
		Base: in.Base, DryRun: in.DryRun, Files: []sitesvc.InputFile{{Path: in.Path, Body: body}},
	})
	if !in.DryRun {
		a.record(r.Context(), c, sitetool.CmdPublish, map[string]any{paramSite: r.PathValue(paramSite), paramPath: in.Path, paramBase: in.Base}, err)
	}
	if err != nil {
		failFile(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteFile handles DELETE /api/v1/portal/sites/{site}/file.
//
// @Summary      Delete one file
// @Description  Writes a release of dev without the file, derived from base, which must be the release dev serves now. Rolling back to base brings it back. The people who manage the site only.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true   "Site name or id"
// @Param        path  query     string  true   "The file"
// @Param        base  query     int     true   "The release dev serves"
// @Param        note  query     string  false  "What changed"
// @Success      200   {object}  sitesvc.Published
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/file [delete]
func (a *portalAPI) deleteFile(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok || !a.mayWrite(w, r, c) {
		return
	}
	q := r.URL.Query()
	base, err := strconv.Atoi(q.Get(paramBase))
	if err != nil || base <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "base: the release dev serves")
		return
	}
	name := q.Get(paramPath)
	// The release's label, shown beside "edit <path>" and "patch <path>"; it
	// is never SQL.
	label := "delete " + name // nosemgrep: go.lang.security.injection.tainted-sql-string.tainted-sql-string
	out, err := a.svc().Publish(r.Context(), c.actor, sitesvc.PublishRequest{
		Site: r.PathValue(paramSite), Environment: site.EnvDev, Source: label, Note: q.Get("note"), Base: base, Remove: []string{name},
	})
	a.record(r.Context(), c, sitetool.CmdPublish, map[string]any{paramSite: r.PathValue(paramSite), paramPath: name, paramBase: base, "delete": true}, err)
	if err != nil {
		failFile(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// uploadFiles handles POST /api/v1/portal/sites/{site}/files.
//
// @Summary      Upload files
// @Description  Writes the uploaded files into a folder of dev as one new release derived from base, which must be the release dev serves now; a file at a path that is taken replaces it. Multipart form: base, folder, note and one or more files. The people who manage the site only.
// @Tags         Sites
// @Accept       multipart/form-data
// @Produce      json
// @Param        site    path      string  true   "Site name or id"
// @Param        base    formData  int     true   "The release dev serves"
// @Param        folder  formData  string  false  "The folder, \"\" for the top"
// @Param        note    formData  string  false  "What changed"
// @Param        files   formData  file    true   "The files"
// @Success      200     {object}  sitesvc.Published
// @Failure      400     {object}  httpjson.ProblemDetail
// @Failure      403     {object}  httpjson.ProblemDetail
// @Failure      409     {object}  httpjson.ProblemDetail
// @Failure      422     {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/files [post]
func (a *portalAPI) uploadFiles(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok || !a.mayWrite(w, r, c) {
		return
	}
	limits := a.d.Runtime.Live.Get().Limits()
	r.Body = http.MaxBytesReader(w, r.Body, limits.MaxReleaseBytes)
	// #nosec G120 -- r.Body is bounded by http.MaxBytesReader above, so the
	// multipart parse cannot exhaust memory; gosec's pattern match misses it.
	if err := r.ParseMultipartForm(uploadMemory); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the upload is not a multipart form, or is larger than a release takes")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	base, err := strconv.Atoi(r.FormValue(paramBase))
	if err != nil || base <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "base: the release dev serves")
		return
	}
	files, err := uploaded(r, strings.Trim(r.FormValue("folder"), "/"), limits.MaxFileBytes)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := a.svc().Publish(r.Context(), c.actor, sitesvc.PublishRequest{
		Site: r.PathValue(paramSite), Environment: site.EnvDev, Source: "upload", Note: r.FormValue("note"), Base: base, Files: files,
	})
	a.record(r.Context(), c, sitetool.CmdPublish, map[string]any{paramSite: r.PathValue(paramSite), paramBase: base, "files": len(files)}, err)
	if err != nil {
		failFile(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// mayWrite refuses, before a byte of the body is read, a caller who could not
// publish to dev: a save holds up to twice sites.max_file_bytes in memory and
// an upload spools up to sites.max_release_bytes to disk, which only the people
// who manage the site may make a replica do.
func (a *portalAPI) mayWrite(w http.ResponseWriter, r *http.Request, c *caller) bool {
	if err := a.svc().MayPublish(r.Context(), c.actor, r.PathValue(paramSite), site.EnvDev); err != nil {
		failFile(w, r, err)
		return false
	}
	return true
}

// uploaded reads the form's files into the folder, each at most maxFile bytes.
func uploaded(r *http.Request, folder string, maxFile int64) ([]sitesvc.InputFile, error) {
	heads := r.MultipartForm.File["files"]
	if len(heads) == 0 {
		return nil, errors.New("files: choose at least one file")
	}
	out := make([]sitesvc.InputFile, 0, len(heads))
	for _, h := range heads {
		if h.Size > maxFile {
			return nil, errors.New("file " + h.Filename + " is larger than sites.max_file_bytes allows")
		}
		f, err := h.Open()
		if err != nil {
			return nil, errors.New("file " + h.Filename + " could not be read")
		}
		body, err := io.ReadAll(io.LimitReader(f, maxFile+1))
		_ = f.Close()
		if err != nil {
			return nil, errors.New("file " + h.Filename + " could not be read")
		}
		name := path.Base(h.Filename)
		if folder != "" {
			name = folder + "/" + name
		}
		out = append(out, sitesvc.InputFile{Path: name, Body: body})
	}
	return out, nil
}

// fileDiff handles GET /api/v1/portal/sites/{site}/file/diff.
//
// @Summary      One file's change
// @Description  A unified diff of one file between two releases: to defaults to the release dev serves and from to the release to was derived from. The people who manage the site only, since an earlier release can hold what a later one took out.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true   "Site name or id"
// @Param        path  query     string  true   "The file"
// @Param        from  query     int     false  "The earlier release"
// @Param        to    query     int     false  "The later release"
// @Success      200   {object}  FileDiffView
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/file/diff [get]
func (a *portalAPI) fileDiff(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("from"))
	to, _ := strconv.Atoi(q.Get("to"))
	name := q.Get(paramPath)
	out, err := a.svc().Diff(r.Context(), c.actor, devRef(r, name), from, to)
	if err != nil {
		failFile(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, FileDiffView{From: out.From, To: out.To, Path: name, Diff: out.Diff})
}

// failFile answers a stale base with 409, so the editor can tell a conflict
// from a refusal, and a file that is not text with what it is; anything else
// as fail does.
func failFile(w http.ResponseWriter, r *http.Request, err error) {
	if te, ok := errors.AsType[*textpatch.Error](err); ok {
		switch te.Code {
		case textpatch.CodeStaleBase:
			httpjson.WriteError(w, http.StatusConflict, te.Error())
			return
		default:
			httpjson.WriteError(w, http.StatusUnprocessableEntity, te.Error())
			return
		}
	}
	fail(w, r, err)
}
