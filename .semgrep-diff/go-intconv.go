//go:build ignore

// Fixture for go-intconv.yml (#2425), held to it by make semgrep-diff. It
// holds the refused shape on purpose, so the build constraint keeps every Go
// tool (gosec included) from loading it; Semgrep reads it as source.

package fixture

import "errors"

const maxConns = 100

type config struct{ MaxConns int }

type poolConfig struct{ MaxConns int32 }

// validate is where #2424 bounded the value, out of the converting function's
// sight.
func validate(c config) error {
	if c.MaxConns < 1 || c.MaxConns > maxConns {
		return errors.New("max_conns out of range")
	}
	return nil
}

// The #2424 shape: the bound is in validate, not here.
func poolOf2424(c config) poolConfig {
	pc := poolConfig{}
	// ruleid: narrowing-int-conversion-unbounded
	pc.MaxConns = int32(c.MaxConns)
	return pc
}

// The fix #2424 shipped: the bound beside the conversion.
func poolFixed(c config) (poolConfig, error) {
	if c.MaxConns < 1 || c.MaxConns > maxConns {
		return poolConfig{}, errors.New("max_conns out of range")
	}
	pc := poolConfig{}
	// ok: narrowing-int-conversion-unbounded
	pc.MaxConns = int32(c.MaxConns)
	return pc, nil
}

// A clamp in the conversion.
func clamped(n int) uint16 {
	// ok: narrowing-int-conversion-unbounded
	return uint16(min(n, 65535))
}

// A clamp assigned back.
func clampedBack(n int) uint8 {
	n = max(n, 0)
	n = min(n, 255)
	// ok: narrowing-int-conversion-unbounded
	return uint8(n)
}

// Inside the guarded branch.
func guarded(n int) int8 {
	if n <= 127 && n >= -128 {
		// ok: narrowing-int-conversion-unbounded
		return int8(n)
	}
	return 0
}

// A literal.
func literal() int16 {
	// ok: narrowing-int-conversion-unbounded
	return int16(42)
}

// Each narrowing target, unbounded.
func unbounded(n int, m int64) (int16, uint32) {
	// ruleid: narrowing-int-conversion-unbounded
	a := int16(n)
	// ruleid: narrowing-int-conversion-unbounded
	b := uint32(m)
	return a, b
}

// A widening conversion is not this rule's.
func widening(n int32) int64 {
	// ok: narrowing-int-conversion-unbounded
	return int64(n)
}
