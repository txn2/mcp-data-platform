package mcpapps

import "testing"

func TestMIMEType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		// HTML
		{"index.html", "text/html; charset=utf-8"},
		{"page.htm", "text/html; charset=utf-8"},
		{"INDEX.HTML", "text/html; charset=utf-8"},

		// CSS
		{"style.css", "text/css; charset=utf-8"},
		{"STYLE.CSS", "text/css; charset=utf-8"},

		// JavaScript
		{"app.js", "application/javascript; charset=utf-8"},
		{"module.mjs", "application/javascript; charset=utf-8"},

		// JSON
		{"data.json", "application/json; charset=utf-8"},
		{"config.map", "application/json; charset=utf-8"},

		// Images
		{"logo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"animation.gif", "image/gif"},
		{"icon.svg", "image/svg+xml"},
		{"favicon.ico", "image/x-icon"},

		// Fonts
		{"font.woff", "font/woff"},
		{"font.woff2", "font/woff2"},
		{"font.ttf", "font/ttf"},
		{"font.eot", "application/vnd.ms-fontobject"},
		{"font.otf", "font/otf"},

		// Other
		{"data.xml", "application/xml; charset=utf-8"},
		{"readme.txt", "text/plain; charset=utf-8"},
		{"unknown.xyz", "application/octet-stream"},
		{"noextension", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := MIMEType(tt.filename)
			if got != tt.want {
				t.Errorf("MIMEType(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}
