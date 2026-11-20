// Package reqlog logs a failure an HTTP handler met while serving a request
// (#2728).
//
// A handler whose caller went away mid-request (a browser panning a map away,
// the tile renderer abandoning a page) sees its reads fail with the request's
// own context canceled. Nothing failed on the server: nobody was waiting for
// the answer any more. Logged at ERROR, those lines read as outages to every
// soak, alert and dashboard that counts ERROR, and hide the real failures
// that look the same. Failed logs them at DEBUG, and a failure met while the
// caller is still there at ERROR, in one place for every handler.
// .semgrep/go-request-error-log.yml refuses slog.ErrorContext on a request's
// context anywhere else.
package reqlog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// errorKey is the attribute the failure's text is logged under.
const errorKey = "error"

// CallerGone reports whether the request's caller stopped waiting: its
// context is done, or err is the cancellation that ended the read.
func CallerGone(r *http.Request, err error) bool {
	return r.Context().Err() != nil || errors.Is(err, context.Canceled)
}

// Failed logs msg with err (sanitized for the log) and attrs: at ERROR while
// the caller is waiting, at DEBUG once it has gone. It returns whether the
// caller has gone, so a handler may skip writing an answer nobody reads.
func Failed(r *http.Request, msg string, err error, attrs ...any) bool {
	gone := CallerGone(r, err)
	text := ""
	if err != nil {
		text = logsan.SanitizeForLog(err.Error())
	}
	args := append(slices.Clone(attrs), errorKey, text)
	if gone {
		args = append(args, "caller_gone", true)
		slog.DebugContext(r.Context(), msg, args...) // #nosec G706 -- every value is sanitized by the caller or here
		return true
	}
	slog.ErrorContext(r.Context(), msg, args...) // #nosec G706 -- every value is sanitized by the caller or here
	return false
}
