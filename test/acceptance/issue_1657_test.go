//go:build integration

package acceptance

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1657: an agent handed the URI of a PDF, a presentation, a spreadsheet
// or an image could not read the file. `fetch` returned the resource's
// metadata row, and an agent given a size and a media type reports that the
// platform is blocking it.
//
// Every criterion below runs through the real MCP surface against the running
// stack: a real file is uploaded into the platform's own managed-resource
// library with `manage_resource`, and read back with `fetch`, which is the
// tool the reporter's agent used.
//
// Wire forms: this ticket changes no input parameter. `fetch` takes one
// parameter, `reference`, typed string in a schema closed to unknown keys, so
// it admits exactly ONE JSON form, and that is what every check sends as
// literal tools/call params. The forms that matter here are on the RESULT, and
// they are the point of the ticket: a tool result's content is a list of
// blocks, and a file reaches a client as a text block, an image block or an
// embedded-resource block depending on what it is. Each check asserts on the
// blocks a real client received, not on the handler's return value, because
// the whole defect was that the file never reached the client at all. The
// upload side sends a binary file through `content_base64` and a text file
// through `content`, which are the two forms manage_resource admits for
// content, and both are exercised.
const (
	issue1657Purpose = "Acceptance for #1657: a binary resource's content reaches the agent."
	issue1657Folder  = "acceptance/issue-1657"
)

// issue1657Upload lands one file in the ticket's folder and returns the
// reference `fetch` reads it by. Binary files travel as base64; a text file
// travels as text.
func issue1657Upload(c *client, filename, contentType string, body []byte, asText bool) string {
	c.t.Helper()
	args := map[string]any{
		"action": "create", "path": issue1657Folder, "filename": filename,
		"display_name": "Acceptance 1657 " + filename,
		"description":  "Written by the #1657 acceptance run.",
		"content_type": contentType,
		"if_exists":    "replace",
	}
	if asText {
		args["content"] = string(body)
	} else {
		args["content_base64"] = base64.StdEncoding.EncodeToString(body)
	}
	out := c.call("manage_resource", args)
	ref, _ := out["reference"].(string)
	if ref == "" {
		c.t.Fatalf("manage_resource create returned no reference for %s: %v", filename, out)
	}
	return ref
}

// issue1657Fetch reads a resource by reference and returns the content blocks
// the client received, plus the decoded JSON body.
func issue1657Fetch(c *client, ref string) ([]mcp.Content, map[string]any) {
	c.t.Helper()
	res, text, err := c.callRaw("fetch", map[string]any{
		"reference": ref, "purpose": issue1657Purpose,
	})
	if err != nil {
		c.t.Fatalf("fetch: transport error: %v", err)
	}
	if res.IsError {
		c.t.Fatalf("fetch reported a tool error: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.t.Fatalf("fetch result is not a JSON object: %v\n%s", err, text)
	}
	if found, _ := out["found"].(bool); !found {
		c.t.Fatalf("fetch did not resolve %s: %v", ref, out)
	}
	return res.Content, out
}

// documentNote digs the fetched document's note out of the JSON: the one line
// that says where the file went when the body is empty.
func documentNote(t *testing.T, out map[string]any) string {
	t.Helper()
	doc, ok := out["document"].(map[string]any)
	if !ok {
		t.Fatalf("fetch carried no document: %v", out)
	}
	note, _ := doc["note"].(string)
	return note
}

// documentBody digs the fetched document's text body out of the JSON.
func documentBody(t *testing.T, out map[string]any) string {
	t.Helper()
	doc, ok := out["document"].(map[string]any)
	if !ok {
		t.Fatalf("fetch carried no document: %v", out)
	}
	body, _ := doc["body"].(string)
	return body
}

// TestIssue1657_APDFComesBackAsItsText is the ticket's headline: the reporter
// uploaded a PDF, handed its URI to an agent, and the agent could not read it.
func TestIssue1657_APDFComesBackAsItsText(t *testing.T) {
	c := connect(t)
	body, err := os.ReadFile("../../internal/docread/testdata/objstream.pdf")
	if err != nil {
		t.Fatalf("reading the PDF fixture: %v", err)
	}

	ref := issue1657Upload(c, fmt.Sprintf("report-%d.pdf", time.Now().UnixNano()), "application/pdf", body, false)
	_, out := issue1657Fetch(c, ref)

	got := documentBody(t, out)
	if !strings.Contains(got, "Quarterly revenue summary") {
		t.Fatalf("the PDF's text did not come back:\n%s", got)
	}
}

// TestIssue1657_APresentationComesBackAsItsParts is the case the reporter
// needs a deck for: an agent asked to use a presentation as a template must
// see the slide parts, not a summary of them.
func TestIssue1657_APresentationComesBackAsItsParts(t *testing.T) {
	c := connect(t)
	deck := issue1657Deck(t)

	ref := issue1657Upload(c, fmt.Sprintf("deck-%d.pptx", time.Now().UnixNano()),
		"application/vnd.openxmlformats-officedocument.presentationml.presentation", deck, false)
	_, out := issue1657Fetch(c, ref)

	got := documentBody(t, out)
	for _, want := range []string{"PowerPoint presentation", "ppt/slides/slide1.xml", "Quarterly Review"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the deck's parts are missing %q:\n%s", want, got)
		}
	}
	// The picture on the slide is not text, so it is named rather than
	// rendered. An agent shown only the XML would conclude the deck has none.
	if !strings.Contains(got, "ppt/media/image1.png") {
		t.Fatalf("the deck's picture was not named:\n%s", got)
	}
}

