package sourcecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// excalidrawFramedTypes are element types that frame a web page, which the
// portal never loads on a reader's behalf.
var excalidrawFramedTypes = map[string]bool{"embeddable": true, "iframe": true}

// excalidrawScene is the part of a scene the check reads.
type excalidrawScene struct {
	Type     string                     `json:"type"`
	Elements []map[string]any           `json:"elements"`
	Files    map[string]json.RawMessage `json:"files"`
}

// checkExcalidraw refuses a scene that is not one, an element framing a page,
// and any URL other than an inline data: image or a managed-resource
// reference: an image file's dataURL and an element's link (#2306). The scene
// is drawn in the reader's browser, and the editor writes back through this
// same check.
func checkExcalidraw(body string) error {
	var scene excalidrawScene
	if err := json.Unmarshal([]byte(body), &scene); err != nil {
		return fmt.Errorf("the Excalidraw scene is not valid JSON: %w", err)
	}
	if scene.Type != "excalidraw" || scene.Elements == nil {
		return errors.New(`the Excalidraw scene must be an object with "type": "excalidraw" and an "elements" array`)
	}
	framed, urls, err := excalidrawElements(scene.Elements)
	if err != nil {
		return err
	}
	urls = append(urls, excalidrawFiles(scene.Files)...)
	if len(framed) > 0 {
		return fmt.Errorf("the Excalidraw scene frames a web page (%s); the portal draws a scene without them, so remove the element", named(framed))
	}
	if len(urls) > 0 {
		return fmt.Errorf("the Excalidraw scene names %s, which is not an inline image or a managed resource. "+
			"An image is stored in the scene's files as a data:image/ URL, and a link names an mcp:// resource or an mcp:asset: reference", named(urls))
	}
	return nil
}

// excalidrawElements returns the element types that frame a page and the
// links that name something other than a reference.
func excalidrawElements(elements []map[string]any) (framed, urls []string, err error) {
	for i, el := range elements {
		t, _ := el["type"].(string)
		if t == "" {
			return nil, nil, fmt.Errorf("element %d of the Excalidraw scene has no type", i)
		}
		if excalidrawFramedTypes[t] {
			framed = append(framed, t)
		}
		if link, ok := el["link"].(string); ok && link != "" && !isReference(strings.ToLower(link)) {
			urls = append(urls, link)
		}
	}
	return framed, urls, nil
}

// excalidrawFiles names every image file that is not an inline data: image.
func excalidrawFiles(files map[string]json.RawMessage) []string {
	var out []string
	for id, raw := range files {
		var f struct {
			DataURL string `json:"dataURL"`
		}
		if json.Unmarshal(raw, &f) != nil || !strings.HasPrefix(strings.ToLower(f.DataURL), "data:image/") {
			out = append(out, "file "+id)
		}
	}
	return out
}
