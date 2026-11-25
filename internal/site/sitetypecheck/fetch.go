package sitetypecheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/outbound"
)

// Bounds on what a check fetches and keeps.
const (
	maxTarballBytes   = 64 << 20
	maxCompilerBytes  = 96 << 20
	maxKeptFileBytes  = 4 << 20
	maxKeptTotalBytes = 64 << 20
	fetchTimeout      = 2 * time.Minute
)

// slash separates the parts of a package path and an object key.
const slash = "/"

// The integrity algorithms a check holds a tarball to, weakest first.
const (
	algoSHA256 = "sha256"
	algoSHA384 = "sha384"
	algoSHA512 = "sha512"
)

// Getter fetches a URL's body, refusing one past limit bytes.
type Getter interface {
	Get(ctx context.Context, url string, limit int64) ([]byte, error)
}

// httpGetter is a Getter over an HTTP client.
type httpGetter struct{ client *http.Client }

// NewGuardedGetter is the getter a deployment fetches with: every dial passes
// the egress guard, so no URL a lockfile names reaches a private, loopback,
// link-local or metadata address, and no redirect is followed.
func NewGuardedGetter() (Getter, error) {
	guard, err := egressguard.New(nil)
	if err != nil {
		return nil, fmt.Errorf("building the egress guard: %w", err)
	}
	client := outbound.NewClient(outbound.Options{
		Kind: outbound.KindSite, Base: guard.Transport(), Timeout: fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	})
	return httpGetter{client: client}, nil
}

// Get fetches url, refusing a body past limit bytes or an answer other than
// 200.
func (g httpGetter) Get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: the registry answered %d", url, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("the body of %s is larger than the %d bytes a check fetches", url, limit)
	}
	return data, nil
}

// ErrIntegrity is a fetched file that does not match the hash its lockfile
// or the platform pins.
var ErrIntegrity = errors.New("integrity mismatch")

// verifyIntegrity checks data against a Subresource Integrity string: one or
// more algo-base64 hashes, the strongest of which must match. Only SHA-256 and
// stronger count: a SHA-1 hash cannot hold a tarball to what was locked.
func verifyIntegrity(data []byte, integrity string) error {
	best, bestRank := "", 0
	for h := range strings.FieldsSeq(integrity) {
		algo, _, ok := strings.Cut(h, "-")
		if r := algoRank(algo); ok && r > bestRank {
			best, bestRank = h, r
		}
	}
	if best == "" {
		return fmt.Errorf("no hash it can check in %q: %w", integrity, ErrIntegrity)
	}
	algo, want, _ := strings.Cut(best, "-")
	h := newHash(algo)
	_, _ = h.Write(data)
	if base64.StdEncoding.EncodeToString(h.Sum(nil)) != want {
		return ErrIntegrity
	}
	return nil
}

// checkable reports an integrity string holding a hash verifyIntegrity checks.
func checkable(integrity string) bool {
	for h := range strings.FieldsSeq(integrity) {
		if algo, _, ok := strings.Cut(h, "-"); ok && algoRank(algo) > 0 {
			return true
		}
	}
	return false
}

// algoRank orders the algorithms a check holds a tarball to, 0 for one it
// does not.
func algoRank(algo string) int {
	return slices.Index([]string{algoSHA256, algoSHA384, algoSHA512}, algo) + 1
}

func newHash(algo string) hash.Hash {
	switch algo {
	case algoSHA512:
		return sha512.New()
	case algoSHA384:
		return sha512.New384()
	default:
		return sha256.New()
	}
}

// keep reports a file of a package tarball a check keeps: its package.json
// and its declaration files.
func keep(name string) bool {
	base := path.Base(name)
	return base == "package.json" || strings.HasSuffix(name, ".d.ts") ||
		strings.HasSuffix(name, ".d.mts") || strings.HasSuffix(name, ".d.cts")
}