// TestIssue1657_AnImageComesBackAsAPicture: a model reads an image by looking
// at it, which is an image content block and nothing else.
func TestIssue1657_AnImageComesBackAsAPicture(t *testing.T) {
	c := connect(t)
	png := issue1657PNG()

	ref := issue1657Upload(c, fmt.Sprintf("chart-%d.png", time.Now().UnixNano()), "image/png", png, false)
	blocks, out := issue1657Fetch(c, ref)

	// The document's body is empty for a picture, so the record has to say
	// where the file went. An empty body with no explanation is what produced
	// the report that the platform was withholding the file.
	if note := documentNote(t, out); !strings.Contains(note, "attached") {
		t.Fatalf("the document did not say where the picture went: %q", note)
	}
	for _, block := range blocks {
		if img, ok := block.(*mcp.ImageContent); ok {
			if !bytes.Equal(img.Data, png) {
				t.Fatalf("the image block did not carry the file's bytes (%d of %d)", len(img.Data), len(png))
			}
			return
		}
	}
	t.Fatalf("the fetch result carried no image block: %s", blockKinds(blocks))
}

// TestIssue1657_AnUnreadableFileComesBackAsItsBytes: a family the server has
// no reader for still reaches the caller, for the caller's own tools to open.
// Metadata alone is what produced the report that the file cannot be opened.
func TestIssue1657_AnUnreadableFileComesBackAsItsBytes(t *testing.T) {
	c := connect(t)
	blob := []byte{0x00, 0x17, 0x42, 0xff, 0x00, 0x91}

	ref := issue1657Upload(c, fmt.Sprintf("thing-%d.bin", time.Now().UnixNano()), "application/octet-stream", blob, false)
	blocks, out := issue1657Fetch(c, ref)

	if note := documentNote(t, out); !strings.Contains(note, "attached") {
		t.Fatalf("the document did not say where the file went: %q", note)
	}
	for _, block := range blocks {
		if embedded, ok := block.(*mcp.EmbeddedResource); ok {
			if !bytes.Equal(embedded.Resource.Blob, blob) {
				t.Fatalf("the embedded resource did not carry the file's bytes: %v", embedded.Resource.Blob)
			}
			if !strings.HasPrefix(embedded.Resource.URI, "mcp://") {
				t.Fatalf("the embedded resource was not addressed: %q", embedded.Resource.URI)
			}
			return
		}
	}
	t.Fatalf("the fetch result carried no embedded resource: %s", blockKinds(blocks))
}

// TestIssue1657_ATextFileIsUnchanged: the path a text resource always took
// still returns its content inline, through the same reader.
func TestIssue1657_ATextFileIsUnchanged(t *testing.T) {
	c := connect(t)
	const contents = "column,description\ngross_margin_pct,margin after COGS\n"

	ref := issue1657Upload(c, fmt.Sprintf("dict-%d.csv", time.Now().UnixNano()), "text/csv", []byte(contents), true)
	_, out := issue1657Fetch(c, ref)

	if got := documentBody(t, out); !strings.Contains(got, "gross_margin_pct") {
		t.Fatalf("a text resource lost its inline content:\n%s", got)
	}
}

// issue1657Deck builds a minimal but real .pptx: the OOXML content-type part,
// a presentation part, one slide carrying a known phrase, and a picture in the
// media folder.
func issue1657Deck(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parts := [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types/>`},
		{"ppt/presentation.xml", `<?xml version="1.0" encoding="UTF-8"?><presentation><sldSz cx="12192000" cy="6858000"/></presentation>`},
		{"ppt/slides/slide1.xml", `<?xml version="1.0" encoding="UTF-8"?><sld><txBody><a:t>Quarterly Review</a:t></txBody></sld>`},
		{"ppt/media/image1.png", string(issue1657PNG())},
	}
	for _, part := range parts {
		w, err := zw.Create(part[0])
		if err != nil {
			t.Fatalf("creating %s: %v", part[0], err)
		}
		if _, err := w.Write([]byte(part[1])); err != nil {
			t.Fatalf("writing %s: %v", part[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the deck: %v", err)
	}
	return buf.Bytes()
}

// issue1657PNG is a one-pixel PNG: a real file of its family, small enough to
// compare byte for byte.
func issue1657PNG() []byte {
	const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	out, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic("issue1657PNG: " + err.Error())
	}
	return out
}

// blockKinds names the content blocks a result carried, for a failure message
// that says what arrived instead of what was expected.
func blockKinds(blocks []mcp.Content) string {
	kinds := make([]string, 0, len(blocks))
	for _, block := range blocks {
		kinds = append(kinds, fmt.Sprintf("%T", block))
	}
	return strings.Join(kinds, ", ")
}
