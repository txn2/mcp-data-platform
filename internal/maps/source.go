package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

// Where the current Protomaps build is found. Protomaps keeps a week of daily
// builds and one per version and says their URLs may change, so a fetch
// resolves the newest daily build from the index when it starts rather than
// storing a file name.
const (
	ProtomapsIndexURL = "https://build-metadata.protomaps.dev/builds.json"
	ProtomapsBuildURL = "https://build.protomaps.com/"
)

// maxIndexBytes bounds the build index read: it lists a few dozen builds.
const maxIndexBytes = 1 << 20

// dailyBuild is a daily build's file name: its date, YYYYMMDD.
var dailyBuild = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})\.pmtiles$`)

// sourceRef is the archive a fetch reads and the name of its build.
type sourceRef struct {
	url   string
	build string
}

// resolveSource returns the archive the settings name: the configured URL, or
// the newest daily build in the Protomaps index.
func resolveSource(ctx context.Context, client *http.Client, s Settings, indexURL, buildURL string) (sourceRef, error) {
	if s.SourceURL != "" {
		name := s.SourceURL[strings.LastIndex(s.SourceURL, "/")+1:]
		return sourceRef{url: s.SourceURL, build: strings.TrimSuffix(name, ".pmtiles")}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, http.NoBody)
	if err != nil {
		return sourceRef{}, fmt.Errorf("building the build index request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return sourceRef{}, fmt.Errorf("reading the Protomaps build index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return sourceRef{}, fmt.Errorf("reading the Protomaps build index: HTTP %d", resp.StatusCode)
	}
	var builds []struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxIndexBytes)).Decode(&builds); err != nil {
		return sourceRef{}, fmt.Errorf("decoding the Protomaps build index: %w", err)
	}
	keys := make([]string, 0, len(builds))
	for _, b := range builds {
		if dailyBuild.MatchString(b.Key) {
			keys = append(keys, b.Key)
		}
	}
	if len(keys) == 0 {
		return sourceRef{}, errors.New("the Protomaps build index lists no daily build")
	}
	sort.Strings(keys)
	latest := keys[len(keys)-1]
	return sourceRef{url: buildURL + latest, build: dailyBuild.ReplaceAllString(latest, "$1-$2-$3")}, nil
}

// buildDate names the data an archive was built from: the OpenStreetMap
// replication time its metadata carries, as a date, else the source's name.
func buildDate(meta map[string]any, fallback string) string {
	if v, ok := meta["planetiler:osm:osmosisreplicationtime"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UTC().Format(time.DateOnly)
		}
	}
	return fallback
}

// rangeAttempts is how many times one range read is tried. A build host that
// drops a connection mid-extract is ordinary over gigabytes of reads.
const rangeAttempts = 4

// httpSource reads an archive over HTTP by byte range. The first answer's
// entity tag is sent with every later read as If-Match, so a source replaced
// mid-fetch fails the fetch instead of mixing two builds into one archive.
type httpSource struct {
	client *http.Client
	url    string
	pause  func(attempt int) time.Duration

	mu   sync.Mutex
	etag string
}

func (s *httpSource) tag() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.etag
}

func (s *httpSource) keepTag(etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.etag == "" {
		s.etag = etag
	}
}

// defaultRetryPause is the wait before a range read's attempt: 1s, 4s, 9s.
func defaultRetryPause(attempt int) time.Duration {
	return time.Duration(attempt*attempt) * time.Second
}

func newHTTPSource(client *http.Client, url string, pause func(attempt int) time.Duration) *httpSource {
	return &httpSource{client: client, url: url, pause: pause}
}

// errNoRanges is a source server that answers a range read with the whole
// file.
var errNoRanges = errors.New("the source server does not answer byte ranges")

// errSourceChanged is a read refused because the archive is no longer the one
// the fetch started on.
var errSourceChanged = errors.New("the source archive changed while it was being read; refresh the region to fetch the new build")

// ReadRange implements pmtiles.Source.
func (s *httpSource) ReadRange(ctx context.Context, offset, length uint64) ([]byte, error) {
	var lastErr error
	for attempt := range rangeAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err() //nolint:wrapcheck // the caller's own cancellation
			case <-time.After(s.pause(attempt)):
			}
		}
		b, retry, err := s.read(ctx, offset, length)
		if err == nil {
			return b, nil
		}
		if !retry {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *httpSource) read(ctx context.Context, offset, length uint64) (body []byte, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, http.NoBody)
	if err != nil {
		return nil, false, fmt.Errorf("building a range request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	if etag := s.tag(); etag != "" {
		req.Header.Set("If-Match", etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, fmt.Errorf("reading the source archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, retry, err = readAnswer(resp, offset, length)
	if err == nil && body != nil {
		s.keepTag(resp.Header.Get("ETag"))
	}
	return body, retry, err
}

// readAnswer reads a range request's response: the bytes asked for, or why
// there are none and whether asking again could help.
func readAnswer(resp *http.Response, offset, length uint64) (body []byte, retry bool, err error) {
	switch {
	case resp.StatusCode == http.StatusPreconditionFailed:
		return nil, false, errSourceChanged
	case resp.StatusCode == http.StatusOK && offset == 0:
		return readWhole(resp, length)
	case resp.StatusCode == http.StatusPartialContent:
		body, err = io.ReadAll(io.LimitReader(resp.Body, int64(length))) // #nosec G115 -- a range read is far below 2^63
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return []byte{}, false, nil
	default:
		return nil, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError,
			fmt.Errorf("reading the source archive: HTTP %d", resp.StatusCode)
	}
	if err != nil {
		return nil, true, fmt.Errorf("reading the source archive: %w", err)
	}
	return body, false, nil
}

// readWhole reads a server's answer of the whole file to a range read from
// its start. That is the read only for a file shorter than it.
func readWhole(resp *http.Response, length uint64) (body []byte, retry bool, err error) {
	body, err = io.ReadAll(io.LimitReader(resp.Body, int64(length)+1)) // #nosec G115 -- a range read is far below 2^63
	if err != nil {
		return nil, true, fmt.Errorf("reading the source archive: %w", err)
	}
	if uint64(len(body)) > length {
		return nil, false, errNoRanges
	}
	return body, false, nil
}

var _ pmtiles.Source = (*httpSource)(nil)
