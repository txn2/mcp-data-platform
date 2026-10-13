// Package indexpolicy decides how a managed resource is indexed for search
// (#2111): fully, on its metadata alone, or not at all.
//
// The decision is the writer's first. A writer that says nothing gets the
// default the operator's rules give the file, matched on its folder path, its
// media type, its size, its library scope and where the write came from; the
// first rule that matches decides. A rule may also set a ceiling, so a writer
// cannot ask for more indexing than the operator allows on a folder of bulk
// data. Being written by a script is never itself the deciding fact: poster art
// a pipeline writes is searchable, and an origin is only a matcher an operator
// may choose to use.
package indexpolicy

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Policy is how one resource is indexed for search.
type Policy string

// The three policies, cheapest last. Full embeds the resource's metadata and a
// prefix of its content and stores that prefix for the lexical arm; Metadata
// embeds the name, description, folder, filename and tags and reads no content;
// None enqueues no index job, reads no blob, stores no text and no vector.
const (
	Full     Policy = "full"
	Metadata Policy = "metadata"
	None     Policy = "none"
)

// Origin is where a resource write came from, a matcher a rule may use.
type Origin string

// The writers a resource can come from.
const (
	// OriginUpload is a person uploading through the portal or the REST API.
	OriginUpload Origin = "upload"
	// OriginAgent is a session's manage_resource call.
	OriginAgent Origin = "agent"
	// OriginScript is any write made while a managed script runs.
	OriginScript Origin = "script"
	// OriginExport is an export tool's output landed in the library.
	OriginExport Origin = "export"
	// OriginWebhook is an inbound webhook source's compacted window.
	OriginWebhook Origin = "webhook"
)

// rank orders the policies by how much indexing they do, so a ceiling can be
// compared against a request.
func (p Policy) rank() int {
	switch p {
	case Full:
		return 2 //nolint:mnd // the order of three values
	case Metadata:
		return 1
	default:
		return 0
	}
}

// Valid reports whether p is one of the three policies.
func (p Policy) Valid() bool {
	return p == Full || p == Metadata || p == None
}

// Indexed reports whether a resource under p is given an index job at all.
func (p Policy) Indexed() bool { return p == Full || p == Metadata }

// ReadsContent reports whether a resource under p has its content extracted.
func (p Policy) ReadsContent() bool { return p == Full }

// ErrInvalid is returned for a policy value that is none of the three.
var ErrInvalid = errors.New(`must be one of "full", "metadata", "none"`)

// Parse reads a policy a writer or an operator named. Empty is not a policy;
// callers treat it as "not said".
func Parse(s string) (Policy, error) {
	p := Policy(strings.ToLower(strings.TrimSpace(s)))
	if !p.Valid() {
		return "", fmt.Errorf("index %w, not %q", ErrInvalid, s)
	}
	return p, nil
}

// Rule is one operator rule. Every matcher it sets must hold for the rule to
// apply; a rule that sets none applies to every resource.
type Rule struct {
	// Path is a slash glob over the resource's folder path, where "**"
	// matches any number of folders: "pipelines/**" is everything filed under
	// pipelines, at any depth.
	Path string `yaml:"path,omitempty"`
	// MIME lists media types, each exact or a family ("image/*").
	MIME []string `yaml:"mime,omitempty"`
	// SizeOver matches a file larger than this ("1MiB", "500KB", or bytes).
	SizeOver string `yaml:"size_over,omitempty"`
	// Scope matches the library: global, persona or user.
	Scope string `yaml:"scope,omitempty"`
	// Origin lists the writers the rule applies to.
	Origin []string `yaml:"origin,omitempty"`
	// Index is the policy a writer that says nothing gets. Empty leaves the
	// default to the next rule, so a rule may set only a ceiling.
	Index string `yaml:"index,omitempty"`
	// Max is the most indexing a writer may ask for here. Empty is no ceiling.
	Max string `yaml:"max,omitempty"`

	sizeOverBytes int64
}

