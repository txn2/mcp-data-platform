package site

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// The fields of a redirect rule: from, to, and an optional status.
const (
	minRuleFields = 2
	maxRuleFields = 3
)

// File is one file of a release.
type File struct {
	Path        string `json:"path" example:"css/site.3f2a9c1b.css"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type" example:"text/css"`
	Size        int64  `json:"size"`
	// GzipSize and BrotliSize are the stored variants' sizes, 0 for a file
	// with no such variant.
	GzipSize   int64 `json:"gzip_size,omitempty"`
	BrotliSize int64 `json:"brotli_size,omitempty"`
	// StoredIn is the release whose own prefix holds this file's objects, for
	// a file written before objects were kept by digest; "" for every other.
	StoredIn string `json:"-"`
}

// The trees of a release. Output is what is served. A rendered release also has
// its source, which is what is edited, and the record of what each output read.
const (
	TreeOutput = "output"
	TreeSource = "source"
	TreeRender = "render"
)

// Redirect is one rule of a release's redirect map. From ending in "/*"
// matches the path and everything beneath it, and ":splat" in To is what the
// "*" matched.
type Redirect struct {
	From   string `json:"from" example:"/old/*"`
	To     string `json:"to" example:"/new/:splat"`
	Status int    `json:"status" example:"301"`
}

// Release is one immutable file set.
type Release struct {
	ID         string `json:"id"`
	SiteID     string `json:"site_id"`
	Number     int    `json:"number" example:"3"`
	Source     string `json:"source,omitempty"`
	Note       string `json:"note,omitempty"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
	// ManifestSHA256 fixes the file set: the digest of every path and content
	// digest, in path order.
	ManifestSHA256 string     `json:"manifest_sha256"`
	Redirects      []Redirect `json:"redirects"`
	// Headers is what the release's _headers file declared (#2557).
	Headers []HeaderRule `json:"headers"`
	// Contract is what the release's _application file declared (#2679),
	// nil for none: the application it is a front end of and the contract
	// versions it works with.
	Contract  *Contract `json:"contract,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	// Base is the number of the release this one was derived from, 0 for a
	// release built from a complete file set.
	Base int `json:"base,omitempty" example:"41"`
	// Files is filled by a read that asks for them. Written with a
	// ManifestParent it holds only what differs from the parent.
	Files []File `json:"files,omitempty"`
	// ManifestParent is the release whose file set this one amends, "" for a
	// release that holds its whole file set. ManifestDepth counts the links
	// back to one that does.
	ManifestParent string `json:"-"`
	ManifestDepth  int    `json:"-"`
	// Removed is the paths this release takes out of its parent's file set.
	Removed []string `json:"-"`
	// Rendered is true for a release with a source tree of its own; a release
	// without one serves the files it was published with.
	Rendered bool `json:"rendered,omitempty"`
	// SourceFiles and SourceRemoved are Files and Removed for the source tree.
	SourceFiles   []File   `json:"-"`
	SourceRemoved []string `json:"-"`
	// RenderIndex is the file holding what each output read.
	RenderIndex *File `json:"-"`
	// Pruned is true once a sweep removed the release's files (#2602); its
	// record stays so the deployments and approvals naming it read as before.
	Pruned   bool       `json:"pruned,omitempty"`
	PrunedAt *time.Time `json:"pruned_at,omitempty"`
}

