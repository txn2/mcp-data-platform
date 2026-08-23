package contenttype

import (
	"path"
	"strings"
)

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

// extensionTypes is the reverse of extensions: the canonical media type a
// filename's extension names. It is derived from the same table so the two
// cannot drift, and an extension claimed by more than one family is left out
// because there is nothing to prefer between the claimants.
//
// Active types are excluded. Detection may never produce one from anything but
// an explicit declaration, and a filename is not a declaration: naming a file
// .html must not turn its bytes into a family whose renderer runs script.
// Generic types are excluded because they say nothing a declaration does not
// already say.
var extensionTypes = newExtensionTypes()

// newExtensionTypes inverts the extension table, dropping the extensions no
// single family owns.
func newExtensionTypes() map[string]string {
	claims := make(map[string]int, len(extensions))
	for ct, ext := range extensions {
		if IsActive(ct) || IsGeneric(ct) {
			continue
		}
		claims[ext]++
	}
	types := make(map[string]string, len(claims))
	for ct, ext := range extensions {
		if IsActive(ct) || IsGeneric(ct) || claims[ext] != 1 {
			continue
		}
		types[ext] = ct
	}
	return types
}

// TypeForFilename returns the canonical media type a filename's extension
// names, or the empty string when the name carries no extension, the extension
// is unknown, or it names a family detection may not produce on its own.
//
// The answer is a claim about the name, not about the bytes. Nothing decides a
// stored type on this alone; see DetectFile.
func TypeForFilename(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return ""
	}
	return extensionTypes[ext]
}
