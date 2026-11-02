//go:build integration

package acceptance

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Issue #2308: a Lottie animation plays in the portal. What is held here,
// through the surface an agent and a reader meet:
//
//   - Lottie JSON sent as an object and as a string of JSON stores and plays
//     the same animation on the asset page, an authenticated share and a public
//     share signed out, and reads back unchanged; an uploaded .lottie file
//     plays on its resource page;
//   - play, pause and scrub work on the asset page;
//   - a source with an expression is refused, and so is one naming an
//     external image; no asset is created;
//   - the tile is the last frame, light and dark.
//
// Wire forms: save_asset's `content` is sent as a JSON object and as a JSON
// string holding the same animation, as literal tools/call params; `name`,
// `content_type` and `purpose` are typed string. The .lottie file is a
// multipart upload to /api/v1/resources.
//
// The ticket said fetch returns the source. fetch on an asset returns its
// record (#2377); the read is manage_asset get_content.

const issue2308Purpose = "Acceptance for #2308: a Lottie animation played by the portal."

// issue2308Pulse is a circle growing over two seconds.
const issue2308Pulse = `{"v":"5.7.0","fr":30,"ip":0,"op":60,"w":200,"h":200,"layers":[{"ty":4,"ip":0,"op":60,"st":0,"ks":{` +
	`"o":{"a":0,"k":100},"r":{"a":0,"k":0},"p":{"a":0,"k":[100,100,0]},"a":{"a":0,"k":[0,0,0]},` +
	`"s":{"a":1,"k":[{"t":0,"s":[40,40,100],"e":[100,100,100]},{"t":60,"s":[100,100,100]}]}},` +
	`"shapes":[{"ty":"gr","it":[{"ty":"el","p":{"a":0,"k":[0,0]},"s":{"a":0,"k":[140,140]}},` +
	`{"ty":"fl","c":{"a":0,"k":[0.31,0.47,0.65,1]},"o":{"a":0,"k":100}},` +
	`{"ty":"tr","p":{"a":0,"k":[0,0]},"a":{"a":0,"k":[0,0]},"s":{"a":0,"k":[100,100]},"r":{"a":0,"k":0},"o":{"a":0,"k":100}}]}]}]}`

// issue2308DotLottie packs the animation as a dotLottie archive.
func issue2308DotLottie(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	z := zip.NewWriter(buf)
	for name, body := range map[string]string{
		"manifest.json":         `{"version":"1","animations":[{"id":"pulse"}]}`,
		"animations/pulse.json": issue2308Pulse,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIssue2308_AnAnimationSentEveryWayPlaysWhereItIsRead(t *testing.T) {
	c := connect(t)
	var object map[string]any
	if err := json.Unmarshal([]byte(issue2308Pulse), &object); err != nil {
		t.Fatal(err)
	}
	asString := saveDrawn(t, c, "Acceptance 2308 string", "application/vnd.lottie+json", issue2308Pulse, issue2308Purpose)
	asObject := saveDrawn(t, c, "Acceptance 2308 object", "application/vnd.lottie+json", object, issue2308Purpose)
	if got := storedContent(t, c, asString); got != issue2308Pulse {
		t.Errorf("get_content did not return the animation unchanged:\n%s", got)
	}
	var a, b any
	_ = json.Unmarshal([]byte(storedContent(t, c, asString)), &a)
	_ = json.Unmarshal([]byte(storedContent(t, c, asObject)), &b)
	if len(storedContent(t, c, asObject)) == 0 || a == nil || b == nil {
		t.Fatalf("an animation did not store")
	}
	upload, _ := issue2395Upload(t, c, "pulse.lottie", "", issue2308DotLottie(t))
	status, res := c.rest("GET", "/api/v1/resources/"+upload, nil)
	if status != 200 || res["mime_type"] != "application/zip+dotlottie" {
		t.Errorf("the uploaded .lottie was stored as %v (%d), want application/zip+dotlottie", res["mime_type"], status)
	}
	runDrawnSuite(t, []drawnCase{
		{
			Name: "Lottie JSON sent as a string", Kind: "lottie", Asset: asString,
			AuthShare: issue2373Share(t, c, asString, "authenticated"), PublicShare: issue2373Share(t, c, asString, "public"),
			SourceLabel: "View JSON", SourceText: `"fr"`, Playback: true,
		},
		{
			Name: "Lottie JSON sent as an object", Kind: "lottie", Asset: asObject,
			PublicShare: issue2373Share(t, c, asObject, "public"), SourceLabel: "View JSON", SourceText: `"fr"`,
		},
	})
	runDrawnSuite(t, []drawnCase{{
		// A resource page has no source toggle: an empty label skips that step.
		Name: "an uploaded .lottie file", Kind: "lottie", Asset: upload, Page: "/portal/resources/" + upload,
	}})
}

func TestIssue2308_AnExpressionOrAnExternalImageIsRefused(t *testing.T) {
	c := connect(t)
	withExpression := strings.Replace(issue2308Pulse, `"r":{"a":0,"k":0}`, `"r":{"a":0,"k":0,"x":"var $bm_rt = time * 360;"}`, 1)
	if text := refusedSave(t, c, "application/vnd.lottie+json", withExpression, issue2308Purpose); !strings.Contains(text, "expression") {
		t.Errorf("the refusal does not name the expression: %s", text)
	}
	withImage := strings.Replace(issue2308Pulse, `"layers":`, `"assets":[{"id":"image_0","w":10,"h":10,"u":"https://cdn.example.com/","p":"a.png","e":0}],"layers":`, 1)
	if text := refusedSave(t, c, "application/vnd.lottie+json", withImage, issue2308Purpose); !strings.Contains(text, "https://cdn.example.com/a.png") {
		t.Errorf("the refusal does not name the image: %s", text)
	}
}

func TestIssue2308_TheTileIsTheLastFrameLightAndDark(t *testing.T) {
	c := connect(t)
	id := saveDrawn(t, c, "Acceptance 2308 tile", "application/vnd.lottie+json", issue2308Pulse, issue2308Purpose)
	for _, variant := range []string{"light", "dark"} {
		if ink := issue2373Ink(issue2068Tile(t, c, id, variant)); ink < 0.005 {
			t.Errorf("the %s tile is blank (%.4f)", variant, ink)
		}
	}
}
