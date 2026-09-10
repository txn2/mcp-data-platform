package docread_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/docread"
)

// pptx builds a presentation-shaped archive: the OOXML content-type part, a
// slide under the presentation root, and a picture in the media folder.
func pptx(t *testing.T) []byte {
	t.Helper()
	return zipOf(t,
		[2]string{"[Content_Types].xml", `<?xml version="1.0"?><Types/>`},
		[2]string{"ppt/presentation.xml", `<?xml version="1.0"?><presentation><sldSz cx="12192000"/></presentation>`},
		[2]string{"ppt/slides/slide1.xml", `<?xml version="1.0"?><sld><txBody><a:t>Quarterly Review</a:t></txBody></sld>`},
		[2]string{"ppt/media/image1.png", string(pngBytes)},
	)
}

func TestReadAPresentationReturnsItsParts(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "application/zip", "deck.pptx", pptx(t), 1<<20)
	if got.Form != docread.FormText {
		t.Fatalf("a deck did not read as text: %+v", got)
	}
	for _, want := range []string{
		"PowerPoint presentation",
		"ppt/slides/slide1.xml",
		"Quarterly Review",
		"ppt/presentation.xml",
	} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("the rendering is missing %q:\n%s", want, got.Text)
		}
	}
}

// The picture in a deck is not text, so it is named in the inventory rather
// than rendered. A reader shown only the XML would conclude the slide has no
// picture on it.
func TestReadAPresentationNamesTheMembersItDidNotRender(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "application/zip", "deck.pptx", pptx(t), 1<<20)
	if !strings.Contains(got.Text, "Not rendered above") || !strings.Contains(got.Text, "ppt/media/image1.png") {
		t.Fatalf("the picture was not named:\n%s", got.Text)
	}
}

func TestReadNamesEachDocumentFamily(t *testing.T) {
	cases := []struct {
		name    string
		members [][2]string
		want    string
	}{
		{"word", [][2]string{{"[Content_Types].xml", "<Types/>"}, {"word/document.xml", "<document/>"}}, "Word document"},
		{"excel", [][2]string{{"[Content_Types].xml", "<Types/>"}, {"xl/workbook.xml", "<workbook/>"}}, "Excel workbook"},
		{"opendocument", [][2]string{{"mimetype", "application/vnd.oasis.opendocument.presentation"}, {"content.xml", "<content/>"}}, "OpenDocument file"},
		{"plain zip", [][2]string{{"notes.txt", "just some notes"}}, "zip archive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := docread.New(nil).Read(context.Background(), "application/zip", "f.zip", zipOf(t, tc.members...), 1<<20)
			if !strings.Contains(got.Text, tc.want) {
				t.Fatalf("expected %q in:\n%s", tc.want, got.Text)
			}
		})
	}
}

// An OOXML archive with no recognizable body root is still an OOXML archive.
func TestReadNamesAnUnfamiliarOfficeArchive(t *testing.T) {
	body := zipOf(t, [2]string{"[Content_Types].xml", "<Types/>"}, [2]string{"other/thing.xml", "<thing/>"})
	got := docread.New(nil).Read(context.Background(), "application/zip", "f.zip", body, 1<<20)
	if !strings.Contains(got.Text, "Office Open XML") {
		t.Fatalf("an unfamiliar office archive was not named:\n%s", got.Text)
	}
}

