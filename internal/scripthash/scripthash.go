// Package scripthash is the hashing a managed script is given, as a Starlark
// module (#2049).
//
// It is separate from the engine for the reason internal/scriptdate is: every
// function is a pure transformation of its arguments, there is no run, no
// caller and no state, and the engine's only use of it is to predeclare the
// module. A digest is deterministic, so a recorded run replays with nothing
// recorded for it.
package scripthash

import (
	"crypto/hmac"
	"crypto/md5"  // #nosec G501 -- a content fingerprint a script asks for by name, not a credential hash
	"crypto/sha1" // #nosec G505 -- a content fingerprint and webhook HMAC a script asks for by name
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/fnv"
	"sort"

	"go.starlark.net/starlark"
)

// Name is the global the module is bound to.
const Name = "hash"

// Module is the hash module the engine predeclares.
//
// Every digest is computed in Go, so hashing a value costs one step whatever
// its length: an MD5 written in Starlark spends a step per operation and a
// megabyte page alone exhausts a run's step budget.
//
// The module is also callable. Starlark's universe has hash(x), and the
// module takes its name, so hash("abc") keeps returning what the universe's
// hash returned before the module existed.
var Module = &module{members: starlark.StringDict{
	"md5":         digestBuiltin("md5", md5.New),
	"sha1":        digestBuiltin("sha1", sha1.New),
	"sha256":      digestBuiltin("sha256", sha256.New),
	"sha512":      digestBuiltin("sha512", sha512.New),
	"hmac_sha1":   hmacBuiltin("hmac_sha1", sha1.New),
	"hmac_sha256": hmacBuiltin("hmac_sha256", sha256.New),
	"hmac_sha512": hmacBuiltin("hmac_sha512", sha512.New),
	"crc32":       starlark.NewBuiltin(Name+".crc32", crc32Fn),
	"fnv64":       starlark.NewBuiltin(Name+".fnv64", fnv64Fn),
}}

// universeHash is Starlark's own hash(x), which calling the module runs.
var universeHash = starlark.Universe["hash"]

// module is a Starlark module that can also be called. It is not a
// starlarkstruct.Module because that type is not callable.
type module struct {
	members starlark.StringDict
}

// String, Type, Freeze, Truth and Hash make the module a Starlark value that
// reads as a module.
func (*module) String() string { return "<module " + Name + ">" }

// Type is a module's.
func (*module) Type() string { return "module" }

// Freeze has nothing mutable to freeze.
func (*module) Freeze() {}

// Truth is a module's.
func (*module) Truth() starlark.Bool { return starlark.True }

// Hash refuses, as a module's does.
func (*module) Hash() (uint32, error) { return 0, errUnhashable }

// errUnhashable is a module's answer to hash(), as Starlark's own modules give.
var errUnhashable = errors.New("unhashable: module")

// Name is the name a call of the module reports in its errors.
func (*module) Name() string { return Name }

// Attr returns a member, or nil for a name the module does not have, which
// Starlark reports as "module has no .x field or method".
func (m *module) Attr(name string) (starlark.Value, error) { return m.members[name], nil }

// AttrNames lists the members, sorted as dir() expects.
func (m *module) AttrNames() []string {
	names := make([]string, 0, len(m.members))
	for n := range m.members {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Members is the module's functions, for the contract's checks.
func (m *module) Members() starlark.StringDict { return m.members }

// CallInternal is hash(x) as the universe defines it.
func (*module) CallInternal(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	v, err := starlark.Call(thread, universeHash, args, kwargs)
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	return v, nil
}

// content reads the value a digest is taken over: a string's bytes, or a
// bytes value. Anything else is refused rather than stringified, since the
// digest of a list's repr is never what an author wanted.
func content(b *starlark.Builtin, v starlark.Value, arg string) ([]byte, error) {
	switch v := v.(type) {
	case starlark.String:
		return []byte(v), nil
	case starlark.Bytes:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("in %s: %s must be a string or bytes, not %s", b.Name(), arg, v.Type())
	}
}

// argS is the name of the value every function hashes.
const argS = "s"

// oneArg reads the one `s` argument a function hashes.
func oneArg(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) ([]byte, error) {
	var s starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, argS, &s); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	return content(b, s, argS)
}

// digestBuiltin is hash.<name>(s): the lowercase hex digest of s.
func digestBuiltin(name string, newHash func() hash.Hash) *starlark.Builtin {
	return starlark.NewBuiltin(Name+"."+name, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		data, err := oneArg(b, args, kwargs)
		if err != nil {
			return nil, err
		}
		h := newHash()
		_, _ = h.Write(data)
		return starlark.String(hex.EncodeToString(h.Sum(nil))), nil
	})
}

// hmacBuiltin is hash.hmac_<name>(key, s): the lowercase hex HMAC of s under
// key, for verifying a webhook signature or signing an outbound payload.
func hmacBuiltin(name string, newHash func() hash.Hash) *starlark.Builtin {
	return starlark.NewBuiltin(Name+"."+name, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var key, s starlark.Value
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "key", &key, argS, &s); err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		k, err := content(b, key, "key")
		if err != nil {
			return nil, err
		}
		data, err := content(b, s, argS)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(newHash, k)
		_, _ = mac.Write(data)
		return starlark.String(hex.EncodeToString(mac.Sum(nil))), nil
	})
}

// crc32Fn is hash.crc32(s): the IEEE CRC-32 of s as a non-negative int, for
// cheap bucketing.
func crc32Fn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	data, err := oneArg(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	return starlark.MakeUint64(uint64(crc32.ChecksumIEEE(data))), nil
}

// fnv64Fn is hash.fnv64(s): the FNV-1a 64-bit hash of s as a non-negative
// int, for cheap bucketing.
func fnv64Fn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	data, err := oneArg(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return starlark.MakeUint64(h.Sum64()), nil
}
