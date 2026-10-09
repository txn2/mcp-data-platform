// Package maphttp serves the basemap archives the maps settings keep (#2068)
// and the list of them, with no session.
//
// The archives hold public OpenStreetMap data, and they are read by a map
// running in a sandboxed asset frame, whose origin is opaque, and from public
// share links, neither of which carries a session. corshttp answers these
// paths for any origin. PMTiles is read only by byte range, so the archive
// route is the tile server: each map view reads the header, a directory or
// two, and the tiles it draws.
package maphttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/maps"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// PathPrefix is where the routes live. It is under /portal/ so the tile
// worker answers it in-process, and is registered ahead of the portal UI's
// catch-all.
const PathPrefix = "/portal/maps/"

// regionsPath is maps.RegionsPath, spelled where the route scanner can fold
// it; a test holds the two together.
const regionsPath = PathPrefix + "regions"

// Service is what the routes read. *maps.Service satisfies it.
type Service interface {
	State(ctx context.Context) (maps.State, error)
	ServedArchive(ctx context.Context, id string) (maps.Archive, maps.Objects, error)
}

// archiveCacheControl lets a browser keep what it read and asks it to
// revalidate each use against the ETag. A refresh replaces the archive under
// the same URL; a byte range kept from the old one and read beside the new
// one's header would be a different build's tile, so nothing is reused
// without the ETag matching.
const archiveCacheControl = "public, no-cache"

// Mount registers the routes. A nil service mounts nothing, and the portal
// UI's catch-all answers the paths as it does any other.
func Mount(mux *http.ServeMux, svc Service) {
	if svc == nil {
		return
	}
	mux.HandleFunc("GET "+regionsPath, func(w http.ResponseWriter, r *http.Request) {
		state, err := svc.State(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "maps: listing regions", "error", logsan.SanitizeForLog(err.Error()))
			httpjson.WriteError(w, http.StatusInternalServerError, "the map regions could not be read")
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		httpjson.WriteJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("GET "+PathPrefix+"{file}", func(w http.ResponseWriter, r *http.Request) {
		serveArchive(w, r, svc)
	})
}

func serveArchive(w http.ResponseWriter, r *http.Request, svc Service) {
	id, ok := strings.CutSuffix(r.PathValue("file"), ".pmtiles")
	if !ok {
		http.NotFound(w, r)
		return
	}
	archive, objects, err := svc.ServedArchive(r.Context(), id)
	if errors.Is(err, maps.ErrUnavailable) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "maps: reading an archive", "region", logsan.SanitizeForLog(id),
			"error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the map archive could not be read")
		return
	}
	end := rangeEnd(r.Header.Get("Range"))
	source := blobserve.NewRangeSource(archive.Size, func(offset, length int64) ([]byte, error) {
		if end >= offset {
			length = min(length, end-offset+1)
		}
		body, _, err := objects.GetObjectRange(r.Context(), archive.Bucket, archive.Key, offset, length)
		return body, err //nolint:wrapcheck // named by RangeSource
	})
	w.Header().Set("Cache-Control", archiveCacheControl)
	w.Header().Set("ETag", archive.ETag())
	blobserve.Serve(w, r, blobserve.Options{
		Name: id + ".pmtiles", ContentType: "application/vnd.pmtiles", ModTime: archive.ReadyAt, Source: source,
	})
}

// A Range header's offsets are decimal and fit an int64.
const (
	decimal    = 10
	offsetBits = 64
)

// rangeEnd returns the last byte a single closed range asks for, or -1 for
// anything else (an open range, a suffix range, several ranges). A map reads
// a few kilobytes at a time; without it every read would fetch a whole block
// from the bucket.
func rangeEnd(h string) int64 {
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return -1
	}
	first, last, ok := strings.Cut(spec, "-")
	if !ok || strings.TrimSpace(first) == "" || last == "" {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(last), decimal, offsetBits)
	if err != nil || n < 0 {
		return -1
	}
	return n
}
