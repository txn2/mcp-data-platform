package httpobs

import (
	"net/http"
	"strings"
)

// responseWriter records the status and whether the response is an event
// stream, and keeps the streaming contract the MCP transports need: Flush,
// so a handler that type-asserts http.Flusher (the session-aware SSE stream)
// finds it, and Unwrap, so http.ResponseController reaches the writer below
// for anything else (SetWriteDeadline, Hijack). The gateway's statusRecorder
// has neither and was not a safe template.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the first status written and forwards it.
func (w *responseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write forwards the body, committing a 200 first when no header was written.
func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b) //nolint:wrapcheck // the writer below's error, as it is
}

// Flush forwards to the writer below when it can flush, and marks the
// response written: a stream's first flush is its status line.
func (w *responseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is what http.ResponseController reads through.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// statusCode is the status written, or 200 for a handler that wrote a body
// with no explicit header, or 200 for one that wrote nothing: net/http sends
// 200 for both.
func (w *responseWriter) statusCode() int {
	if !w.wroteHeader {
		return http.StatusOK
	}
	return w.status
}

// isEventStream reports whether the response is a server-sent event stream.
func (w *responseWriter) isEventStream() bool {
	return strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
}

// isOpenStream reports whether the response is a stream the client holds
// open: a GET answered as an event stream (the SSE transport's /sse, the
// streamable transport's standalone listen). Its duration is the client's
// choice and is not a slow request. A POST answered as an event stream is
// a streamable MCP message whose stream ends with its result: a request,
// slow when it is slow.
func (w *responseWriter) isOpenStream(method string) bool {
	return method == http.MethodGet && w.isEventStream()
}
