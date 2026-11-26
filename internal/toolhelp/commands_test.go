package toolhelp

import (
	"slices"
	"testing"
)

// TestUseOf reads the description forms the platform's schemas write (#2826).
func TestUseOf(t *testing.T) {
	cmds := []string{"get", "update", "delete", "revert", "revoke_share", "list", "create", "extract", "register", "check", "unregister", "consolidate", "forget", "reply", "edit", "publish", "patch", "share", "locate", "get_content", "outline", "diff"}
	for desc, want := range map[string]struct{ named, required []string }{
		"Asset ID (required for get, update, delete, list_versions, revert). More.":                                     {[]string{"get", "update", "delete", "revert"}, []string{"get", "update", "delete", "revert"}},
		"Share ID to revoke (required for revoke_share). Call list_shares.":                                             {[]string{"revoke_share"}, []string{"revoke_share"}},
		"Version number (required for revert action)":                                                                   {[]string{"revert"}, []string{"revert"}},
		"How long a link lasts. Required for access_mode public and refused else.":                                      {nil, nil},
		"Memory record ID. Required for 'update' and 'forget'; for 'consolidate' it is the record to KEEP.":             {[]string{"update", "forget", "consolidate"}, []string{"update", "forget"}},
		"Human-readable name (required for create; update changes it).":                                                 {[]string{"create"}, []string{"create"}},
		"REQUIRED for create, and not detected for you.":                                                                {[]string{"create"}, []string{"create"}},
		"The file. Required for register and list; unregister takes registration_id instead.":                           {[]string{"register", "list"}, []string{"register", "list"}},
		"Comment text (required for reply, for create, where it is the first comment, and for edit, where it replaces)": {[]string{"reply", "create", "edit"}, []string{"reply", "create", "edit"}},
		"The folder. Required for create and for extract, where members go. For list it is the root.":                   {[]string{"create", "extract", "list"}, []string{"create", "extract"}},
		"For register: save a corrected version.":                                                                       {[]string{"register"}, nil},
		"publish: the files. Required for publish.":                                                                     {[]string{"publish"}, []string{"publish"}},
		"publish: the files. Required.":                                                                                 {[]string{"publish"}, []string{"publish"}},
		"The site, by name or id.":                                                                                      {nil, nil},
		"Wait time, used for polling.":                                                                                  {nil, nil},
		// #2832: a parenthesis that opens with the commands.
		"Ordered edits to apply (patch action). Each edit is anchored.":                                                  {[]string{"patch"}, nil},
		"Summary of the change (update and patch actions). Defaults to a generated summary.":                             {[]string{"update", "patch"}, nil},
		"New content type (update action, only when replacing content; accepts the same types), or for list the type":    {[]string{"update", "list"}, nil},
		"Several assets (update, delete and share actions; at most 500).":                                                {[]string{"update", "delete", "share"}, nil},
		"Which element (locate and get_content): 'first' or 'last'.":                                                     {[]string{"locate", "get_content"}, nil},
		"Which element ('locate' or 'get_content').":                                                                     {[]string{"locate", "get_content"}, nil},
		"Tags (create, update, where they replace the file's tags).":                                                     {[]string{"create", "update"}, nil},
		"Why it changed (replace_content, and an extract that records a member).":                                        {nil, nil},
		"Asset ID (required for get). The content actions (get_content, outline, locate, diff, patch) also take a site.": {[]string{"get", "get_content", "outline", "locate", "diff", "patch"}, []string{"get"}},
		"Older side of a comparison (diff action).":                                                                      {[]string{"diff"}, nil},
		"How much text each match reports (locate action; default 160).":                                                 {[]string{"locate"}, nil},
		"The ID (asset_id or list_versions).":                                                                            {nil, nil},
		"Filter to threads that do (true) or do not (false) need it.":                                                    {nil, nil},
		"A list (list of names).": {nil, nil},
		"Media (publish, delete and revert leave it alone, and it counts for nothing).": {nil, nil},
		"Both (update and more).":         {nil, nil},
		"Bounded (default 50, max 200).":  {nil, nil},
		"An address (mcp:resource:<id>).": {nil, nil},
	} {
		u := useOf(desc, cmds)
		if !slices.Equal(u.named, want.named) || !slices.Equal(u.requiredFor, want.required) {
			t.Errorf("%q: named %v required %v, want %v %v", desc, u.named, u.requiredFor, want.named, want.required)
		}
	}
}

func TestRequiredFor(t *testing.T) {
	named := paramUse{named: []string{"get"}}
	if !requiredFor(paramUse{}, true, "list") || requiredFor(paramUse{}, false, "list") {
		t.Error("an unnamed parameter is required exactly where the schema says")
	}
	if !requiredFor(named, true, "get") || requiredFor(named, true, "list") {
		t.Error("a schema-required parameter naming get is required for get alone")
	}
	if requiredFor(named, false, "get") || !requiredFor(paramUse{named: []string{"get"}, requiredFor: []string{"get"}}, false, "get") {
		t.Error("a description's required list decides an optional parameter")
	}
}

func TestHelpField(t *testing.T) {
	if f, ok := HelpField(assetSchema()); !ok || f != "action" {
		t.Errorf("HelpField = %q %v, want action", f, ok)
	}
	if _, ok := HelpField(map[string]any{"properties": map[string]any{"sql": map[string]any{"type": "string"}}}); ok {
		t.Error("a tool with no command enum has a help field")
	}
}
