package pkgmap

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// urlPackage is the package and version a CDN URL's path names first.
var urlPackage = regexp.MustCompile(`^/((?:@[^/@]+/)?[^/@]+)@([^/?#]+)`)

// pinnedURL is one URL of an imports map read for what it loads.
type pinnedURL struct {
	key, raw string
	pkg, ver string
	host     string
	// deps are the versions its ?deps= pins, by package; nil for none.
	deps map[string]string
}

func readURL(key, raw string) (pinnedURL, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return pinnedURL{}, false
	}
	m := urlPackage.FindStringSubmatch(u.Path)
	if m == nil {
		return pinnedURL{}, false
	}
	p := pinnedURL{key: key, raw: raw, pkg: m[1], ver: strings.TrimPrefix(m[2], "v"), host: u.Host}
	if deps := u.Query().Get("deps"); deps != "" {
		p.deps = map[string]string{}
		for d := range strings.SplitSeq(deps, ",") {
			at := strings.LastIndex(d, "@")
			if at > 0 {
				p.deps[d[:at]] = strings.TrimPrefix(d[at+1:], "v")
			}
		}
	}
	return p, true
}

// CheckSingletons refuses an imports map that would load two copies of a
// package only one copy of which may load (#2785): two URLs naming it at
// different versions, a ?deps= pin of another version, or an esm.sh URL for a
// package that peers on it without pinning it, which esm.sh then builds
// against a copy of its own. Peers are read from the lockfile, else from
// extra; a package whose peers neither knows is not judged on that last rule.
func CheckSingletons(ctx context.Context, imports map[string]string, singletons []string, lock *Lock, extra Peers) ([]string, error) {
	set := append(slices.Clone(DefaultSingletons), singletons...)
	var urls []pinnedURL
	for _, key := range sortedKeys(imports) {
		if p, ok := readURL(key, imports[key]); ok {
			urls = append(urls, p)
		}
	}
	first := map[string]pinnedURL{}
	var out []string
	for _, p := range urls {
		if !slices.Contains(set, p.pkg) {
			continue
		}
		if f, seen := first[p.pkg]; seen && f.ver != p.ver {
			out = append(out, fmt.Sprintf("imports load two copies of %s: %q is %s@%s and %q is %s@%s; map them to one version", p.pkg, f.key, p.pkg, f.ver, p.key, p.pkg, p.ver))
			continue
		}
		if _, seen := first[p.pkg]; !seen {
			first[p.pkg] = p
		}
	}
	for _, p := range urls {
		msgs, err := checkPins(ctx, p, pinScope{set: set, first: first, lock: lock, extra: extra})
		if err != nil {
			return nil, err
		}
		out = append(out, msgs...)
	}
	return out, nil
}

// pinScope is what one URL's pins are checked against: the singletons, the
// first URL of each, and where peers are read from.
type pinScope struct {
	set   []string
	first map[string]pinnedURL
	lock  *Lock
	extra Peers
}

func checkPins(ctx context.Context, p pinnedURL, sc pinScope) ([]string, error) {
	set, first := sc.set, sc.first
	var out []string
	for dep, ver := range p.deps {
		if f, ok := first[dep]; ok && slices.Contains(set, dep) && f.ver != ver {
			out = append(out, fmt.Sprintf("imports load two copies of %s: %q pins %s@%s through deps, and %q is %s@%s", dep, p.key, dep, ver, f.key, dep, f.ver))
		}
	}
	if p.host != "esm.sh" {
		return out, nil
	}
	peers, err := peersFor(ctx, p, sc.lock, sc.extra)
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		if unpinned(p, peer, set, first) {
			want := peer
			if f, ok := first[peer]; ok {
				want = peer + at + f.ver
			}
			out = append(out, fmt.Sprintf("%q (%s) loads its own copy of %s: %s peers on %s, so add ?deps=%s to its URL, or generate the map with manage_site imports",
				p.key, p.raw, peer, p.pkg, peer, want))
		}
	}
	return out, nil
}

// unpinned reports a singleton peer an esm.sh URL does not pin through deps.
// react-dom@18.3.1 beside react@18.3.1 is one singleton built against another
// at its own version, the map #2774 documented, which esm.sh resolves to that
// version, and is not held to it.
func unpinned(p pinnedURL, peer string, set []string, first map[string]pinnedURL) bool {
	if !slices.Contains(set, peer) || peer == p.pkg {
		return false
	}
	if _, pinned := p.deps[peer]; pinned {
		return false
	}
	f, ok := first[peer]
	return !ok || !slices.Contains(set, p.pkg) || f.ver != p.ver
}

// peersFor is what the package a URL loads peers on, at the URL's version.
func peersFor(ctx context.Context, p pinnedURL, lock *Lock, extra Peers) ([]string, error) {
	if lock != nil {
		if locked, ok := lock.Packages[p.pkg]; ok && locked.Version == p.ver && locked.Peers != nil {
			return locked.Peers, nil
		}
	}
	if extra == nil {
		return nil, nil
	}
	peers, err := extra.PeersOf(ctx, p.pkg, p.ver)
	if err != nil {
		return nil, fmt.Errorf("reading what %s peers on: %w", p.pkg, err)
	}
	return peers, nil
}