// Config is the resources.index section.
type Config struct {
	// Default is the policy a resource no rule decides gets. Empty is full.
	Default string `yaml:"default,omitempty"`
	// Rules are matched in order; the first that sets an index decides the
	// default, and the first that sets a max caps a writer's request.
	Rules []Rule `yaml:"rules,omitempty"`
}

// BuiltinRules are the rules that follow an operator's own: a table of values
// is embedded on its metadata alone, which is what #1988 did in code. An
// operator rule that matches first decides instead.
func BuiltinRules() []Rule {
	return []Rule{{
		MIME:  []string{contenttype.CSV, contenttype.TSV, contenttype.NDJSON},
		Index: string(Metadata),
	}}
}

// Validate checks every value the section names, so a typo is refused at
// startup rather than read as "no rule".
func (c *Config) Validate() error {
	if c.Default != "" {
		if _, err := Parse(c.Default); err != nil {
			return fmt.Errorf("resources.index.default: %w", err)
		}
	}
	for i := range c.Rules {
		if err := c.Rules[i].validate(); err != nil {
			return fmt.Errorf("resources.index.rules[%d]: %w", i, err)
		}
	}
	return nil
}

func (r *Rule) validate() error {
	if err := r.validatePolicies(); err != nil {
		return err
	}
	return r.validateMatchers()
}

// validatePolicies checks the policies a rule gives: at least one, each known.
func (r *Rule) validatePolicies() error {
	if r.Index == "" && r.Max == "" {
		return errors.New("a rule sets index, max, or both")
	}
	for _, v := range []string{r.Index, r.Max} {
		if v == "" {
			continue
		}
		if _, err := Parse(v); err != nil {
			return err
		}
	}
	return nil
}

// validateMatchers checks what a rule matches on, and reads its size.
func (r *Rule) validateMatchers() error {
	if r.Path != "" {
		if _, err := path.Match(strings.ReplaceAll(r.Path, "**", "x"), ""); err != nil {
			return fmt.Errorf("path %q: %w", r.Path, err)
		}
	}
	if err := r.validateWriters(); err != nil {
		return err
	}
	if r.SizeOver != "" {
		n, err := ParseSize(r.SizeOver)
		if err != nil {
			return fmt.Errorf("size_over: %w", err)
		}
		r.sizeOverBytes = n
	}
	return nil
}

// validateWriters checks the library and origins a rule names.
func (r *Rule) validateWriters() error {
	if r.Scope != "" && r.Scope != "global" && r.Scope != "persona" && r.Scope != "user" {
		return fmt.Errorf("scope %q: must be global, persona or user", r.Scope)
	}
	for _, o := range r.Origin {
		if !validOrigin(Origin(o)) {
			return fmt.Errorf("origin %q: must be one of upload, agent, script, export, webhook", o)
		}
	}
	return nil
}

func validOrigin(o Origin) bool {
	switch o {
	case OriginUpload, OriginAgent, OriginScript, OriginExport, OriginWebhook:
		return true
	default:
		return false
	}
}

// File is what a rule is matched against.
type File struct {
	Path      string
	MIMEType  string
	SizeBytes int64
	Scope     string
	Origin    Origin
}

// Decision is a resolved policy and whether the writer chose it. A policy no
// writer chose is the one a later reclassification may change.
type Decision struct {
	Policy   Policy
	Explicit bool
}

// Resolver applies a validated Config.
type Resolver struct {
	def   Policy
	rules []Rule
}

// NewResolver builds a resolver over cfg, validating it. The built-in rules
// follow cfg's own.
func NewResolver(cfg Config) (*Resolver, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	builtin := BuiltinRules()
	for i := range builtin {
		if err := builtin[i].validate(); err != nil {
			return nil, err
		}
	}
	def := Full
	if cfg.Default != "" {
		def, _ = Parse(cfg.Default)
	}
	rules := make([]Rule, 0, len(cfg.Rules))
	rules = append(rules, cfg.Rules...)
	rules = append(rules, builtin...)
	return &Resolver{def: def, rules: rules}, nil
}

