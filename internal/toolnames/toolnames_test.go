package toolnames

import (
	"bufio"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestUnknown(t *testing.T) {
	// A realistic inventory for a deployment that runs the api gateway, the
	// portal, memory and search, so the derived prefixes are the ones a real
	// deployment contributes.
	registered := []string{
		"platform_info", "search", "fetch", "api_discover", "api_invoke_endpoint",
		"manage_asset", "save_asset", "memory_capture", "apply_knowledge", "trino_query",
	}

	tests := []struct {
		name       string
		text       string
		registered []string
		want       []string
	}{
		{
			name:       "a registered name is not reported",
			text:       "Call `api_discover` before `api_invoke_endpoint`.",
			registered: registered,
			want:       nil,
		},
		{
			// The case the six-prefix list this replaced could not see: api_ was
			// not one of its prefixes, so a retired api_ name went unreported.
			name:       "a retired name in a registered family is reported",
			text:       "Use `api_list_endpoints` to enumerate the operations.",
			registered: registered,
			want:       []string{"api_list_endpoints"},
		},
		{
			name:       "ordinary snake_case prose is left alone",
			text:       "The table has first_name and last_name columns; use snake_case naming.",
			registered: registered,
			want:       nil,
		},
		{
			name:       "a script host method is not a tool reference",
			text:       "Persist the cursor with platform.save_state at the end of the run.",
			registered: registered,
			want:       nil,
		},
		{
			name:       "a family this deployment no longer runs is still recognized",
			text:       "Reach the catalog with datahub_search.",
			registered: []string{"trino_query"},
			want:       []string{"datahub_search"},
		},
		{
			name:       "each unknown name is reported once, in order",
			text:       "Use memory_recall, then save_artifact, then memory_recall again.",
			registered: registered,
			want:       []string{"memory_recall", "save_artifact"},
		},
		{
			name:       "empty text reports nothing",
			text:       "",
			registered: registered,
			want:       nil,
		},
		{
			name:       "an empty inventory still recognizes the floor families",
			text:       "Use trino_gone for queries.",
			registered: nil,
			want:       []string{"trino_gone"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unknown(tt.text, tt.registered)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Unknown() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Every name in scripts/retired-tools.txt is a tool this platform once
// registered and no longer does, so every one of them must be reported against
// a live inventory. This is what binds the floor prefix list to the retirement
// list: retiring the last tool of a family leaves nothing in the live inventory
// to contribute its prefix, and without the floor the stale name would pass.
func TestRetiredToolNamesAreReported(t *testing.T) {
	names := readRetiredTools(t)
	if len(names) == 0 {
		t.Fatal("scripts/retired-tools.txt named no tools")
	}
	// The inventory a current deployment registers. Deliberately does not
	// include any retired name.
	registered := []string{
		"platform_info", "list_connections", "platform_find_tools", "search", "fetch",
		"api_discover", "api_invoke_endpoint", "api_export", "s3_list", "s3_object",
		"manage_asset", "save_asset", "manage_resource", "manage_script", "run_script",
		"show_scripts", "manage_prompt", "show_prompts", "manage_feedback", "manage_table",
		"memory_capture", "memory_manage", "apply_knowledge", "trino_query", "trino_execute",
		"datahub_browse", "datahub_update",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			got := Unknown("Call "+name+" to do the thing.", registered)
			if len(got) != 1 || got[0] != name {
				t.Errorf("Unknown() = %v, want [%s]", got, name)
			}
		})
	}
}

// readRetiredTools reads the retirement list, skipping comments and blanks.
func readRetiredTools(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("../../scripts/retired-tools.txt")
	if err != nil {
		t.Fatalf("opening the retirement list: %v", err)
	}
	defer func() { _ = f.Close() }()

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the retirement list: %v", err)
	}
	return names
}
