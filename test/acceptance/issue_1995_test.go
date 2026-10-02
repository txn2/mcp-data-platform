//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #1995: the words a user names output in -- a PowerPoint, a
// deck, a markdown report, a spreadsheet -- reach an agent mapped to the
// platform's assets, with save_asset as the way to make each and no local file,
// install or bucket in its place.
//
// What an agent reads is the surface: the platform_info baseline every
// session starts with, the save_asset and s3_list descriptions in tools/list,
// and the presentations page the baseline names. Each is read through the real
// MCP client.
//
// Wire forms: platform_info takes absent params and an empty object, and both
// are sent; tools/list and fetch take one form each.

// TestIssue1995_TheBaselineMapsOutputWordsToAssets: the first answer of a
// session says a PowerPoint is a presentation made with save_asset.
func TestIssue1995_TheBaselineMapsOutputWordsToAssets(t *testing.T) {
	c := connect(t)
	for _, form := range []map[string]any{nil, {}} {
		info := issue1767Instructions(t, c, form)
		for _, want := range []string{"PowerPoint is a presentation", "a spreadsheet CSV", "make it with `save_asset`",
			"never a local file, an install or a bucket", "name what you make when it differs"} {
			if !strings.Contains(info, want) {
				t.Errorf("platform_info (params %v) does not say %q:\n%s", form, want, info)
			}
		}
	}
}

// TestIssue1995_TheToolDescriptionsPointAtSaveAsset: the description an agent
// chooses a tool by maps the words.
func TestIssue1995_TheToolDescriptionsPointAtSaveAsset(t *testing.T) {
	c := connect(t)
	descriptions := map[string]string{}
	for _, tool := range c.tools() {
		descriptions[tool.Name] = tool.Description
	}
	save := descriptions["save_asset"]
	for _, want := range []string{"PowerPoint", "markdown report", "spreadsheet is text/csv", "no local file system",
		"nothing to install", "say what you are making instead"} {
		if !strings.Contains(save, want) {
			t.Errorf("save_asset's description does not say %q:\n%s", want, save)
		}
	}
}

// TestIssue1995_ThePresentationsPageAnswersAPowerPointRequest: the page the
// baseline names tells an agent asked for a PowerPoint what to make.
func TestIssue1995_ThePresentationsPageAnswersAPowerPointRequest(t *testing.T) {
	c := connect(t)
	page := issue1767Page(t, c)
	for _, want := range []string{"a PowerPoint: each is an HTML asset", "rather than building", "installing software"} {
		if !strings.Contains(page, want) {
			t.Errorf("the presentations page does not say %q", want)
		}
	}
}
