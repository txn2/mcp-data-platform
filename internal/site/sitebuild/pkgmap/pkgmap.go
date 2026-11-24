// Package pkgmap turns a JavaScript project's package.json and lockfile into
// the imports map a site's _build.json declares (#2785): each package the
// entries import at its locked version on esm.sh, each imported path inside a
// package as its own entry, and every package's locked peers pinned through
// ?deps= so that one copy of React loads. It also refuses a map that would load
// two copies of a package only one copy of which may load.
package pkgmap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// CDN is where a generated map loads packages from.
const CDN = "https://esm.sh/"

// at and slash join a package to its version and its subpath.
const (
	at    = "@"
	slash = "/"
)

// DefaultSingletons are the packages only one copy of which may load.
var DefaultSingletons = []string{"react", "react-dom"}

// Package is one locked package.
type Package struct {
	Version string
	// Peers are its peer dependencies; nil when the lockfile does not say.
	Peers []string
}

// Lock is what a project's lockfile says about its packages, by name.
type Lock struct {
	// File is the lockfile it was read from, "" for none.
	File     string
	Packages map[string]Package
}

// lockfiles are read in this order; the first present is the project's.
var lockfiles = []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock"}

// maxLockBytes bounds a lockfile read.
const maxLockBytes = 32 << 20

