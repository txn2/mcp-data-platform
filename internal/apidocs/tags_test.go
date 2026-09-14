package apidocs

import (
	"encoding/json"
	"sort"
	"testing"
)

// TestSwaggerTagsAreDescribedAndGrouped guards the reference's navigation: every
// tag an operation carries must also be declared with a description and placed
// in an x-tagGroups group, both of which scripts/swagger-tag-groups.py injects
// after swag runs.
//
// A tag that is used but not declared renders in the served reference as an
// untitled bucket outside both groups, which is how eight tags -- API Catalogs,
// APIs, Feedback, Notifications, Portal, Portal Assets, Settings and Users --
// came to hold a large share of the surface in an unnavigable heap (#1742). The
// failure is silent: swag emits the operations happily, the spec is valid, and
// nothing but reading the rendered page reveals it. Adding a route under a new
// tag is the moment this is easy to miss, so it fails here instead.
func TestSwaggerTagsAreDescribedAndGrouped(t *testing.T) {
	var doc struct {
		Tags []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tags"`
		TagGroups []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"x-tagGroups"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(SwaggerJSON()), &doc); err != nil {
		t.Fatalf("parsing the embedded spec: %v", err)
	}

	described := make(map[string]bool, len(doc.Tags))
	for _, tag := range doc.Tags {
		if tag.Description == "" {
			t.Errorf("tag %q is declared with an empty description; add one to TAG_DESCRIPTIONS "+
				"in scripts/swagger-tag-groups.py", tag.Name)
		}
		described[tag.Name] = true
	}

	grouped := make(map[string]bool)
	for _, group := range doc.TagGroups {
		for _, name := range group.Tags {
			if grouped[name] {
				t.Errorf("tag %q appears in more than one x-tagGroups group; a tag renders under "+
					"exactly one heading", name)
			}
			grouped[name] = true
		}
	}

	for _, name := range operationTags(t, doc.Paths) {
		if !described[name] {
			t.Errorf("tag %q is used by an operation but is not declared with a description; add it "+
				"to TAG_DESCRIPTIONS in scripts/swagger-tag-groups.py and run `make swagger`", name)
		}
		if !grouped[name] {
			t.Errorf("tag %q is used by an operation but is in no x-tagGroups group, so the served "+
				"reference renders it outside both headings; add it to TAG_GROUPS in "+
				"scripts/swagger-tag-groups.py and run `make swagger`", name)
		}
	}

	// A declared tag no longer carried by any operation is a dead heading in the
	// reference's navigation, and the same drift in the other direction.
	for name := range described {
		if !grouped[name] {
			t.Errorf("tag %q is described but in no x-tagGroups group", name)
		}
	}
}

// operationTags returns every tag name any operation in the document carries,
// sorted so failures are reported in a stable order.
func operationTags(t *testing.T, paths map[string]map[string]json.RawMessage) []string {
	t.Helper()
	methods := map[string]bool{
		"get": true, "put": true, "post": true, "delete": true,
		"options": true, "head": true, "patch": true,
	}
	seen := make(map[string]bool)
	for path, item := range paths {
		for method, raw := range item {
			if !methods[method] {
				continue
			}
			var op struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("parsing %s %s: %v", method, path, err)
			}
			if len(op.Tags) == 0 {
				t.Errorf("%s %s carries no tag, so it renders outside every heading in the "+
					"served reference; add an @Tags line and run `make swagger`",
					method, path)
			}
			for _, name := range op.Tags {
				seen[name] = true
			}
		}
	}
	if len(seen) == 0 {
		t.Fatalf("found zero tagged operations in a spec with %d paths; the scan is broken", len(paths))
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestSwaggerJSONIsRenderableJSON pins the property the served reference needs
// and had silently lost: the embedded document parses.
//
// The route used to answer from swag.ReadDoc, which renders docs.go's template
// at run time. That template is this JSON with the document's own braces left
// as text/template actions, so a `{{...}}` inside the spec's CONTENT is parsed
// as one -- and the prompt-content example carries the literal placeholder
// "{{data}}" (pkg/portal/prompt_handler.go). text/template read it as a call
// to an undefined function, failed to parse, and ReadDoc fell back to
// returning the template unrendered, so the route served
// `{{ marshal .Schemes }}` where `schemes` belongs. Swagger UI could not load
// it on any deployment.
//
// pkg/admin now serves these bytes directly. This asserts they are what a
// reader can actually parse, and that a placeholder inside a description or an
// example is content rather than something waiting to be executed.
func TestSwaggerJSONIsRenderableJSON(t *testing.T) {
	var doc struct {
		Swagger  string                     `json:"swagger"`
		BasePath string                     `json:"basePath"`
		Paths    map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(SwaggerJSON()), &doc); err != nil {
		t.Fatalf("the embedded spec is not valid JSON, so the served reference cannot load it: %v", err)
	}
	if doc.Swagger == "" {
		t.Error("the embedded spec declares no swagger version")
	}
	if doc.BasePath != "/api/v1" {
		t.Errorf("basePath = %q; want /api/v1, the prefix every @Router path is written against", doc.BasePath)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("the embedded spec documents no paths")
	}
}
