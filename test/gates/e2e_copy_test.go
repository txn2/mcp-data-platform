package gates

// scripts/e2e-copy-check.py fails when a Playwright spec asserts UI copy the
// diff removed from ui/src (#1977). On the #1970 branch the first verify failed
// in frontend-e2e on /only person who sees it/, a sentence the diff had
// rewritten, fifteen minutes in. What the check must never do is fail on copy
// the diff moved or reformatted, or on a spec helper's /\s+/.

import (
	"strings"
	"testing"
)

const copyScriptRelPath = "scripts/e2e-copy-check.py"

const (
	ownerPage = `export function Owner() {
  return (
    <p>
      Its owner is the only person who sees it.
    </p>
  );
}
`
	kindFilter = `const KIND_OPTIONS = [
  { value: "", label: "All" },
  { value: "automation", label: "Automations" },
];
export const heading = "Automations";
`
	deleteButton = `export function Delete() {
  return <Button confirmLabel="Delete script">Delete script</Button>;
}
`
	ownerSpec = `import { test, expect } from "@playwright/test";

test("owner", async ({ page }) => {
  await expect(page.getByText(/only person who sees it/)).toBeVisible();
  await expect(page.getByRole("option", { name: "Automations" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete script" })).toBeVisible();
  const words = (await page.textContent("main"))?.split(/\s+/);
  await expect(page.getByText("Alex Morgan")).toBeVisible();
  await page.getByRole("button", { name: /Open/ }).click();
});
`
)

func TestE2ECopyCheck(t *testing.T) {
	t.Parallel()
	const fail = "FAIL e2e-copy-check"
	cases := []struct {
		name  string
		files map[string]string
		env   []string
		expect
	}{
		{
			name: "a rewritten sentence a spec still asserts fails and names the spec line",
			files: map[string]string{"ui/src/pages/Owner.tsx": strings.Replace(ownerPage,
				"Its owner is the only person who sees it.", "Only its owner and an administrator see it.", 1)},
			expect: expect{want: []string{
				fail,
				`ui/e2e/interactive/owner.spec.ts:4: /only person who sees it/ matches "Its owner is the only person who sees it."`,
				"ui/src/pages/Owner.tsx no longer has",
			}},
		},
		{
			name:  "copy a file lost but another still shows is listed, not failed",
			files: map[string]string{"ui/src/pages/Kind.tsx": strings.Replace(kindFilter, `label: "Automations"`, `label: "Scripts"`, 1)},
			expect: expect{pass: true, want: []string{
				"still shown elsewhere",
				`ui/e2e/interactive/owner.spec.ts:5: "Automations" matches "Automations"`,
			}, mustNot: []string{fail}},
		},
		{
			name: "copy rendered by an interpolated template is not removed",
			files: map[string]string{"ui/src/pages/Delete.tsx": "export function Delete({ noun }) {\n" +
				"  return <Button confirmLabel={`Delete ${noun}`}>{`Delete ${noun}`}</Button>;\n}\n"},
			expect: expect{pass: true, want: []string{"no UI copy removed"}, mustNot: []string{fail, "Delete script"}},
		},
		{
			name: "copy moved within a file, a comment and a test file are not removals",
			files: map[string]string{
				"ui/src/pages/Owner.tsx":      "// Its owner is the only person who sees it.\n" + ownerPage,
				"ui/src/pages/Owner.test.tsx": `it("says", () => expect("Its owner is the only person who sees it.").toBeTruthy());`,
			},
			expect: expect{pass: true, want: []string{"no UI copy removed from ui/src against main"}, mustNot: []string{fail}},
		},
		{
			name:   "a spec helper's generic regex matches no removed copy",
			files:  map[string]string{"ui/src/pages/Empty.tsx": "export const empty = <p>Nothing shared yet.</p>;\n"},
			expect: expect{pass: true, want: []string{"1 UI text(s) no longer in ui/src; no e2e spec asserts one"}, mustNot: []string{fail, `/\s+/`}},
		},
		{
			name:  "a name removed from MSW mock data a spec asserts fails, since the suite runs against the mocks",
			files: map[string]string{"ui/src/mocks/data/owners.ts": "export const owners = [{ name: \"Dana Reyes\" }];\n"},
			expect: expect{want: []string{
				fail,
				`ui/e2e/interactive/owner.spec.ts:8: "Alex Morgan" matches "Alex Morgan", which ui/src/mocks/data/owners.ts no longer has`,
			}},
		},
		{
			name:  "a regex a removed sentence matched is listed, not failed, while it still matches copy on show",
			files: map[string]string{"ui/src/pages/Print.tsx": "export const print = <p>Saved.</p>;\n"},
			expect: expect{pass: true, want: []string{
				"still shown elsewhere",
				`ui/e2e/interactive/owner.spec.ts:9: /Open/ matches "Open the print dialog and save a PDF"`,
			}, mustNot: []string{fail}},
		},
		{
			name: "a regex that matches no copy left on show still fails",
			files: map[string]string{
				"ui/src/pages/Print.tsx":     "export const print = <p>Saved.</p>;\n",
				"ui/src/pages/Inspector.tsx": "export const open = <Button label=\"Go\" />;\n",
			},
			expect: expect{want: []string{fail, `ui/e2e/interactive/owner.spec.ts:9: /Open/ matches`}},
		},
		{
			name:   "a base branch that cannot be resolved fails rather than skipping",
			env:    []string{"BASE_BRANCH=no-such-branch"},
			expect: expect{want: []string{"FAIL e2e-copy-check: cannot resolve base branch 'no-such-branch'"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newGitRepo(t, copyScriptRelPath, map[string]string{
				"ui/src/pages/Owner.tsx":             ownerPage,
				"ui/src/pages/Kind.tsx":              kindFilter,
				"ui/src/pages/Delete.tsx":            deleteButton,
				"ui/src/pages/Empty.tsx":             "export const empty = <p>Nothing here yet, and nothing shared.</p>;\n",
				"ui/src/pages/Print.tsx":             "export const print = <p>Open the print dialog and save a PDF</p>;\n",
				"ui/src/pages/Inspector.tsx":         "export const open = <Button label=\"Open\" />;\n",
				"ui/e2e/interactive/owner.spec.ts":   ownerSpec,
				"ui/src/mocks/data/owners.ts":        "export const owners = [{ name: \"Alex Morgan\" }];\n",
				"ui/e2e/interactive/NOTES_x.spec.ts": ownerSpec,
			})
			for rel, body := range tc.files {
				writeFile(t, root, rel, body)
			}
			out, passed := runScript(t, root, []string{copyScriptRelPath}, tc.env)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}