// typesOf reads a package tarball and keeps its package.json and declaration
// files, by their path inside the package. A path that leaves the package is
// refused.
func typesOf(tgz []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("not a gzip tarball: %w", err)
	}
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	total := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the tarball: %w", err)
		}
		name, ok := keptEntry(hdr)
		if !ok {
			continue
		}
		if hdr.Size > maxKeptFileBytes || total+int(hdr.Size) > maxKeptTotalBytes {
			return nil, errors.New("the package's declaration files are larger than a check keeps")
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		total += len(data)
		out[name] = data
	}
}

// keptEntry is a tarball entry's path inside the package when a check keeps
// it: a regular file that is the package.json or a declaration file.
func keptEntry(hdr *tar.Header) (string, bool) {
	// An entry naming a parent directory is refused outright, whatever the
	// cleaning below would make of it.
	if hdr.Typeflag != tar.TypeReg || strings.Contains(hdr.Name, "..") {
		return "", false
	}
	name, ok := packagePath(hdr.Name)
	if !ok || !keep(name) {
		return "", false
	}
	return name, true
}

// packagePath is a tarball entry's path inside the package: the first
// directory (package/, or the name a registry chose) dropped, and refused
// when it leaves the package.
func packagePath(entry string) (string, bool) {
	entry = strings.TrimPrefix(path.Clean(slash+entry), slash)
	_, rest, ok := strings.Cut(entry, slash)
	if !ok || rest == "" || strings.HasPrefix(rest, "..") {
		return "", false
	}
	return rest, true
}

// Cache keeps fetched, verified files in the platform's bucket by the hash
// they were verified against, so a second check fetches nothing.
type Cache struct {
	Objects Objects
	Bucket  string
	Prefix  string
}

// Objects is the bucket a cache writes to.
type Objects interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
}

// key is a cached entry's object key for an integrity string.
func (c Cache) key(kind, integrity string) string {
	sum := sha256.Sum256([]byte(integrity))
	return strings.TrimRight(c.Prefix, slash) + slash + kind + slash + hex.EncodeToString(sum[:]) + ".json"
}

// get reads a cached file set, false when absent.
func (c Cache) get(ctx context.Context, kind, integrity string) (map[string][]byte, bool) {
	if c.Objects == nil || c.Bucket == "" {
		return nil, false
	}
	raw, _, err := c.Objects.GetObject(ctx, c.Bucket, c.key(kind, integrity))
	if err != nil {
		return nil, false
	}
	var files map[string][]byte
	if json.Unmarshal(raw, &files) != nil {
		return nil, false
	}
	return files, true
}

// put stores a file set; a failure only costs the next check a fetch.
func (c Cache) put(ctx context.Context, kind, integrity string, files map[string][]byte) {
	if c.Objects == nil || c.Bucket == "" {
		return
	}
	raw, err := json.Marshal(files)
	if err != nil {
		return
	}
	_ = c.Objects.PutObject(ctx, c.Bucket, c.key(kind, integrity), raw, "application/json")
}

// fetchTypes is a package's kept files: from the cache, or fetched, verified
// against its integrity and filtered. fetched reports a registry read.
func fetchTypes(ctx context.Context, g Getter, c Cache, t Tarball) (files map[string][]byte, fetched bool, err error) {
	if files, ok := c.get(ctx, "pkg", t.Integrity); ok {
		return files, false, nil
	}
	tgz, err := g.Get(ctx, t.URL, maxTarballBytes)
	if err != nil {
		return nil, true, fmt.Errorf("package %s@%s: %w", t.Name, t.Version, err)
	}
	if err := verifyIntegrity(tgz, t.Integrity); err != nil {
		return nil, true, fmt.Errorf("package %s@%s: %w", t.Name, t.Version, err)
	}
	files, err = typesOf(tgz)
	if err != nil {
		return nil, true, fmt.Errorf("package %s@%s: %w", t.Name, t.Version, err)
	}
	c.put(ctx, "pkg", t.Integrity, files)
	return files, true, nil
}
