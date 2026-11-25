// Package sitetypecheck type-checks a site release with the TypeScript 7
// compiler (#2793), run as WebAssembly under wazero in a worker process of its
// own, after the release publishes.
//
// esbuild compiles a site's TypeScript at publish by stripping its types, so a
// type error publishes. This is the check a team's editor and CI gave it: a
// background check of each release whose site asks for one in _build.json,
// reported on the release with each error's file, line, column and code. It
// never holds a publish back.
//
// The compiler is the pinned @typescript/typescript-wasip1-wasm build, fetched
// from the npm registry once, verified against its integrity hash and kept in
// the platform's bucket. A release's dependency types come from its lockfile:
// each package at its locked version, verified the same way, with only its
// package.json and declaration files kept. No package's install script runs.
package sitetypecheck

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// Tarball is one locked package as a check fetches it.
type Tarball struct {
	// Name is the package name, scoped or not.
	Name string
	// Version is the locked version.
	Version string
	// Dir is where the package is laid out under node_modules, as the
	// lockfile places it.
	Dir string
	// URL is the tarball, and Integrity its Subresource Integrity hash.
	URL       string
	Integrity string
}

// Locked is what a lockfile names for a check.
type Locked struct {
	// File is the lockfile read, "" for none.
	File string
	// Packages are the tarballs to fetch.
	Packages []Tarball
	// Unverifiable are packages the lockfile names without an integrity
	// hash, which a check does not fetch.
	Unverifiable []string
}

// maxLockBytes bounds a lockfile read.
const maxLockBytes = 32 << 20

// defaultRegistry is where a tarball the lockfile does not locate is fetched.
const defaultRegistry = "https://registry.npmjs.org/"

// readLocked reads every package a release's lockfile locks.
func readLocked(src fs.FS, registry string) (*Locked, error) {
	for _, name := range []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock"} {
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
		out := &Locked{File: name}
		switch name {
		case "package-lock.json":
			err = out.readNPM(data)
		case "pnpm-lock.yaml":
			err = out.readPNPM(data, registry)
		default:
			err = out.readYarn(data)
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		sort.Slice(out.Packages, func(i, j int) bool { return out.Packages[i].Dir < out.Packages[j].Dir })
		sort.Strings(out.Unverifiable)
		return out, nil
	}
	return &Locked{}, nil
}

