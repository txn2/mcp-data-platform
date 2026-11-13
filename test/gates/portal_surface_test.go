package gates

// scripts/portal-surface-check.py warns when a diff registers an MCP tool or
// adds a top-level config section and changes nothing under ui/src (#2560):
// manage_site reached a release with no Sites page, and the API gateway's
// first five pull requests with no way to add an api connection.

import "testing"

const portalSurfaceScript = "scripts/portal-surface-check.py"

// portalSurfaceBase is the tree the change branch starts from: one toolkit
// registering one tool, the tool table naming it and platform_info, a config
// with two sections, and an exemption list naming platform_info. A tool the
// base's table names is not new, however its registration is rewritten.
func portalSurfaceBase(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"scripts/acceptance-evidence.py": readRepoFile(t, "scripts/acceptance-evidence.py"),
		"scripts/portal-surface-exempt.txt": "# fixture\n" +
			"tool platform_info Instructions to the model.\n",
		"test/toolsurface/toolsurface.go": "package toolsurface\n\nvar Annotations = map[string]Expectation{\n" +
			"\t\"platform_info\": {ReadOnly: true},\n\t\"widget_list\":   {ReadOnly: true},\n}\n",
		"pkg/platform/config.go": "package platform\n\ntype Config struct {\n" +
			"\tServer ServerConfig `yaml:\"server\"`\n\tPortal PortalConfig `yaml:\"portal\"`\n}\n",
		"pkg/toolkits/widget/widget.go": "package widget\n\nconst ToolList = \"widget_list\"\n\n" +
			"func (t *Toolkit) RegisterTools(s *mcp.Server) {\n\tmcp.AddTool(s, &mcp.Tool{Name: ToolList}, t.list)\n}\n",
	}
}

// A second tool, registered the way a toolkit registers one.
const widgetWithCreate = "package widget\n\nconst (\n\tToolList   = \"widget_list\"\n\tToolCreate = \"widget_create\"\n)\n\n" +
	"func (t *Toolkit) RegisterTools(s *mcp.Server) {\n\tmcp.AddTool(s, &mcp.Tool{Name: ToolList}, t.list)\n" +
	"\tmcp.AddTool(s, &mcp.Tool{Name: ToolCreate}, t.create)\n}\n"

// The tool table naming the second tool, as the change registering it must.
const tableWithCreate = "package toolsurface\n\nvar Annotations = map[string]Expectation{\n" +
	"\t\"platform_info\": {ReadOnly: true},\n\t\"widget_list\":   {ReadOnly: true},\n\t\"widget_create\": {},\n}\n"

const configWithWidgets = "package platform\n\ntype Config struct {\n" +
	"\tServer  ServerConfig  `yaml:\"server\"`\n\tPortal  PortalConfig  `yaml:\"portal\"`\n\tWidgets WidgetsConfig `yaml:\"widgets\"`\n}\n"

func TestPortalSurfaceCheck(t *testing.T) {
	t.Parallel()
	const warning = "WARNING portal-surface-check"
	cases := []struct {
		name  string
		files map[string]string
		env   []string
		expect
	}{
		{
			name:  "a tool added with no ui/src change is reported by name",
			files: map[string]string{"pkg/toolkits/widget/widget.go": widgetWithCreate},
			pass:  true, want: []string{warning, "tool widget_create (registered in pkg/toolkits/widget/widget.go)", "blocking for an AI agent"},
		},
		{
			name: "the same tool with a ui/src change is quiet",
			files: map[string]string{
				"pkg/toolkits/widget/widget.go": widgetWithCreate,
				"ui/src/pages/Widgets.tsx":      "export const Widgets = () => null;\n",
			},
			pass: true, want: []string{"OK portal-surface-check"}, mustNot: []string{warning},
		},
		{
			name: "the same tool listed with a reason is quiet",
			files: map[string]string{
				"pkg/toolkits/widget/widget.go":   widgetWithCreate,
				"test/toolsurface/toolsurface.go": tableWithCreate,
				"scripts/portal-surface-exempt.txt": "tool platform_info Instructions to the model.\n" +
					"tool widget_create Called by other tools only; a person never starts it.\n",
			},
			pass: true, want: []string{"OK portal-surface-check"}, mustNot: []string{warning},
		},
		{
			name:  "a config section added with no ui/src change is reported",
			files: map[string]string{"pkg/platform/config.go": configWithWidgets},
			pass:  true, want: []string{warning, "config section widgets:"}, mustNot: []string{"tool widget"},
		},
		{
			name:  "a tool in a new file git does not track yet is reported",
			files: map[string]string{"pkg/toolkits/gadget/gadget.go": "package gadget\n\nconst (\n\tToolRun = \"gadget_run\"\n)\n\nfunc (t *Toolkit) RegisterTools(s *mcp.Server) {\n\tmcp.AddTool(s, &mcp.Tool{Name: ToolRun}, t.run)\n}\n"},
			pass:  true, want: []string{warning, "tool gadget_run"},
		},
		{
			name: "a tool the tool table gained is reported",
			files: map[string]string{"test/toolsurface/toolsurface.go": "package toolsurface\n\nvar Annotations = map[string]Expectation{\n" +
				"\t\"platform_info\": {ReadOnly: true},\n\t\"widget_list\":   {ReadOnly: true},\n\t\"widget_export\": {},\n}\n"},
			pass: true, want: []string{warning, "tool widget_export (registered in test/toolsurface/toolsurface.go)"},
		},
		{
			name: "an exemption naming a tool that is no longer registered fails, naming it",
			files: map[string]string{"scripts/portal-surface-exempt.txt": "tool platform_info Instructions to the model.\n" +
				"tool widget_delete It went away.\n"},
			want: []string{"FAIL portal-surface-check: tool widget_delete is exempt, and the platform registers no tool of that name"},
		},
		{
			name:  "an exemption with no reason fails",
			files: map[string]string{"scripts/portal-surface-exempt.txt": "tool platform_info\n"},
			want:  []string{"FAIL portal-surface-check: line 1:"},
		},
		{
			name:  "a base branch that cannot be resolved fails rather than skipping",
			files: map[string]string{"pkg/toolkits/widget/widget.go": widgetWithCreate},
			env:   []string{"BASE_BRANCH=no-such-branch"},
			want:  []string{"FAIL portal-surface-check: cannot resolve base branch 'no-such-branch'"},
		},
		{
			name:  "a change with no tool and no section is quiet",
			files: map[string]string{"pkg/toolkits/widget/helper.go": "package widget\n\nfunc helper() {}\n"},
			pass:  true, want: []string{"OK portal-surface-check"}, mustNot: []string{warning},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newGitRepo(t, portalSurfaceScript, portalSurfaceBase(t))
			for rel, body := range tc.files {
				writeFile(t, root, rel, body)
			}
			out, passed := runScript(t, root, []string{portalSurfaceScript}, tc.env)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}
