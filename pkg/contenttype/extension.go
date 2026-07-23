package contenttype

import "strings"

// extensions maps a canonical media type to the file extension used for its
// object key. Storage keys carry an extension so an object downloaded straight
// from the bucket opens in the right application, and so operators browsing the
// bucket can tell what a key holds without consulting the database.
var extensions = map[string]string{
	JSON:               ".json",
	NDJSON:             ".ndjson",
	CSV:                ".csv",
	TSV:                ".tsv",
	XML:                ".xml",
	YAML:               ".yaml",
	Markdown:           ".md",
	PlainText:          ".txt",
	HTML:               ".html",
	JSX:                ".html",
	SVG:                ".svg",
	JavaScript:         ".js",
	PDF:                ".pdf",
	OctetStream:        ".bin",
	"application/sql":  ".sql",
	"application/zip":  ".zip",
	"application/gzip": ".gz",
	"text/x-python":    ".py",
	"text/css":         ".css",
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/gif":        ".gif",
	"image/webp":       ".webp",
	"image/avif":       ".avif",
	"image/bmp":        ".bmp",
	"image/x-icon":     ".ico",
	"image/tiff":       ".tiff",
	"audio/mpeg":       ".mp3",
	"audio/wav":        ".wav",
	"audio/ogg":        ".ogg",
	"audio/mp4":        ".m4a",
	"audio/flac":       ".flac",
	"audio/aiff":       ".aiff",
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/ogg":        ".ogv",
	"video/quicktime":  ".mov",
	"video/x-msvideo":  ".avi",
	"font/woff":        ".woff",
	"font/woff2":       ".woff2",
	"font/ttf":         ".ttf",
}

// Extension returns the file extension for a media type, including the leading
// dot. Unrecognized types fall back to ".bin".
func Extension(ct string) string {
	norm := Normalize(ct)
	if ext, ok := extensions[norm]; ok {
		return ext
	}
	// A "+json"/"+xml" structured suffix names the underlying syntax even when
	// the full type is unregistered (e.g. application/vnd.acme.thing+json).
	switch {
	case strings.HasSuffix(norm, "+json"):
		return ".json"
	case strings.HasSuffix(norm, "+xml"):
		return ".xml"
	case strings.HasSuffix(norm, "+yaml"):
		return ".yaml"
	case strings.HasPrefix(norm, "text/"):
		return ".txt"
	default:
		return ".bin"
	}
}