// Deployment is one recorded move of an environment's pointer.
type Deployment struct {
	Environment string `json:"environment"`
	Action      string `json:"action" example:"promote"`
	From        int    `json:"from_release"`
	To          int    `json:"to_release"`
	Actor       string `json:"actor"`
	// Detail is what a status change said: the offline message or the
	// redirect target (#2631, #2632); a rename's old name (#2630).
	Detail string `json:"detail,omitempty"`
	// DecidedBy is whose decision carried out a move an agent asked for
	// (#2643), absent for a move a person made.
	DecidedBy string `json:"decided_by,omitempty"`
	// Removed is a move of an environment that was removed (#2630).
	Removed   bool      `json:"removed,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MovesRelease reports whether the deployment moved the environment's
// pointer, rather than recording a status change or a rename.
func (d Deployment) MovesRelease() bool {
	switch d.Action {
	case ActionOffline, ActionOnline, ActionRedirect, ActionRename:
		return false
	}
	return true
}

// ParseRedirects reads a release's _redirects file: one rule per line, "from
// to [status]", with blank lines and # comments skipped. Status defaults to
// 301 and must be a redirect status. A target is a local path or an http(s)
// URL, and a rule with a control character in it is refused, so nothing in
// the file can write a header of its own.
func ParseRedirects(text string) ([]Redirect, error) {
	out := []Redirect{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule, err := parseRedirect(line)
		if err != nil {
			return nil, fmt.Errorf("the _redirects file, line %d: %w", i+1, err)
		}
		out = append(out, rule)
		if len(out) > maxRedirects {
			return nil, fmt.Errorf("the _redirects file holds more than %d rules", maxRedirects)
		}
	}
	return out, nil
}

// RenderRedirects is a redirect map as the _redirects file that parses back to
// it, which is what an edit of that file reads: the rules, without the comments
// the file they were first parsed from may have held.
func RenderRedirects(rules []Redirect) string {
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "%s %s %d\n", r.From, r.To, r.Status)
	}
	return b.String()
}

func parseRedirect(line string) (Redirect, error) {
	fields := strings.Fields(line)
	if len(fields) < minRuleFields || len(fields) > maxRuleFields {
		return Redirect{}, errors.New("a rule is: from to [status]")
	}
	rule := Redirect{From: fields[0], To: fields[1], Status: http.StatusMovedPermanently}
	if len(fields) == maxRuleFields {
		status, err := strconv.Atoi(fields[2])
		if err != nil {
			return Redirect{}, fmt.Errorf("status %q is not a number", fields[2])
		}
		rule.Status = status
	}
	return rule, ValidateRedirect(rule)
}

// ValidateRedirect checks one rule. Status 200 is a rewrite: the path is
// answered with the file the target names, in place, which is how a
// single-page application answers its own routes (/* /index.html 200).
func ValidateRedirect(r Redirect) error {
	switch r.Status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	case http.StatusOK:
		if !localTarget(r.To) {
			return fmt.Errorf("to %q: a rewrite (status 200) answers with a file of this site, so its target is a local path", r.To)
		}
	default:
		return fmt.Errorf("status %d is not a redirect or a rewrite (200)", r.Status)
	}
	if !strings.HasPrefix(r.From, "/") || len(r.From) > maxRedirectLen || hasControl(r.From) {
		return fmt.Errorf("from %q: a path starting with /", r.From)
	}
	if strings.Contains(strings.TrimSuffix(r.From, "/*"), "*") {
		return fmt.Errorf("from %q: * only as the last segment", r.From)
	}
	return validateTarget(r.To)
}

func validateTarget(to string) error {
	if len(to) > maxRedirectLen || hasControl(to) {
		return fmt.Errorf("to %q: too long or holding a control character", to)
	}
	if localTarget(to) {
		return nil
	}
	u, err := url.Parse(to)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("to %q: a local path or an http(s) URL", to)
	}
	return nil
}

// localTarget reports whether to is a path on this site: one leading slash,
// never two (a protocol-relative address) and no backslash, which a browser
// reads as a slash.
func localTarget(to string) bool {
	return strings.HasPrefix(to, "/") && !strings.HasPrefix(to, "//") && !strings.Contains(to, `\`)
}

// hasControl reports a control character or a space: a rule is written as
// space-separated fields, so a space cannot be part of one.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r <= 0x20 || r == 0x7f })
}

// RollbackTarget is the release a rollback returns an environment to: the one
// named, which the environment must have served and which must not be pruned
// (ErrPruned), or, with to 0, the newest kept release it served before the
// current one. A release it never served is reached by promote.
func RollbackTarget(env *Environment, history []Deployment, to int, gone map[int]bool) (int, error) {
	served := make([]int, 0, len(history))
	for _, d := range history {
		if d.MovesRelease() && !d.Removed && d.To != env.Release {
			served = append(served, d.To)
		}
	}
	if to != 0 {
		switch {
		case !slices.Contains(served, to):
			return 0, fmt.Errorf("environment %q has not served release %d; a release it never served is reached by promote", env.Name, to)
		case gone[to]:
			return 0, ErrPruned
		}
		return to, nil
	}
	return previousRelease(env, served, gone)
}

// previousRelease is the newest kept release the environment served that is
// older than the current one, so a second rollback keeps going back rather
// than returning.
func previousRelease(env *Environment, served []int, gone map[int]bool) (int, error) {
	before := 0
	for _, n := range served {
		if n < env.Release && n > before && !gone[n] {
			before = n
		}
	}
	if before == 0 {
		return 0, fmt.Errorf("environment %q has served no earlier release it still keeps", env.Name)
	}
	return before, nil
}

// Contract is what a site release's _application file declares (#2679): the
// application it is a front end of, and the range of that application's
// contract versions it works with, written as space-separated clauses
// (">=2 <4", "=3", "2"). A release with no _application file, or one naming no
// range, works with every contract.
type Contract struct {
	Application string `json:"application"`
	Range       string `json:"contract"`
}

// ApplicationFile is the release file a contract is declared in. It is read
// at publish and kept on the release, not served.
const ApplicationFile = "_application"

// maxContractBytes bounds the _application file.
const maxContractBytes = 4 << 10

// clause is one bound of a range.
type clause struct {
	op string
	n  int
}

// ParseContract reads an _application file: a JSON object with application
// and contract (a range). An empty file declares nothing.
func ParseContract(text string) (*Contract, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil //nolint:nilnil // an empty file declares nothing
	}
	if len(text) > maxContractBytes {
		return nil, fmt.Errorf("the _application file is %d bytes, more than %d", len(text), maxContractBytes)
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var c Contract
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf(`the _application file must be a JSON object such as {"application": "store", "contract": ">=2 <4"}: %w`, err)
	}
	c.Application, c.Range = strings.TrimSpace(c.Application), strings.Join(strings.Fields(c.Range), " ")
	if c.Application == "" {
		return nil, errors.New("the _application file names no application")
	}
	if _, err := parseRange(c.Range); err != nil {
		return nil, fmt.Errorf("the _application file: %w", err)
	}
	return &c, nil
}

// Render writes the contract back as the _application file it was read from.
func (c *Contract) Render() string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(c)
	return b.String()
}

// Accepts reports whether the release works with the application's contract
// version v. An application that declares none (0), and a release that names
// no range, are compatible with everything.
func (c *Contract) Accepts(v int) bool {
	if c == nil || c.Range == "" || v == 0 {
		return true
	}
	clauses, err := parseRange(c.Range)
	if err != nil {
		return false
	}
	for _, cl := range clauses {
		if !cl.holds(v) {
			return false
		}
	}
	return true
}

// For reports whether the contract is about the named application: a release
// declaring another application's contract says nothing about this one.
func (c *Contract) For(application string) bool {
	return c != nil && strings.EqualFold(c.Application, application)
}

func (cl clause) holds(v int) bool {
	switch cl.op {
	case ">=":
		return v >= cl.n
	case ">":
		return v > cl.n
	case "<=":
		return v <= cl.n
	case "<":
		return v < cl.n
	default:
		return v == cl.n
	}
}

// errRange is a range clause that is not an operator and a whole number.
var errRange = errors.New(`a contract range is clauses of an operator (>=, >, <=, <, =) and a whole number, such as ">=2 <4"`)

// parseRange reads a range's clauses; "" is no bound.
func parseRange(r string) ([]clause, error) {
	out := []clause{}
	for f := range strings.FieldsSeq(r) {
		op := ""
		for _, p := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(f, p) {
				op = p
				break
			}
		}
		n, err := strconv.Atoi(strings.TrimPrefix(f, op))
		if err != nil || n < 0 {
			return nil, errRange
		}
		out = append(out, clause{op: op, n: n})
	}
	return out, nil
}

// Data files are the release files a publish reads into the release rather
// than serving: the redirect map (_redirects), the headers each path carries
// (_headers, #2557) and the application contract (_application, #2679). Each
// reads back as the text it became, so it is edited like any file.
const (
	RedirectsFile = "_redirects"
	HeadersFile   = "_headers"
)

// dataFileType is what a data file reads back as.
const dataFileType = "text/plain; charset=utf-8"

// IsDataFile reports whether a release path is one of its data files.
func IsDataFile(name string) bool {
	return name == RedirectsFile || name == HeadersFile || name == ApplicationFile
}

// DataFile is the text a release's data file reads back as and its type;
// present is false when the release declares nothing in it.
func (r *Release) DataFile(name string) (text, contentType string, present bool) {
	switch name {
	case RedirectsFile:
		return RenderRedirects(r.Redirects), dataFileType, len(r.Redirects) > 0
	case HeadersFile:
		return RenderHeaders(r.Headers), dataFileType, len(r.Headers) > 0
	case ApplicationFile:
		return r.Contract.Render(), "application/json", r.Contract != nil
	}
	return "", "", false
}

// SetDataFile reads a data file's text into the release; "" declares
// nothing, which is what deleting the file does.
func (r *Release) SetDataFile(name, text string) error {
	var err error
	switch name {
	case RedirectsFile:
		r.Redirects = []Redirect{}
		if text != "" {
			r.Redirects, err = ParseRedirects(text)
		}
	case HeadersFile:
		r.Headers = []HeaderRule{}
		if text != "" {
			r.Headers, err = ParseHeaders(text)
		}
	case ApplicationFile:
		r.Contract, err = ParseContract(text)
	}
	return err
}

// SameData reports whether two releases declare the same data files.
func (r *Release) SameData(o *Release) bool {
	for _, name := range []string{RedirectsFile, HeadersFile, ApplicationFile} {
		a, _, _ := r.DataFile(name)
		b, _, _ := o.DataFile(name)
		if a != b {
			return false
		}
	}
	return true
}

// activeTypes are the types a site serves that the shared filename table will
// not name.
var activeTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
	".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".json": "application/json",
	".wasm": "application/wasm", ".xml": "application/xml", ".txt": dataFileType,
	// TypeScript and JSX are text a person or an agent reads and patches; a
	// release that compiles them never serves them (#2774).
	".ts": dataFileType, ".tsx": dataFileType, ".jsx": dataFileType, ".mts": dataFileType, ".cts": dataFileType,
}

// ServedType is the type a release file is served as, from its name alone:
// never from its bytes or from what a source claimed. A site's own pages and
// scripts are what it is for, so they are named here; the shared table leaves
// them out because elsewhere a filename must not make a file active.
func ServedType(name string) string {
	if ct, ok := activeTypes[strings.ToLower(path.Ext(name))]; ok {
		return ct
	}
	if ct := contenttype.TypeForFilename(name); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
