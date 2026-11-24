// Package sitepath is how a release keys its files: the paths a release may
// hold (CleanPath), and where each stored file's bytes live in object storage
// (ObjectKey) and what fixes a file set (Digest).
package sitepath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// What a release path may be.
const (
	firstPrintable = 0x20
	deleteRune     = 0x7f
	maxPathLen     = 1024
)

// CleanPath is a file's path as the release keys it, or a refusal: relative,
// forward-slashed, with no parent reference, no control character, and no
// dotted segment other than .well-known, so a source tree's .env or .git never
// becomes a public URL. A project's tooling dotfiles at the root (.nvmrc,
// .eslintrc*, .prettierrc*, sitebuild.ToolingDotfile) are taken as source and
// never served (#2785); .npmrc is refused, since it holds a registry token as
// often as not.
func CleanPath(name string) (string, error) {
	p := strings.TrimPrefix(strings.TrimSpace(name), "./")
	switch {
	case unservablePath(p):
		return "", fmt.Errorf("file path %q cannot be served", name)
	case strings.ContainsFunc(p, isControlRune):
		return "", fmt.Errorf("file path %q holds a control character", name)
	case strings.HasPrefix(p, "/") || path.Clean(p) != p:
		return "", fmt.Errorf("file path %q must be relative and clean", name)
	case sitebuild.ToolingDotfile(p):
		return p, nil
	case path.Base(p) == ".npmrc":
		return "", fmt.Errorf("file path %q is refused: an .npmrc often holds a registry token, and a site's build reads no registry settings; leave it out", name)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if dottedSegment(seg) {
			return "", fmt.Errorf("file path %q has a dotted segment; a release is public, so only .well-known is served", name)
		}
	}
	return p, nil
}

// unservablePath reports a path no release can key: empty, overlong, not
// UTF-8, or holding a backslash or a NUL.
func unservablePath(p string) bool {
	return p == "" || len(p) > maxPathLen || !utf8.ValidString(p) || strings.ContainsAny(p, "\\\x00")
}

func isControlRune(r rune) bool { return r < firstPrintable || r == deleteRune }

// dottedSegment reports a parent reference or a hidden name; .well-known is
// the one dotted segment a release may hold.
func dottedSegment(seg string) bool {
	return seg == ".." || (strings.HasPrefix(seg, ".") && seg != ".well-known")
}

// ObjectKey is where one form of a file is stored: "" for the file itself,
// "gz" or "br" for a compressed variant. Objects are kept by digest, per site,
// so a release that keeps a file costs nothing and a caller's path never
// reaches a key; a file written before that stays under its release's prefix.
func ObjectKey(siteID string, f site.File, encoding string) string {
	if f.StoredIn != "" {
		form := "files"
		if encoding != "" {
			form = encoding
		}
		return fmt.Sprintf("sites/%s/releases/%s/%s/%s", siteID, f.StoredIn, form, f.Path)
	}
	key := fmt.Sprintf("sites/%s/objects/%s", siteID, f.SHA256)
	if encoding != "" {
		key += "." + encoding
	}
	return key
}

// Digest fixes a file set: every path and content digest, in path order.
func Digest(files []site.File) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b site.File) int { return strings.Compare(a.Path, b.Path) })
	h := sha256.New()
	for _, f := range sorted {
		_, _ = io.WriteString(h, f.Path+"\x00"+f.SHA256+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}