// readNPM reads package-lock.json versions 2 and 3: every installed path with
// its resolved tarball and integrity.
func (l *Locked) readNPM(data []byte) error {
	var doc struct {
		Packages map[string]struct {
			Version   string `json:"version"`
			Resolved  string `json:"resolved"`
			Integrity string `json:"integrity"`
			Link      bool   `json:"link"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}
	for key, p := range doc.Packages {
		dir, name, ok := npmDir(key)
		if !ok || p.Link {
			continue
		}
		l.add(Tarball{Name: name, Version: p.Version, Dir: dir, URL: p.Resolved, Integrity: p.Integrity})
	}
	return nil
}

// npmDir is a package-lock.json key's directory and package name:
// node_modules/a/node_modules/@s/b is @s/b at that path. The root ("") and
// workspace folders are not packages.
func npmDir(key string) (dir, name string, ok bool) {
	_, name, found := strings.CutLast(key, "node_modules/")
	if !found {
		return "", "", false
	}
	if name == "" || !validPackageName(name) || !fs.ValidPath(key) {
		return "", "", false
	}
	return key, name, true
}

// readPNPM reads pnpm-lock.yaml: every package with its integrity, laid out
// flat under node_modules, one version per name (the one the project depends
// on directly when it locks more than one).
func (l *Locked) readPNPM(data []byte, registry string) error {
	var doc struct {
		Packages map[string]struct {
			Resolution struct {
				Integrity string `yaml:"integrity"`
				Tarball   string `yaml:"tarball"`
			} `yaml:"resolution"`
		} `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}
	best := map[string]Tarball{}
	for key, p := range doc.Packages {
		name, version, ok := pnpmPackage(key)
		if !ok {
			continue
		}
		t := Tarball{
			Name: name, Version: version, Dir: "node_modules/" + name,
			URL: p.Resolution.Tarball, Integrity: p.Resolution.Integrity,
		}
		if t.URL == "" {
			t.URL = registryTarball(registry, name, version)
		}
		if prev, seen := best[name]; !seen || semver.Compare("v"+prev.Version, "v"+version) < 0 {
			best[name] = t
		}
	}
	for _, t := range best {
		l.add(t)
	}
	return nil
}

// pnpmPackage reads a pnpm packages key: /name@1.2.3, name@1.2.3 or
// @scope/name@1.2.3, with any (peer) suffix dropped.
func pnpmPackage(key string) (name, version string, ok bool) {
	key = strings.TrimPrefix(key, "/")
	key, _, _ = strings.Cut(key, "(")
	i := strings.LastIndex(key, "@")
	if i <= 0 {
		return "", "", false
	}
	name, version = key[:i], key[i+1:]
	if !validPackageName(name) || version == "" || version[0] < '0' || version[0] > '9' {
		return "", "", false
	}
	return name, version, true
}

// readYarn reads a Yarn 1 lockfile: each entry's version, resolved tarball and
// integrity. A Yarn 2+ lockfile carries a checksum of its own cache, not the
// npm integrity hash, so none of its packages can be verified.
func (l *Locked) readYarn(data []byte) error {
	if !bytes.Contains(data, []byte("# yarn lockfile v1")) {
		return errors.New("a Yarn 2+ lockfile carries no npm integrity hash; use package-lock.json, pnpm-lock.yaml or a Yarn 1 lockfile")
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var cur Tarball
	flush := func() {
		if cur.Name != "" {
			cur.Dir = "node_modules/" + cur.Name
			l.add(cur)
		}
		cur = Tarball{}
	}
	for sc.Scan() {
		line := sc.Text()
		if yarnHeader(line) {
			flush()
			cur.Name = yarnName(strings.TrimSuffix(line, ":"))
			continue
		}
		cur.yarnField(line)
	}
	flush()
	return sc.Err() //nolint:wrapcheck // the caller names the file
}

// yarnHeader reports a Yarn 1 line that starts an entry: unindented, not a
// comment, ending in a colon.
func yarnHeader(line string) bool {
	return line != "" && !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "#")
}

// yarnField reads one of an entry's fields a check uses: its version, its
// resolved tarball (without the fragment) and its integrity.
func (t *Tarball) yarnField(line string) {
	switch {
	case strings.HasPrefix(line, "  version "):
		t.Version = strings.Trim(strings.TrimPrefix(line, "  version "), `"`)
	case strings.HasPrefix(line, "  resolved "):
		t.URL, _, _ = strings.Cut(strings.Trim(strings.TrimPrefix(line, "  resolved "), `"`), "#")
	case strings.HasPrefix(line, "  integrity "):
		t.Integrity = strings.TrimSpace(strings.TrimPrefix(line, "  integrity "))
	}
}

// yarnName is the package a Yarn 1 header names: its first spec's name.
func yarnName(header string) string {
	spec, _, _ := strings.Cut(header, ",")
	spec = strings.Trim(strings.TrimSpace(spec), `"`)
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return ""
	}
	if name := spec[:i]; validPackageName(name) {
		return name
	}
	return ""
}

// add records a package, or names it as unverifiable when the lockfile gives
// no tarball or no integrity hash a check can hold it to (SHA-256 or stronger).
func (l *Locked) add(t Tarball) {
	if !checkable(t.Integrity) || t.URL == "" || !strings.HasPrefix(t.URL, "https://") {
		l.Unverifiable = append(l.Unverifiable, t.Name+"@"+t.Version)
		return
	}
	l.Packages = append(l.Packages, t)
}

// registryTarball is a package's tarball on a registry.
func registryTarball(registry, name, version string) string {
	base := path.Base(name)
	return strings.TrimRight(registry, slash) + slash + name + "/-/" + base + "-" + version + ".tgz"
}

// maxPackageNameLen is the longest name npm publishes.
const maxPackageNameLen = 214

// validPackageName is a name npm would publish: a word, or @scope/word, of
// lowercase letters, digits and . _ -.
func validPackageName(name string) bool {
	if name == "" || len(name) > maxPackageNameLen {
		return false
	}
	parts, ok := packageNameParts(name)
	if !ok {
		return false
	}
	for _, p := range parts {
		if !validNamePart(p) {
			return false
		}
	}
	return true
}

// packageNameParts is a name's word, or its scope and word without the @.
func packageNameParts(name string) ([]string, bool) {
	parts := strings.Split(name, slash)
	if !strings.HasPrefix(name, "@") {
		return parts, len(parts) == 1
	}
	if len(parts) != 2 || len(parts[0]) < 2 {
		return nil, false
	}
	return []string{parts[0][1:], parts[1]}, true
}

// validNamePart is one word of a package name.
func validNamePart(p string) bool {
	if p == "" || p == "." || p == ".." {
		return false
	}
	for _, r := range p {
		if !nameRune(r) {
			return false
		}
	}
	return true
}

// nameRune is a rune a package name may hold: lower case, a digit, or . _ -.
func nameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r)
}
