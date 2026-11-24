// Package tailwind compiles a site's Tailwind CSS 4 stylesheet at publish
// (#2780), in-process: Tailwind's own compiler, fetched from the npm registry
// at the exact version the release names and checked against the registry's
// integrity hash, runs in goja. It is given CSS and the class names the
// release's files hold, and nothing else: no module loader, so @plugin and
// @config are refused, and no site code runs.
package tailwind

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

// Registry is where Tailwind is fetched from.
const Registry = "https://registry.npmjs.org/tailwindcss/"

// The stylesheets Tailwind's own @imports name, by the id it asks for.
var packageSheets = map[string]string{
	"tailwindcss":               "index.css",
	"tailwindcss/index.css":     "index.css",
	"tailwindcss/theme":         "theme.css",
	"tailwindcss/theme.css":     "theme.css",
	"tailwindcss/preflight":     "preflight.css",
	"tailwindcss/preflight.css": "preflight.css",
	"tailwindcss/utilities":     "utilities.css",
	"tailwindcss/utilities.css": "utilities.css",
}

// maxPackageFile bounds one file read out of the package.
const maxPackageFile = 8 << 20

// Fetch reads a URL; the platform's is the egress-guarded module fetcher.
type Fetch func(ctx context.Context, url string) (body []byte, contentType string, err error)

// Package is one version of Tailwind, ready to run: its compiler rewritten as
// a script goja runs, and the stylesheets it imports.
type Package struct {
	Version string
	script  string
	sheets  map[string]string
}

// Loader fetches and prepares Tailwind versions, keeping each it prepared.
type Loader struct {
	Fetch Fetch

	mu       sync.Mutex
	versions map[string]*Package
}

// Load is one version, fetched and checked the first time it is asked for.
func (l *Loader) Load(ctx context.Context, version string) (*Package, error) {
	l.mu.Lock()
	if p, ok := l.versions[version]; ok {
		l.mu.Unlock()
		return p, nil
	}
	l.mu.Unlock()
	if l.Fetch == nil {
		return nil, errors.New("this deployment cannot fetch Tailwind CSS")
	}
	p, err := l.fetch(ctx, version)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.versions == nil {
		l.versions = map[string]*Package{}
	}
	l.versions[version] = p
	return p, nil
}

func (l *Loader) fetch(ctx context.Context, version string) (*Package, error) {
	meta, _, err := l.Fetch(ctx, Registry+version)
	if err != nil {
		return nil, fmt.Errorf("tailwind %s: the npm registry has no such version, or could not be read (%w)", version, err)
	}
	var doc struct {
		Version string `json:"version"`
		Dist    struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}
	if err := json.Unmarshal(meta, &doc); err != nil || doc.Version != version || doc.Dist.Tarball == "" {
		return nil, fmt.Errorf("tailwind %s: the npm registry has no such version", version)
	}
	if !strings.HasPrefix(doc.Dist.Tarball, "https://registry.npmjs.org/") {
		return nil, fmt.Errorf("tailwind %s: the registry names its package at %q, outside the registry", version, doc.Dist.Tarball)
	}
	tarball, _, err := l.Fetch(ctx, doc.Dist.Tarball)
	if err != nil {
		return nil, fmt.Errorf("tailwind %s: reading its package: %w", version, err)
	}
	if err := checkIntegrity(tarball, doc.Dist.Integrity); err != nil {
		return nil, fmt.Errorf("tailwind %s: %w", version, err)
	}
	files, err := untar(tarball)
	if err != nil {
		return nil, fmt.Errorf("tailwind %s: reading its package: %w", version, err)
	}
	return Prepare(version, files)
}

// checkIntegrity holds a package to the sha512 the registry publishes for it.
func checkIntegrity(body []byte, integrity string) error {
	want, ok := strings.CutPrefix(integrity, "sha512-")
	if !ok {
		return fmt.Errorf("the registry publishes no sha512 integrity for it (%q)", integrity)
	}
	sum := sha512.Sum512(body)
	if base64.StdEncoding.EncodeToString(sum[:]) != want {
		return errors.New("its package fails the registry's integrity check")
	}
	return nil
}

// untar reads the files of an npm package tarball under package/.
func untar(body []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("not a gzipped package: %w", err)
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the package: %w", err)
		}
		name, ok := strings.CutPrefix(h.Name, "package/")
		if !ok || h.Typeflag != tar.TypeReg || h.Size > maxPackageFile {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxPackageFile))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		files[path.Clean(name)] = data
	}
}

// Prepare rewrites a package's compiler (dist/lib.js) into one script that
// sets a global the runner calls, and keeps the stylesheets it imports.
func Prepare(version string, files map[string][]byte) (*Package, error) {
	if _, ok := files["dist/lib.js"]; !ok {
		return nil, fmt.Errorf("tailwind %s: its package holds no dist/lib.js; versions before 4 are not supported", version)
	}
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{"dist/lib.js"},
		Bundle:      true,
		Write:       false,
		Format:      api.FormatIIFE,
		GlobalName:  "__tailwind",
		Platform:    api.PlatformNeutral,
		Target:      api.ES2017,
		LogLevel:    api.LogLevelSilent,
		Plugins: []api.Plugin{{Name: "package", Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: `.*`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				name := path.Clean(path.Join(path.Dir(args.Importer), args.Path))
				if args.Kind == api.ResolveEntryPoint {
					name = args.Path
				}
				if _, ok := files[name]; !ok {
					return api.OnResolveResult{}, fmt.Errorf("tailwind's compiler imports %q, which its package does not hold", args.Path)
				}
				return api.OnResolveResult{Path: name, Namespace: "tw"}, nil
			})
			b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "tw"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				contents := string(files[args.Path])
				return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
			})
		}}},
	})
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("tailwind %s: preparing its compiler: %s", version, result.Errors[0].Text)
	}
	p := &Package{Version: version, script: string(result.OutputFiles[0].Contents), sheets: map[string]string{}}
	for id, file := range packageSheets {
		if data, ok := files[file]; ok {
			p.sheets[id] = string(data)
		}
	}
	return p, nil
}
