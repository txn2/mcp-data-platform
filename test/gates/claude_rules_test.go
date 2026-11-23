package gates

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CLAUDE.md states the project's development rules. Some are enforced by a
// gate and some are steps only a person can take; docs/reference/
// development-rules.md says which, and these tests hold the two files
// together (#2550).

const (
	claudeFile = "CLAUDE.md"
	rulesDoc   = "docs/reference/development-rules.md"
)

var (
	numberedRuleRE = regexp.MustCompile(`^\d+\. \*\*(.+?)\*\*`)
	criticalItemRE = regexp.MustCompile(`^\s+- \*\*(CRITICAL - .+?)\*\*`)
	gateTokenRE    = regexp.MustCompile("`([^`]+)`")
	testFuncRE     = regexp.MustCompile(`(?m)^func (Test\w+)\(`)
)

// ruleTitle trims the punctuation a bold rule title ends with.
func ruleTitle(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ".:")
}

// isRuleSection reports whether a CLAUDE.md heading's numbered items are
// rules: a CRITICAL section, Code Standards and AI Verification Requirements.
// Other numbered lists (the middleware order) describe the system.
func isRuleSection(heading string) bool {
	return strings.Contains(heading, "CRITICAL") || heading == "Code Standards" || heading == "AI Verification Requirements"
}

// claudeRules returns the title of every numbered rule and CRITICAL section
// in CLAUDE.md, in order.
func claudeRules(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", claudeFile))
	require(t, err)
	defer func() { _ = f.Close() }()
	var titles []string
	heading, fenced := "", false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if h, ok := strings.CutPrefix(line, "## "); ok {
			heading = strings.TrimSpace(h)
			if strings.Contains(heading, "CRITICAL") {
				titles = append(titles, heading)
			}
			continue
		}
		if m := criticalItemRE.FindStringSubmatch(line); m != nil {
			titles = append(titles, ruleTitle(m[1]))
			continue
		}
		if m := numberedRuleRE.FindStringSubmatch(line); m != nil && isRuleSection(heading) {
			titles = append(titles, ruleTitle(m[1]))
		}
	}
	require(t, sc.Err())
	return titles
}

// ruleRow is one row of the development rules table.
type ruleRow struct {
	title, status string
	gates         []string
}

// ruleRows reads the table's rows, keyed by title.
func ruleRows(t *testing.T) map[string]ruleRow {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", rulesDoc))
	require(t, err)
	rows := map[string]ruleRow{}
	for line := range strings.SplitSeq(string(body), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) != 5 || cells[0] == "Rule" {
			continue
		}
		row := ruleRow{title: cells[0], status: cells[2]}
		for _, m := range gateTokenRE.FindAllStringSubmatch(cells[3], -1) {
			row.gates = append(row.gates, m[1])
		}
		if _, dup := rows[row.title]; dup {
			t.Errorf("%s lists %q twice", rulesDoc, row.title)
		}
		rows[row.title] = row
	}
	return rows
}

// testFuncs is every Test function in the repository's Go test files.
func testFuncs(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join("..", "..")
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "build", "_dist", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) // #nosec G304 G122 -- a test file in this repository
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		for _, m := range testFuncRE.FindAllSubmatch(body, -1) {
			found[string(m[1])] = true
		}
		return nil
	})
	require(t, err)
	return found
}

// gateExists reports whether a gate the table names is real: a Makefile
// target (`make <target>`), a Go test (`Test...`) or a file in the
// repository (a path).
func gateExists(gate, makefile string, tests map[string]bool) bool {
	switch {
	case strings.HasPrefix(gate, "make "):
		target := strings.TrimPrefix(gate, "make ")
		return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).MatchString(makefile)
	case strings.HasPrefix(gate, "Test"):
		return tests[gate]
	case strings.ContainsAny(gate, "/."):
		_, err := os.Stat(filepath.Join("..", "..", gate))
		return err == nil
	}
	return false
}

// TestEveryRuleIsClassified: every numbered rule and CRITICAL section in
// CLAUDE.md has a row, every row is a rule CLAUDE.md still states, each is
// either enforced (naming a gate) or a human step, and every gate a row names
// exists.
func TestEveryRuleIsClassified(t *testing.T) {
	rules := claudeRules(t)
	if len(rules) < 20 {
		t.Fatalf("read %d rules from %s; the parser no longer finds them", len(rules), claudeFile)
	}
	rows := ruleRows(t)
	stated := map[string]bool{}
	for _, title := range rules {
		stated[title] = true
		if _, ok := rows[title]; !ok {
			t.Errorf("%s states the rule %q, which %s does not list", claudeFile, title, rulesDoc)
		}
	}
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require(t, err)
	tests := testFuncs(t)
	for title, row := range rows {
		if !stated[title] {
			t.Errorf("%s lists %q, which %s no longer states", rulesDoc, title, claudeFile)
		}
		switch row.status {
		case "enforced":
			if len(row.gates) == 0 {
				t.Errorf("%q is classified enforced and names no gate", title)
			}
		case "human":
		default:
			t.Errorf("%q is classified %q; a rule is enforced by a gate or is a human step", title, row.status)
		}
		for _, gate := range row.gates {
			if !gateExists(gate, string(makefile), tests) {
				t.Errorf("%q names the gate %q, which does not exist", title, gate)
			}
		}
	}
}

