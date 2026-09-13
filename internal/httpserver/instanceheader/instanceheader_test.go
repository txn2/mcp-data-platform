package instanceheader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestName(t *testing.T) {
	tests := []struct {
		name, hostname, addr, want string
	}{
		{"wildcard listen address", "pod-a", ":8080", "pod-a:8080"},
		{"host listen address", "pod-b", "127.0.0.1:28081", "pod-b:28081"},
		{"no port", "pod-c", "localhost", "pod-c"},
		{"no hostname", "", ":8080", "unknown:8080"},
		{"ipv6 listen address", "pod-d", "[::1]:9000", "pod-d:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Name(tt.hostname, tt.addr); got != tt.want {
				t.Errorf("Name(%q, %q) = %q, want %q", tt.hostname, tt.addr, got, tt.want)
			}
		})
	}
}

func TestHostName(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname: %v", err)
	}
	if got, want := HostName(":28081"), hostname+":28081"; got != want {
		t.Errorf("HostName = %q, want %q", got, want)
	}
}

// TestMiddleware: two processes on one machine answer with different names,
// and a response the handler refuses carries the name as well as one it
// serves.
func TestMiddleware(t *testing.T) {
	refuse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream refused", http.StatusBadGateway)
	})
	serve := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tests := []struct {
		name    string
		addr    string
		handler http.Handler
		status  int
	}{
		{"served on the first process", ":8080", serve, http.StatusOK},
		{"refused on the second process", ":8081", refuse, http.StatusBadGateway},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := Name("host", tt.addr)
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
			Middleware(name, tt.handler).ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
			if got := w.Header().Get(Header); got != name {
				t.Errorf("%s = %q, want %q", Header, got, name)
			}
			seen[w.Header().Get(Header)] = true
		})
	}
	if len(seen) != len(tests) {
		t.Errorf("two processes answered with %d distinct names: %v", len(seen), seen)
	}
}
