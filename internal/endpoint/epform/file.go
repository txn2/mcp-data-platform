package epform

import (
	"errors"
	"fmt"
	"mime"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Bounds of a file field (#2333).
const (
	// DefaultMaxFiles is how many files a file field takes when it names no
	// max_files; MaxFiles is the most any may name.
	DefaultMaxFiles = 1
	MaxFiles        = 20
	// DefaultMaxFileBytes bounds each file of a field that names no
	// max_bytes, below the deployment's upload ceiling when that is lower.
	DefaultMaxFileBytes int64 = 10 << 20
	maxAccept                 = 50
)

// The keys each file of a file field is described by: in form_submit's
// values, and in the stored submission, which adds where it was saved.
const (
	FileKeyName        = "name"
	FileKeyContentType = "content_type"
	FileKeySize        = "size"
	FileKeyURI         = "uri"
	FileKeyResourceID  = "resource_id"
)

// Upload is one file a submission carries: the field it was sent in, the name
// the browser gave it, and its bytes.
type Upload struct {
	Field string
	Name  string
	Data  []byte
}

// ContentType is the upload's type, judged from its bytes and never from the
// name or type the browser sent.
func (u Upload) ContentType() string {
	return contenttype.DetectBytes("", u.Data)
}

// HasFiles reports whether the spec declares a file field.
func (s Spec) HasFiles() bool {
	for _, f := range s.Fields {
		if f.Type == TypeFile {
			return true
		}
	}
	return false
}

// CheckUploads reports a file field whose max_bytes is past ceiling, the
// most a managed resource may hold on this deployment
// (resources.managed.max_upload_bytes).
func (s Spec) CheckUploads(ceiling int64) error {
	for _, f := range s.Fields {
		if f.Type == TypeFile && ceiling > 0 && f.MaxBytes > ceiling {
			return fmt.Errorf("file field %q has max_bytes %d, past this deployment's upload limit of %d bytes", f.Name, f.MaxBytes, ceiling)
		}
	}
	return nil
}

// normalizeFile fills the bounds a file field leaves out.
func (f *Field) normalizeFile() {
	if f.MaxFiles == 0 {
		f.MaxFiles = DefaultMaxFiles
	}
	if f.MaxBytes == 0 {
		f.MaxBytes = DefaultMaxFileBytes
	}
}

// validateFile checks a file field's bounds, and that no other field names
// them.
func (f Field) validateFile() error {
	if f.Type != TypeFile {
		return f.validateNotFile()
	}
	switch {
	case f.MaxFiles < 1 || f.MaxFiles > MaxFiles:
		return fmt.Errorf("file field %q has max_files %d; it is from 1 to %d", f.Name, f.MaxFiles, MaxFiles)
	case f.MaxBytes < 1:
		return fmt.Errorf("file field %q has max_bytes %d; it is at least 1", f.Name, f.MaxBytes)
	case f.Sensitive:
		return fmt.Errorf("file field %q is sensitive; a file is saved as a managed resource and is not encrypted with the submission", f.Name)
	case len(f.Accept) > maxAccept:
		return fmt.Errorf("file field %q accepts more than %d types", f.Name, maxAccept)
	}
	return f.validateAccepts()
}

// validateNotFile refuses a file field's bounds on another type.
func (f Field) validateNotFile() error {
	if f.MaxFiles != 0 || f.MaxBytes != 0 || len(f.Accept) > 0 {
		return fmt.Errorf("field %q is a %s and names max_files, max_bytes or accept, which are a file field's", f.Name, f.Type)
	}
	return nil
}

// validateAccepts checks each type a file field accepts.
func (f Field) validateAccepts() error {
	for _, a := range f.Accept {
		if err := validateAccept(a); err != nil {
			return fmt.Errorf("file field %q: %w", f.Name, err)
		}
	}
	return nil
}

// validateAccept accepts a media type or a type family ("image/*").
func validateAccept(a string) error {
	mt, params, err := mime.ParseMediaType(a)
	if err != nil || len(params) > 0 || mt != a || !strings.Contains(a, "/") || strings.HasPrefix(a, "*") {
		return fmt.Errorf("accept %q must be a content type such as image/png, or a family such as image/*", a)
	}
	return nil
}

// accepts reports whether the field takes a file of content type ct.
func (f Field) accepts(ct string) bool {
	if len(f.Accept) == 0 {
		return true
	}
	ct = contenttype.Normalize(ct)
	for _, a := range f.Accept {
		if family, ok := strings.CutSuffix(a, "/*"); ok {
			if strings.HasPrefix(ct, family+"/") {
				return true
			}
		} else if contenttype.Normalize(a) == ct {
			return true
		}
	}
	return false
}

// checkFiles is a file field's files described as form_submit is handed
// them, or the message that refuses them. A file field takes no text value.
func (f Field) checkFiles(values []string, files []Upload) (value any, refusal string) {
	if first(values) != "" {
		return nil, "this field takes files"
	}
	switch {
	case len(files) == 0 && f.Required:
		return nil, "this field is required"
	case len(files) > f.MaxFiles:
		return nil, fmt.Sprintf("this field takes at most %d %s", f.MaxFiles, plural(f.MaxFiles, "file", "files"))
	}
	out := make([]any, 0, len(files))
	for _, u := range files {
		ct := u.ContentType()
		switch {
		case int64(len(u.Data)) > f.MaxBytes:
			return nil, fmt.Sprintf("%s is larger than %d bytes", fileLabel(u), f.MaxBytes)
		case !f.accepts(ct):
			return nil, fmt.Sprintf("%s is %s, which this field does not take", fileLabel(u), ct)
		}
		out = append(out, map[string]any{FileKeyName: u.Name, FileKeyContentType: ct, FileKeySize: int64(len(u.Data))})
	}
	return out, ""
}

func fileLabel(u Upload) string {
	if u.Name == "" {
		return "a file"
	}
	return fmt.Sprintf("%q", u.Name)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// errNoBoundary is a multipart body whose type names no boundary.
var errNoBoundary = errors.New("the submission is not a multipart form: it names no boundary")