// The budget stops the rendering, and what it stopped must be visible: an
// agent that knows four parts were dropped asks for them, where one shown
// twelve of sixteen with no marker does not.
func TestReadAnArchiveOverBudgetReportsWhatItLeftOut(t *testing.T) {
	members := make([][2]string, 0, 12)
	for i := range 12 {
		members = append(members, [2]string{
			fmt.Sprintf("ppt/slides/slide%d.xml", i+1),
			"<sld>" + strings.Repeat("z", 400) + "</sld>",
		})
	}
	got := docread.New(nil).Read(context.Background(), "application/zip", "deck.pptx", zipOf(t, members...), 1600)
	if !got.Truncated {
		t.Fatalf("an over-budget archive was not reported as truncated:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "Not rendered above") {
		t.Fatalf("the dropped parts were not named:\n%s", got.Text)
	}
	if len(got.Text) > 1600 {
		t.Fatalf("the budget was exceeded: %d bytes", len(got.Text))
	}
}

// A part cut mid-element must say so, or it reads as a malformed part rather
// than a bounded one.
func TestReadMarksAPartItCutShort(t *testing.T) {
	body := zipOf(t, [2]string{"word/document.xml", "<document>" + strings.Repeat("w", 4000) + "</document>"})
	got := docread.New(nil).Read(context.Background(), "application/zip", "d.docx", body, 900)
	if !strings.Contains(got.Text, "cut short") {
		t.Fatalf("a cut part was not marked:\n%s", got.Text)
	}
}

// An archive holding nothing textual still answers: its inventory is what the
// file is, and naming the pictures beats reporting nothing.
func TestReadAnArchiveOfPicturesReportsItsInventory(t *testing.T) {
	body := zipOf(t, [2]string{"a.png", string(pngBytes)}, [2]string{"b.png", string(pngBytes)})
	got := docread.New(nil).Read(context.Background(), "application/zip", "pics.zip", body, 1<<20)
	if got.Form != docread.FormText {
		t.Fatalf("an archive of pictures should still report its inventory: %+v", got)
	}
	if !strings.Contains(got.Text, "a.png") || !strings.Contains(got.Text, "b.png") {
		t.Fatalf("a member was not named:\n%s", got.Text)
	}
}

// Below the floor there is no room for a rendering worth reading, so the bytes
// are the better answer.
func TestReadAnArchiveWithNoRoomFallsToItsBytes(t *testing.T) {
	body := zipOf(t, [2]string{"a.txt", "hello"})
	got := docread.New(nil).Read(context.Background(), "application/zip", "small.zip", body, 40)
	if got.Form != docread.FormOpaque || got.Note == "" {
		t.Fatalf("a budget below the floor did not fall to its bytes with a reason: %+v", got)
	}
}

func TestReadAnUnopenableArchiveFallsToItsBytes(t *testing.T) {
	// A zip local file header over bytes that are not an archive.
	body := append([]byte{'P', 'K', 0x03, 0x04}, []byte("this is not really a zip")...)
	got := docread.New(nil).Read(context.Background(), "application/zip", "broken.zip", body, 1<<20)
	if got.Form != docread.FormOpaque || got.Note == "" {
		t.Fatalf("a broken archive did not fall to its bytes with a reason: %+v", got)
	}
}

// A directory entry is not a part of the document and must not produce an
// empty banner.
func TestReadSkipsDirectoryEntries(t *testing.T) {
	body := zipOf(t, [2]string{"ppt/", ""}, [2]string{"ppt/slides/slide1.xml", "<sld/>"})
	got := docread.New(nil).Read(context.Background(), "application/zip", "deck.pptx", body, 1<<20)
	if strings.Contains(got.Text, "===== ppt/ (") {
		t.Fatalf("a directory entry was rendered:\n%s", got.Text)
	}
}

// The budget is a hard ceiling at every size, not only at the sizes the other
// cases happen to use. This is the invariant the banners and the inventory
// each escaped before they were budgeted.
func TestReadNeverExceedsTheBudget(t *testing.T) {
	members := make([][2]string, 0, 20)
	for i := range 20 {
		members = append(members,
			[2]string{fmt.Sprintf("ppt/slides/slide%d.xml", i+1), "<sld>" + strings.Repeat("q", 300) + "</sld>"},
			[2]string{fmt.Sprintf("ppt/media/image%d.png", i+1), string(pngBytes)},
		)
	}
	body := zipOf(t, members...)
	for limit := 512; limit <= 20000; limit += 373 {
		got := docread.New(nil).Read(context.Background(), "application/zip", "deck.pptx", body, limit)
		if len(got.Text) > limit {
			t.Fatalf("limit %d produced %d bytes", limit, len(got.Text))
		}
	}
}

// An archive carrying two OOXML roots -- a workbook embedded in a deck -- must
// be named the same way on every read.
func TestReadNamesAMixedOfficeArchiveDeterministically(t *testing.T) {
	body := zipOf(t,
		[2]string{"[Content_Types].xml", "<Types/>"},
		[2]string{"xl/workbook.xml", "<workbook/>"},
		[2]string{"ppt/presentation.xml", "<presentation/>"},
	)
	first := docread.New(nil).Read(context.Background(), "application/zip", "f.pptx", body, 1<<20)
	for range 20 {
		got := docread.New(nil).Read(context.Background(), "application/zip", "f.pptx", body, 1<<20)
		if got.Text != first.Text {
			t.Fatalf("the same archive read two ways:\n%s\n---\n%s", first.Text, got.Text)
		}
	}
}

// A member whose compressed data is damaged is skipped and named in the
// inventory. One unreadable part must not cost the reader the rest of the
// document.
func TestReadSkipsAMemberItCannotDecompress(t *testing.T) {
	body := zipOf(t,
		[2]string{"word/document.xml", "<document>" + incompressibleText(4000) + "</document>"},
		[2]string{"word/settings.xml", "<settings/>"},
	)
	corrupted := corruptFirstMemberData(t, body)

	got := docread.New(nil).Read(context.Background(), "application/zip", "d.docx", corrupted, 1<<20)
	if got.Form != docread.FormText {
		t.Fatalf("a damaged member cost the whole document: %+v", got)
	}

	if !strings.Contains(got.Text, "word/settings.xml") || !strings.Contains(got.Text, "<settings/>") {
		t.Fatalf("the readable part did not survive:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "word/document.xml") {
		t.Fatalf("the damaged part was not named:\n%s", got.Text)
	}
}

// corruptFirstMemberData flips the bytes of the first member's compressed data
// while leaving every header intact, so the archive opens and the member does
// not.
func corruptFirstMemberData(t *testing.T, archive []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("reading the archive back: %v", err)
	}
	offset, err := zr.File[0].DataOffset()
	if err != nil {
		t.Fatalf("locating the member's data: %v", err)
	}
	// A fixed, small span, and the member is built from text that does not
	// compress, so the damage stays inside its data and the entries after it
	// are untouched.
	out := bytes.Clone(archive)
	for i := offset; i < offset+corruptSpan && int(i) < len(out); i++ {
		out[i] ^= 0xff
	}
	return out
}

// corruptSpan is how many bytes of a member's compressed data the damage test
// flips.
const corruptSpan = 64

// incompressibleText returns n characters of text that deflate cannot shrink
// much, so a member built from it is larger compressed than corruptSpan.
func incompressibleText(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	out := make([]byte, n)
	state := uint32(2463534242)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = alphabet[state%uint32(len(alphabet))]
	}
	return string(out)
}
