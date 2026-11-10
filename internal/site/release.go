package site

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
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
}

// Deployment is one recorded move of an environment's pointer.
type Deployment struct {
	Environment string    `json:"environment"`
	Action      string    `json:"action" example:"promote"`
	From        int       `json:"from_release"`
	To          int       `json:"to_release"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"created_at"`
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

// ValidateRedirect checks one rule.
func ValidateRedirect(r Redirect) error {
	switch r.Status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return fmt.Errorf("status %d is not a redirect", r.Status)
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
	if strings.HasPrefix(to, "/") && !strings.HasPrefix(to, "//") && !strings.Contains(to, `\`) {
		return nil
	}
	u, err := url.Parse(to)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("to %q: a local path or an http(s) URL", to)
	}
	return nil
}

// hasControl reports a control character or a space: a rule is written as
// space-separated fields, so a space cannot be part of one.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r <= 0x20 || r == 0x7f })
}