// Resolver returns the resolver over c. A section is validated when the
// configuration loads, so one it cannot build is never asked for; the
// built-in rules answer if one is.
func (c Config) Resolver() *Resolver {
	r, err := NewResolver(c)
	if err != nil {
		return Default()
	}
	return r
}

// Default returns a resolver with no operator rules: the built-in rules over a
// full default. A deployment that configures nothing gets it.
func Default() *Resolver {
	r, _ := NewResolver(Config{})
	return r
}

// Resolve decides f's policy. requested is what the writer asked for; empty
// is "not said". A request above the ceiling the first capping rule sets is
// lowered to that ceiling, and is still the writer's choice.
func (r *Resolver) Resolve(f File, requested Policy) Decision {
	if r == nil {
		r = Default()
	}
	if requested != "" {
		return Decision{Policy: r.capped(f, requested), Explicit: true}
	}
	return Decision{Policy: r.capped(f, r.defaultFor(f))}
}

func (r *Resolver) defaultFor(f File) Policy {
	for i := range r.rules {
		if r.rules[i].Index != "" && r.rules[i].matches(f) {
			return Policy(strings.ToLower(r.rules[i].Index))
		}
	}
	return r.def
}

func (r *Resolver) capped(f File, p Policy) Policy {
	for i := range r.rules {
		if r.rules[i].Max == "" || !r.rules[i].matches(f) {
			continue
		}
		ceiling := Policy(strings.ToLower(r.rules[i].Max))
		if p.rank() > ceiling.rank() {
			return ceiling
		}
		return p
	}
	return p
}

func (r *Rule) matches(f File) bool {
	return r.matchesFile(f) && r.matchesWrite(f)
}

// matchesFile is the rule's matchers over the file itself: folder, type, size.
func (r *Rule) matchesFile(f File) bool {
	if r.Path != "" && !MatchPath(r.Path, f.Path) {
		return false
	}
	if len(r.MIME) > 0 && !matchMIME(r.MIME, f.MIMEType) {
		return false
	}
	return r.SizeOver == "" || f.SizeBytes > r.sizeOverBytes
}

// matchesWrite is the rule's matchers over where the file is written and by
// what: its library and its origin.
func (r *Rule) matchesWrite(f File) bool {
	if r.Scope != "" && r.Scope != f.Scope {
		return false
	}
	return len(r.Origin) == 0 || containsOrigin(r.Origin, f.Origin)
}

func containsOrigin(list []string, o Origin) bool {
	for _, v := range list {
		if Origin(v) == o {
			return true
		}
	}
	return false
}

func matchMIME(patterns []string, mime string) bool {
	m := contenttype.Normalize(mime)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if family, ok := strings.CutSuffix(p, "/*"); ok {
			if strings.HasPrefix(m, family+"/") {
				return true
			}
			continue
		}
		if contenttype.Normalize(p) == m {
			return true
		}
	}
	return false
}

// MatchPath reports whether a folder path matches a slash glob in which "**"
// stands for any number of folders, none included. Every other segment is a
// path.Match pattern.
func MatchPath(pattern, p string) bool {
	return matchSegments(splitPath(pattern), splitPath(p))
}

func splitPath(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// sizeUnits are the suffixes a size may carry, longest first so "MiB" is
// matched before "B".
//
//nolint:gochecknoglobals // a read-only table.
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"GB", 1e9},
	{"MB", 1e6},
	{"KB", 1e3},
	{"B", 1},
}

// decimal is the base a size's number is written in, and int64Bits the
// width it is read into.
const (
	decimal   = 10
	int64Bits = 64
)

// ParseSize reads a whole number of bytes, or one with a unit (KiB, MiB, GiB,
// KB, MB, GB, B).
func ParseSize(s string) (int64, error) {
	v := strings.TrimSpace(s)
	mult := int64(1)
	for _, u := range sizeUnits {
		if n, ok := strings.CutSuffix(v, u.suffix); ok {
			v, mult = strings.TrimSpace(n), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(v, decimal, int64Bits)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a size: %q", s)
	}
	return n * mult, nil
}