// trackedFiles lists the repository's tracked files matching a git pathspec.
func trackedFiles(t *testing.T, pathspecs ...string) []string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"ls-files", "--"}, pathspecs...)...) // #nosec G204 -- fixed pathspecs
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	require(t, err)
	return strings.Fields(string(out))
}

var (
	usesRE       = regexp.MustCompile(`^\s*-?\s*uses:\s*(\S+)`)
	actionPinRE  = regexp.MustCompile(`@[0-9a-f]{40}$`)
	fromRE       = regexp.MustCompile(`(?i)^FROM\s+(.+)$`)
	stageRE      = regexp.MustCompile(`(?i)\sAS\s+(\S+)\s*$`)
	imageRE      = regexp.MustCompile(`^\s*image:\s*(\S+)`)
	composeFileR = regexp.MustCompile(`(^|/)(docker-)?compose[^/]*\.ya?ml$`)
	dockerfileRE = regexp.MustCompile(`(^|/)Dockerfile[^/]*$`)
)

// fromImage is the image a FROM line names, past any --flag, and the stage
// name it gives.
func fromImage(rest string) (image, stage string) {
	if m := stageRE.FindStringSubmatch(rest); m != nil {
		stage = strings.ToLower(m[1])
		rest = rest[:len(rest)-len(m[0])]
	}
	for f := range strings.FieldsSeq(rest) {
		if !strings.HasPrefix(f, "--") {
			return f, stage
		}
	}
	return "", stage
}

// TestDependenciesArePinnedByDigest is CLAUDE.md's Pinned Dependencies rule:
// every GitHub Action a workflow uses is pinned to a commit, and every image a
// Dockerfile builds from or a compose file runs is pinned to a digest.
func TestDependenciesArePinnedByDigest(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) []string {
		body, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- a tracked file in this repository
		require(t, err)
		return strings.Split(string(body), "\n")
	}
	for _, rel := range trackedFiles(t, ".github") {
		if !strings.HasSuffix(rel, ".yml") && !strings.HasSuffix(rel, ".yaml") {
			continue
		}
		for i, line := range read(rel) {
			m := usesRE.FindStringSubmatch(line)
			if len(m) < 2 || strings.HasPrefix(m[1], "./") {
				continue
			}
			pinned := actionPinRE.MatchString(m[1]) || (strings.HasPrefix(m[1], "docker://") && strings.Contains(m[1], "@sha256:"))
			if !pinned {
				t.Errorf("%s:%d uses %s, not pinned to a commit", rel, i+1, m[1])
			}
		}
	}
	var dockerfiles, composes int
	for _, rel := range trackedFiles(t) {
		switch {
		case dockerfileRE.MatchString(rel):
			dockerfiles++
			stages := map[string]bool{}
			for i, line := range read(rel) {
				m := fromRE.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				image, stage := fromImage(m[1])
				if image != "scratch" && !stages[strings.ToLower(image)] && !strings.Contains(image, "@sha256:") {
					t.Errorf("%s:%d builds FROM %s, not pinned to a digest", rel, i+1, image)
				}
				if stage != "" {
					stages[stage] = true
				}
			}
		case composeFileR.MatchString(rel):
			composes++
			for i, line := range read(rel) {
				if m := imageRE.FindStringSubmatch(line); len(m) > 1 && !strings.Contains(m[1], "@sha256:") {
					t.Errorf("%s:%d runs image %s, not pinned to a digest", rel, i+1, m[1])
				}
			}
		}
	}
	if dockerfiles == 0 || composes == 0 {
		t.Fatalf("found %d Dockerfiles and %d compose files; the file patterns no longer match the repository", dockerfiles, composes)
	}
}

// boxDrawingRE matches a box drawn in characters: a box-drawing corner, or a
// +---+ border. A directory tree (├── └──) is not a diagram and passes.
var boxDrawingRE = regexp.MustCompile(`[┌┐┘╔╗╝╭╮╯]|\+-{3,}\+`)

// TestDocsDrawDiagramsInMermaid is CLAUDE.md's Diagrams rule: no tracked
// Markdown file draws a box in characters.
func TestDocsDrawDiagramsInMermaid(t *testing.T) {
	files := trackedFiles(t, "*.md")
	if len(files) == 0 {
		t.Fatal("git lists no Markdown files")
	}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join("..", "..", rel)) // #nosec G304 -- a tracked file in this repository
		require(t, err)
		for i, line := range strings.Split(string(body), "\n") {
			if boxDrawingRE.MatchString(line) {
				t.Errorf("%s:%d draws a diagram in characters; draw it in Mermaid: %.80s", rel, i+1, line)
			}
		}
	}
}
