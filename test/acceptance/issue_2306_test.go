//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #2306: an Excalidraw scene is an asset the portal draws and its
// editors edit. What is held here, through the surface an agent, an editor and
// a reader meet:
//
//   - a scene saved as an object and as a string of JSON stores and draws the
//     same scene, and reads back unchanged;
//   - the owner edits the scene in the portal and saves; a new version is
//     written whose diff against the previous one shows the change;
//   - a signed-in reader without edit access and a public share signed out get
//     the drawing and no editor;
//   - a scene naming an external image is refused, from save_asset and from
//     the portal's save;
//   - an invalid scene is refused with the reason, and no asset is created;
//   - the tile is the drawing, light and dark.
//
// Wire forms: save_asset's `content` is sent as a JSON object and as a JSON
// string holding the same scene, as literal tools/call params; `name`,
// `content_type` and `purpose` are typed string. The portal's save is a PUT of
// the scene as the request body.

const issue2306Purpose = "Acceptance for #2306: an Excalidraw scene drawn and edited in the portal."

const issue2306Scene = `{"type":"excalidraw","version":2,"source":"acceptance","elements":[` +
	`{"id":"box","type":"rectangle","x":0,"y":0,"width":200,"height":90,"strokeColor":"#1e1e1e","backgroundColor":"#a5d8ff","fillStyle":"hachure","roughness":1,"seed":1,"version":1},` +
	`{"id":"label","type":"text","x":50,"y":30,"width":100,"height":25,"text":"Ingest","fontSize":20,"seed":2,"version":1},` +
	`{"id":"arrow","type":"arrow","x":210,"y":45,"width":140,"height":0,"points":[[0,0],[140,0]],"seed":3,"version":1}` +
	`],"appState":{"viewBackgroundColor":"#ffffff"},"files":{}}`

func issue2306Object(t *testing.T) map[string]any {
	t.Helper()
	var scene map[string]any
	if err := json.Unmarshal([]byte(issue2306Scene), &scene); err != nil {
		t.Fatal(err)
	}
	return scene
}

func TestIssue2306_ASceneSentEitherWayStoresAndDrawsTheSameScene(t *testing.T) {
	c := connect(t)
	asString := saveDrawn(t, c, "Acceptance 2306 string", "application/vnd.excalidraw+json", issue2306Scene, issue2306Purpose)
	asObject := saveDrawn(t, c, "Acceptance 2306 object", "application/vnd.excalidraw+json", issue2306Object(t), issue2306Purpose)
	var a, b any
	_ = json.Unmarshal([]byte(storedContent(t, c, asString)), &a)
	_ = json.Unmarshal([]byte(storedContent(t, c, asObject)), &b)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("the scene sent as a string and as an object stored differently:\n%v\n%v", a, b)
	}
	if storedContent(t, c, asString) != issue2306Scene {
		t.Errorf("get_content did not return the scene sent as a string unchanged")
	}
	runDrawnSuite(t, []drawnCase{
		{Name: "a scene sent as a string", Kind: "excalidraw", Asset: asString, SourceLabel: "View Scene", SourceText: "Ingest"},
		{Name: "a scene sent as an object", Kind: "excalidraw", Asset: asObject, SourceLabel: "View Scene", SourceText: "Ingest"},
	})
}

func TestIssue2306_AnEditorEditsAndSavesAndReadersGetNoEditor(t *testing.T) {
	c := connect(t)
	id := saveDrawn(t, c, "Acceptance 2306 edit", "application/vnd.excalidraw+json", issue2306Scene, issue2306Purpose)
	runDrawnSuite(t, []drawnCase{{
		Name: "a scene its owner edits", Kind: "excalidraw", Asset: id,
		PublicShare: issue2373Share(t, c, id, "public"), ViewerShare: issue2373Share(t, c, id, "authenticated"),
		SourceLabel: "View Scene", SourceText: "Ingest", EditorForOwnerOnly: true, DrawAndSave: true,
	}})
	versions := c.call("manage_asset", map[string]any{"action": "list_versions", "asset_id": id, "purpose": issue2306Purpose})
	listed, _ := versions["versions"].([]any)
	if len(listed) < 2 {
		t.Fatalf("the portal's save wrote no new version: %v", versions)
	}
	diff := c.call("manage_asset", map[string]any{"action": "diff", "asset_id": id, "from_version": 1, "to_version": 2, "purpose": issue2306Purpose})
	text := fmt.Sprint(diff)
	if !strings.Contains(text, `"rectangle"`) || !strings.Contains(text, "+") {
		t.Errorf("the diff between the versions does not show the drawn rectangle: %v", diff)
	}
}

func TestIssue2306_AnExternalImageIsRefusedFromTheToolAndThePortal(t *testing.T) {
	c := connect(t)
	external := strings.Replace(issue2306Scene, `"files":{}`, `"files":{"f1":{"mimeType":"image/png","dataURL":"https://evil.example.com/a.png"}}`, 1)
	if text := refusedSave(t, c, "application/vnd.excalidraw+json", external, issue2306Purpose); !strings.Contains(text, "file f1") {
		t.Errorf("save_asset's refusal does not name the image: %s", text)
	}
	id := saveDrawn(t, c, "Acceptance 2306 portal", "application/vnd.excalidraw+json", issue2306Scene, issue2306Purpose)
	status, out := c.rest(http.MethodPut, "/api/v1/portal/assets/"+id+"/content", strings.NewReader(external))
	if status != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out), "file f1") {
		t.Errorf("the portal's save of an external image answered %d %v, want 422 naming it", status, out)
	}
	if storedContent(t, c, id) != issue2306Scene {
		t.Errorf("a refused portal save changed the scene")
	}
}

func TestIssue2306_AnInvalidSceneIsRefusedWithTheReason(t *testing.T) {
	c := connect(t)
	text := refusedSave(t, c, "application/vnd.excalidraw+json", `{"elements":[]}`, issue2306Purpose)
	if !strings.Contains(text, `"type": "excalidraw"`) {
		t.Errorf("the refusal does not say why: %s", text)
	}
}

func TestIssue2306_TheTileIsTheDrawingLightAndDark(t *testing.T) {
	c := connect(t)
	id := saveDrawn(t, c, "Acceptance 2306 tile", "application/vnd.excalidraw+json", issue2306Scene, issue2306Purpose)
	for _, variant := range []string{"light", "dark"} {
		if ink := issue2373Ink(issue2068Tile(t, c, id, variant)); ink < 0.005 {
			t.Errorf("the %s tile is blank (%.4f)", variant, ink)
		}
	}
}
