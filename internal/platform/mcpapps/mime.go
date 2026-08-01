package mcpapps

import (
	"path"
	"strings"
)

// MIME type constants used by mimeTypes and any callers that need to
// match against a known type without re-typing the literal.
const (
	mimeHTML        = "text/html; charset=utf-8"
	mimeCSS         = "text/css; charset=utf-8"
	mimeJS          = "application/javascript; charset=utf-8"
	mimeJSON        = "application/json; charset=utf-8"
	mimeXML         = "application/xml; charset=utf-8"
	mimeText        = "text/plain; charset=utf-8"
	mimePNG         = "image/png"
	mimeJPEG        = "image/jpeg"
	mimeGIF         = "image/gif"
	mimeSVG         = "image/svg+xml"
	mimeWOFF2       = "font/woff2"
	mimeOctetStream = "application/octet-stream"
)

// mimeTypes maps file extensions to MIME types.
var mimeTypes = map[string]string{
	".html":  mimeHTML,
	".htm":   mimeHTML,
	".css":   mimeCSS,
	".js":    mimeJS,
	".mjs":   mimeJS,
	".json":  mimeJSON,
	".png":   mimePNG,
	".jpg":   mimeJPEG,
	".jpeg":  mimeJPEG,
	".gif":   mimeGIF,
	".svg":   mimeSVG,
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": mimeWOFF2,
	".ttf":   "font/ttf",
	".eot":   "application/vnd.ms-fontobject",
	".otf":   "font/otf",
	".xml":   mimeXML,
	".txt":   mimeText,
	".map":   mimeJSON,
}

// MIMEType returns the MIME type for a file based on its extension.
func MIMEType(filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}
	return mimeOctetStream
}