// ReadLock reads the project's lockfile from the root of a tree.
func ReadLock(src fs.FS) (*Lock, error) {
	for _, name := range lockfiles {
		info, err := fs.Stat(src, name)
		if err != nil {
			continue
		}
		if info.Size() > maxLockBytes {
			return nil, fmt.Errorf("the lockfile %s is %d bytes, more than the %d read", name, info.Size(), maxLockBytes)
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		lock := &Lock{File: name, Packages: map[string]Package{}}
		switch name {
		case "package-lock.json":
			err = lock.readNPM(data)
		case "pnpm-lock.yaml":
			err = lock.readPNPM(data)
		default:
			err = lock.readYarn(data)
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		return lock, nil
	}
	return &Lock{Packages: map[string]Package{}}, nil
}

func (l *Lock) readNPM(data []byte) error {
	var doc struct {
		Packages map[string]struct {
			Version string            `json:"version"`
			Peers   map[string]string `json:"peerDependencies"`
		} `json:"packages"`
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}
	for key, p := range doc.Packages {
		name, ok := strings.CutPrefix(key, "node_modules/")
		if !ok || strings.Contains(name, "/node_modules/") || p.Version == "" {
			continue
		}
		l.Packages[name] = Package{Version: p.Version, Peers: sortedKeys(p.Peers)}
	}
	for name, d := range doc.Dependencies { // lockfileVersion 1
		if _, ok := l.Packages[name]; !ok && d.Version != "" {
			l.Packages[name] = Package{Version: d.Version}
		}
	}
	return nil
}

// pnpmKey is a pnpm package key: /name@version, name@version, or /name/version,
// with a peer suffix in parentheses or after an underscore.
var pnpmKey = regexp.MustCompile(`^/?((?:@[^/@]+/)?[^/@(]+)[@/](\d[^(_]*)`)

func (l *Lock) readPNPM(data []byte) error {
	var doc struct {
		Importers map[string]struct {
			Dependencies    map[string]yaml.Node `yaml:"dependencies"`
			DevDependencies map[string]yaml.Node `yaml:"devDependencies"`
		} `yaml:"importers"`
		Dependencies map[string]yaml.Node `yaml:"dependencies"`
		Packages     map[string]struct {
			Peers map[string]string `yaml:"peerDependencies"`
		} `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}
	direct := map[string]yaml.Node{}
	if root, ok := doc.Importers["."]; ok {
		maps.Copy(direct, root.DevDependencies)
		maps.Copy(direct, root.Dependencies)
	}
	maps.Copy(direct, doc.Dependencies)
	peers := map[string][]string{}
	for key, p := range doc.Packages {
		if m := pnpmKey.FindStringSubmatch(key); m != nil {
			peers[m[1]+at+strings.TrimSpace(m[2])] = sortedKeys(p.Peers)
		}
	}
	for name, node := range direct {
		if v := pnpmVersion(node); v != "" {
			l.Packages[name] = Package{Version: v, Peers: peers[name+at+v]}
		}
	}
	return nil
}

// pnpmVersion is the version an importer's dependency locks, without its peer
// suffix: "18.3.1", or {specifier, version: "18.3.1(react@18.3.1)"}.
func pnpmVersion(node yaml.Node) string {
	v := node.Value
	if node.Kind == yaml.MappingNode {
		var spec struct {
			Version string `yaml:"version"`
		}
		_ = node.Decode(&spec)
		v = spec.Version
	}
	v, _, _ = strings.Cut(v, "(")
	v, _, _ = strings.Cut(v, "_")
	if !startsWithDigit(v) {
		return ""
	}
	return v
}

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// readYarn reads yarn.lock in both forms: version 1's own syntax and the YAML
// of later versions.
func (l *Lock) readYarn(data []byte) error {
	if !bytes.Contains(data, []byte("# yarn lockfile v1")) && l.readYarnBerry(data) {
		return nil
	}
	return l.readYarnV1(data)
}

// readYarnBerry reads a YAML yarn.lock, reporting whether it was one.
func (l *Lock) readYarnBerry(data []byte) bool {
	var berry map[string]struct {
		Version string            `yaml:"version"`
		Peers   map[string]string `yaml:"peerDependencies"`
	}
	if yaml.Unmarshal(data, &berry) != nil || len(berry) == 0 {
		return false
	}
	for header, p := range berry {
		for _, name := range yarnNames(header) {
			if p.Version != "" {
				l.Packages[name] = Package{Version: p.Version, Peers: sortedKeys(p.Peers)}
			}
		}
	}
	return true
}

// readYarnV1 reads yarn.lock version 1: header lines naming specs, each
// followed by its indented version.
func (l *Lock) readYarnV1(data []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var names []string
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "#") {
			names = yarnNames(strings.TrimSuffix(line, ":"))
			continue
		}
		if v, ok := strings.CutPrefix(line, "  version "); ok {
			l.lockAll(names, strings.Trim(v, `"`))
		}
	}
	return sc.Err() //nolint:wrapcheck // the caller names the file
}

// lockAll records a version for each name not yet locked.
func (l *Lock) lockAll(names []string, version string) {
	for _, name := range names {
		if _, ok := l.Packages[name]; !ok {
			l.Packages[name] = Package{Version: version}
		}
	}
}

// yarnNames are the packages a yarn.lock header names.
func yarnNames(header string) []string {
	var out []string
	for spec := range strings.SplitSeq(header, ",") {
		if name := yarnName(spec); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// yarnName is the package a yarn.lock header spec names: "react@^18.3.1" and
// "react@npm:^18.3.1" are react.
func yarnName(spec string) string {
	spec = strings.Trim(strings.TrimSpace(spec), `"`)
	i := strings.LastIndex(spec, at)
	if i <= 0 {
		return ""
	}
	return spec[:i]
}

// sortedKeys is a map's keys in order, empty (not nil) for none: a lockfile
// entry that names no peers has none.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Peers reads what a package version peers on when its lockfile does not say:
// the registry's record of it.
type Peers interface {
	PeersOf(ctx context.Context, pkg, version string) ([]string, error)
}

// Registry reads peer dependencies from the npm registry through a fetcher.
type Registry struct {
	Fetch func(ctx context.Context, url string) ([]byte, string, error)
}

// PeersOf reads one version's peerDependencies.
func (r Registry) PeersOf(ctx context.Context, pkg, version string) ([]string, error) {
	if r.Fetch == nil {
		return nil, nil
	}
	body, _, err := r.Fetch(ctx, "https://registry.npmjs.org/"+strings.ReplaceAll(pkg, slash, "%2F")+slash+url.PathEscape(version))
	if err != nil {
		return nil, fmt.Errorf("reading %s@%s from the registry: %w", pkg, version, err)
	}
	var doc struct {
		Peers map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("reading %s@%s from the registry: %w", pkg, version, err)
	}
	return sortedKeys(doc.Peers), nil
}

// peersOf is a locked package's peers, from the lockfile or the registry.
func (l *Lock) peersOf(ctx context.Context, name string, extra Peers) ([]string, error) {
	p := l.Packages[name]
	if p.Peers != nil || extra == nil {
		return p.Peers, nil
	}
	peers, err := extra.PeersOf(ctx, name, p.Version)
	if err != nil {
		return nil, fmt.Errorf("reading what %s peers on: %w", name, err)
	}
	if peers == nil {
		peers = []string{}
	}
	p.Peers = peers
	l.Packages[name] = p
	return peers, nil
}

// PackageOf splits a bare specifier into its package and the path inside it.
func PackageOf(spec string) (pkg, sub string) {
	first, rest, _ := strings.Cut(spec, slash)
	if !strings.HasPrefix(spec, at) || rest == "" {
		return first, rest
	}
	name, sub, _ := strings.Cut(rest, slash)
	return first + slash + name, sub
}

// Generated is a proposed imports map and what could not go in it.
type Generated struct {
	Imports map[string]string `json:"imports"`
	// Lockfile is the file versions were read from.
	Lockfile string `json:"lockfile"`
	// Missing are the specifiers whose package the lockfile does not lock.
	Missing []string `json:"missing"`
}

// Generate maps every bare specifier to its locked version on the CDN, each
// path inside a package its own entry, and every package's locked peers
// pinned through ?deps= (#2785).
func Generate(ctx context.Context, specifiers []string, lock *Lock, extra Peers) (*Generated, error) {
	out := &Generated{Imports: map[string]string{}, Lockfile: lock.File, Missing: []string{}}
	for _, spec := range specifiers {
		pkg, sub := PackageOf(spec)
		p, ok := lock.Packages[pkg]
		if !ok {
			out.Missing = append(out.Missing, spec)
			continue
		}
		peers, err := lock.peersOf(ctx, pkg, extra)
		if err != nil {
			return nil, err
		}
		u := CDN + pkg + at + p.Version
		if sub != "" {
			u += slash + sub
		}
		if deps := lock.pins(peers); deps != "" {
			u += "?deps=" + deps
		}
		out.Imports[spec] = u
	}
	slices.Sort(out.Missing)
	return out, nil
}

// pins is the deps= value for a package's peers: each locked peer at its
// locked version.
func (l *Lock) pins(peers []string) string {
	var parts []string
	for _, peer := range peers {
		if p, ok := l.Packages[peer]; ok {
			parts = append(parts, peer+at+p.Version)
		}
	}
	return strings.Join(parts, ",")
}
