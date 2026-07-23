package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// exportWithUpstream runs a real api_export against an upstream that answers
// with the given Content-Type header and body, and returns what was persisted.
// It exercises the whole streaming path, so the assertions are about what the
// portal viewer will later read, not about the detection function alone.
func exportWithUpstream(t *testing.T, header, body string) (ExportAsset, s3Put, ExportVersion) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if header != "" {
			w.Header().Set("Content-Type", header)
		} else {
			// Suppress net/http's own sniffing so the export path sees a
			// genuinely absent declaration, as a bare upstream would send.
			w.Header()["Content-Type"] = nil
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/items", Name: "items dump",
	})
	if r == nil || r.IsError {
		t.Fatalf("handleExport: error result: %+v", r)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("expected 1 asset insert, got %d", len(store.inserted))
	}
	if len(s3.puts) != 1 {
		t.Fatalf("expected 1 s3 put, got %d", len(s3.puts))
	}
	if len(ver.createdVersions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(ver.createdVersions))
	}
	return store.inserted[0], s3.puts[0], ver.createdVersions[0]
}

// TestExportDetectsContentType is the motivating case of issue #1007: an
// upstream that returns JSON under a generic Content-Type (or none at all)
// produces an asset stored as application/json, which the viewer can render.
func TestExportDetectsContentType(t *testing.T) {
	const jsonBody = `{"results":[{"id":1,"name":"acme"}],"total":1}`

	tests := []struct {
		name         string
		header       string
		body         string
		wantType     string
		wantDeclared string
		wantExt      string
	}{
		{
			name:         "json declared text/plain",
			header:       "text/plain",
			body:         jsonBody,
			wantType:     "application/json",
			wantDeclared: "text/plain",
			wantExt:      ".json",
		},
		{
			name:         "json declared text/plain with charset",
			header:       "text/plain; charset=utf-8",
			body:         jsonBody,
			wantType:     "application/json",
			wantDeclared: "text/plain; charset=utf-8",
			wantExt:      ".json",
		},
		{
			name:         "json with no Content-Type at all",
			header:       "",
			body:         jsonBody,
			wantType:     "application/json",
			wantDeclared: "",
			wantExt:      ".json",
		},
		{
			name:         "json declared application/octet-stream",
			header:       "application/octet-stream",
			body:         jsonBody,
			wantType:     "application/json",
			wantDeclared: "application/octet-stream",
			wantExt:      ".json",
		},
		{
			name:     "correct declaration is left alone",
			header:   "application/json",
			body:     jsonBody,
			wantType: "application/json",
			wantExt:  ".json",
		},
		{
			name:     "specific non-json declaration wins over content",
			header:   "text/csv",
			body:     jsonBody,
			wantType: "text/csv",
			wantExt:  ".csv",
		},
		{
			name:         "csv declared octet-stream",
			header:       "application/octet-stream",
			body:         "id,name\n1,acme\n2,globex\n3,initech\n",
			wantType:     "text/csv",
			wantDeclared: "application/octet-stream",
			wantExt:      ".csv",
		},
		{
			name:     "prose stays plain text",
			header:   "text/plain",
			body:     "Nothing structured here.\nJust a sentence.\n",
			wantType: "text/plain",
			wantExt:  ".txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset, put, version := exportWithUpstream(t, tt.header, tt.body)

			if asset.ContentType != tt.wantType {
				t.Errorf("asset content type = %q, want %q", asset.ContentType, tt.wantType)
			}
			if version.ContentType != tt.wantType {
				t.Errorf("version content type = %q, want %q", version.ContentType, tt.wantType)
			}
			if put.ContentType != tt.wantType {
				t.Errorf("s3 content type = %q, want %q", put.ContentType, tt.wantType)
			}
			if !strings.HasSuffix(put.Key, tt.wantExt) {
				t.Errorf("s3 key = %q, want suffix %q", put.Key, tt.wantExt)
			}
			if asset.Provenance.DeclaredContentType != tt.wantDeclared {
				t.Errorf("provenance declared type = %q, want %q",
					asset.Provenance.DeclaredContentType, tt.wantDeclared)
			}
			// The whole body must still reach storage: detection reads a
			// prefix and replays it, it does not consume anything.
			if string(put.Data) != tt.body {
				t.Errorf("stored body = %q, want %q", put.Data, tt.body)
			}
		})
	}
}

// TestExportNeverUpgradesToActiveType is the security rule at the export path:
// an upstream that returns HTML under a generic type must not produce an asset
// the viewer will render as markup.
func TestExportNeverUpgradesToActiveType(t *testing.T) {
	body := "<!DOCTYPE html>\n<html><body><script>alert(1)</script></body></html>"

	for _, header := range []string{"text/plain", "application/octet-stream", ""} {
		t.Run("declared "+header, func(t *testing.T) {
			asset, _, _ := exportWithUpstream(t, header, body)
			if asset.ContentType != "text/plain" {
				t.Errorf("content type = %q, want text/plain", asset.ContentType)
			}
		})
	}
}

// TestExportStreamsBodyLargerThanSniffWindow proves detection did not turn the
// streaming export into a buffered one, and did not truncate the payload: a
// body well past the sniff window arrives at storage byte-for-byte.
func TestExportStreamsBodyLargerThanSniffWindow(t *testing.T) {
	body := `{"rows":[` + strings.Repeat(`{"id":1,"name":"acme"},`, 4000) + `{"id":2}]}`

	asset, put, _ := exportWithUpstream(t, "text/plain", body)

	if asset.ContentType != "application/json" {
		t.Errorf("content type = %q, want application/json", asset.ContentType)
	}
	if string(put.Data) != body {
		t.Errorf("stored body length = %d, want %d", len(put.Data), len(body))
	}
}
