package site

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Mount serves a managed-resource folder at a URL prefix on every environment
// of a site (#2591). Media sits outside releases: publish, promote and rollback
// leave it alone. A release file at the same path wins over a mount.
type Mount struct {
	// Prefix is where the folder answers, "/media/": it starts and ends with a
	// slash and is never the site's root.
	Prefix string `json:"prefix" example:"/media/"`
	// Scope and ScopeID name the resource library: global, persona with the
	// persona's name, or user with the person's subject.
	Scope   string `json:"scope" example:"global"`
	ScopeID string `json:"scope_id,omitempty"`
	// Folder is the folder in that library, "sites/marketing/media".
	Folder    string    `json:"folder" example:"sites/marketing/media"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MaxMounts bounds a site's mounts, and maxPathLen a prefix or folder.
const (
	MaxMounts  = 20
	maxPathLen = 512
)

// reservedPrefixes are paths a site's own address answers itself.
var reservedPrefixes = []string{"/_", "/.well-known/"}

// The libraries a mount can name, and the separator its paths use.
const (
	scopeGlobal  = "global"
	scopePersona = "persona"
	scopeUser    = "user"
	slash        = "/"
)

// ValidateMount refuses a prefix or a folder a mount cannot have, and returns
// the mount as it is stored: the prefix and folder in their one form.
func ValidateMount(m Mount) (Mount, error) {
	prefix, err := mountPrefix(m.Prefix)
	if err != nil {
		return m, err
	}
	folder, err := mountFolder(m)
	if err != nil {
		return m, err
	}
	m.Prefix, m.Folder, m.ScopeID = prefix, folder, strings.TrimSpace(m.ScopeID)
	return m, nil
}

// mountPrefix is a prefix in its one form, "/media/", or why it cannot be one.
func mountPrefix(raw string) (string, error) {
	p := asPrefix(raw)
	clean := path.Clean(p)
	switch {
	case clean == slash:
		return "", errors.New("media: a prefix is a path under the site, such as /media/, never its root")
	case clean+slash != p || unservablePrefix(p):
		return "", fmt.Errorf("media: prefix %q must be a clean path such as /media/", raw)
	}
	if err := prefixSegments(p, raw); err != nil {
		return "", err
	}
	return p, nil
}

// asPrefix is raw with the one leading and trailing slash a prefix has.
func asPrefix(raw string) string {
	p := strings.TrimSpace(raw)
	if !strings.HasPrefix(p, slash) {
		p = slash + p
	}
	if !strings.HasSuffix(p, slash) {
		p += slash
	}
	return p
}

// unservablePrefix reports a prefix holding a character no URL path served
// here carries, or longer than a path may be.
func unservablePrefix(p string) bool {
	return strings.ContainsAny(p, "\\\x00?#%") || hasControl(p) || len(p) > maxPathLen
}

// prefixSegments refuses a prefix the site answers itself or one with a
// dotted segment.
func prefixSegments(p, raw string) error {
	for _, r := range reservedPrefixes {
		if strings.HasPrefix(p, r) {
			return fmt.Errorf("media: prefix %q is a path the site answers itself", raw)
		}
	}
	for seg := range strings.SplitSeq(strings.Trim(p, slash), slash) {
		if strings.HasPrefix(seg, ".") {
			return fmt.Errorf("media: prefix %q has a dotted segment", raw)
		}
	}
	return nil
}

// mountFolder is the folder in its one form, or why the folder or its library
// cannot be mounted.
func mountFolder(m Mount) (string, error) {
	folder := strings.Trim(strings.TrimSpace(m.Folder), slash)
	if !folderPath(folder) {
		return "", fmt.Errorf("media: folder %q must be a folder of the library, such as sites/marketing/media", m.Folder)
	}
	if err := mountLibrary(m); err != nil {
		return "", err
	}
	return folder, nil
}

// folderPath reports a clean folder path inside a library.
func folderPath(folder string) bool {
	return folder != "" && path.Clean(folder) == folder && !strings.HasPrefix(folder, "..") && !hasControl(folder) && len(folder) <= maxPathLen
}

// mountLibrary refuses a library a mount cannot name.
func mountLibrary(m Mount) error {
	switch {
	case m.Scope != scopeGlobal && m.Scope != scopePersona && m.Scope != scopeUser:
		return fmt.Errorf("media: scope %q is global, persona or user", m.Scope)
	case m.Scope == scopeGlobal && m.ScopeID != "":
		return errors.New("media: the global library has no scope_id")
	case m.Scope != scopeGlobal && strings.TrimSpace(m.ScopeID) == "":
		return fmt.Errorf("media: a %s library needs its scope_id", m.Scope)
	}
	return nil
}

// ValidateMounts checks every mount and that no two share a prefix.
func ValidateMounts(in []Mount) ([]Mount, error) {
	if len(in) > MaxMounts {
		return nil, fmt.Errorf("media: at most %d folders", MaxMounts)
	}
	out := make([]Mount, 0, len(in))
	seen := map[string]bool{}
	for _, m := range in {
		v, err := ValidateMount(m)
		if err != nil {
			return nil, err
		}
		if seen[v.Prefix] {
			return nil, fmt.Errorf("media: two folders at %s", v.Prefix)
		}
		seen[v.Prefix] = true
		out = append(out, v)
	}
	return out, nil
}

// MountFor is the mount serving p, the longest prefix, and the path under it;
// false when no mount serves p or p names the prefix itself.
func MountFor(mounts []Mount, p string) (Mount, string, bool) {
	var best Mount
	found := false
	for _, m := range mounts {
		if strings.HasPrefix(p, m.Prefix) && len(m.Prefix) > len(best.Prefix) {
			best, found = m, true
		}
	}
	if !found {
		return best, "", false
	}
	rel := strings.TrimPrefix(p, best.Prefix)
	if rel == "" || strings.HasSuffix(rel, slash) {
		return best, "", false
	}
	return best, rel, true
}
