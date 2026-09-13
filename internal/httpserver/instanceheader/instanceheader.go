// Package instanceheader names, on every HTTP response, the platform process
// that served it. Replicas of one deployment share a database and differ in the
// state each holds in memory, so a defect of that kind is visible only to a
// reader who can tell which replica answered (#1708).
package instanceheader

import (
	"net"
	"net/http"
	"os"
)

// Header is the response header that carries the process name.
const Header = "X-Platform-Instance"

// unknownHost stands in for a hostname the operating system would not report.
const unknownHost = "unknown"

// Name is the header value for a process listening on addr: the hostname,
// which separates pods, and the listen port, which separates two processes on
// one machine.
func Name(hostname, addr string) string {
	if hostname == "" {
		hostname = unknownHost
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return hostname
	}
	return net.JoinHostPort(hostname, port)
}

// HostName is Name for this process.
func HostName(addr string) string {
	hostname, _ := os.Hostname() //nolint:errcheck // Name substitutes a placeholder for an unreported hostname
	return Name(hostname, addr)
}

// Middleware sets Header on every response, before the handler runs, so a
// refusal written by any layer below carries it.
func Middleware(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(Header, name)
		next.ServeHTTP(w, r)
	})
}
