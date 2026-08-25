package trino

import "testing"

// AcceptsWrites is asked by the surface that offers a connection, and answered
// by the interceptor that will refuse the statement. The two have to agree, so
// what is asserted here is that it mirrors checkExecWritable -- including the
// two asymmetries that are easy to get backwards.

func TestAcceptsWrites_PerConnection(t *testing.T) {
	tk := &Toolkit{
		name:     "primary",
		readOnly: NewConnectionReadOnlyInterceptor("primary", map[string]bool{"primary": false, "locked": true}),
	}

	if !tk.AcceptsWrites("primary") {
		t.Error("a writable connection was reported as refusing writes")
	}
	if tk.AcceptsWrites("locked") {
		t.Error("a read-only connection was reported as accepting writes")
	}
	// An empty name is the default connection, as it is for ScratchTarget.
	if !tk.AcceptsWrites("") {
		t.Error("the empty name did not resolve to the default connection")
	}
	// A name the toolkit routes nowhere is not one a write can be run on.
	if tk.AcceptsWrites("nonesuch") {
		t.Error("an unconfigured connection was reported as accepting writes")
	}
}

// No interceptor at all is a single-connection toolkit that was not configured
// read-only: checkExecWritable returns nil for it, so writes are allowed. This
// is the asymmetry with the interceptor's own nil map, which refuses.
func TestAcceptsWrites_NoInterceptorAllows(t *testing.T) {
	tk := &Toolkit{name: "primary"}
	if !tk.AcceptsWrites("primary") {
		t.Error("a toolkit with no read-only interceptor refused writes")
	}
}

// A blanket interceptor holds no per-connection settings, which is how a
// single-connection toolkit configured read_only: true is built.
func TestAcceptsWrites_BlanketReadOnlyRefuses(t *testing.T) {
	tk := &Toolkit{name: "primary", readOnly: NewReadOnlyInterceptor()}
	if tk.AcceptsWrites("primary") {
		t.Error("a blanket read-only toolkit accepted writes")
	}
}
