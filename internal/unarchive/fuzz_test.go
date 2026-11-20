package unarchive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"
)

// fuzzLimits are small enough that an input of a few kilobytes reaches each.
var fuzzLimits = Limits{MaxMemberBytes: 1 << 16, MaxTotalBytes: 1 << 17, MaxMembers: 64, MaxRatio: 50}

// fuzzSource serves b through the range function, refusing a read outside it.
func fuzzSource(b []byte) Source {
	return Source{Name: "upload.zip", Size: int64(len(b)), Fetch: func(_ context.Context, off, length int64) ([]byte, error) {
		if off < 0 || length < 0 || off+length > int64(len(b)) {
			return nil, errors.New("read outside the archive")
		}
		return b[off : off+length], nil
	}}
}

// FuzzOpenAndExtract holds the archive reader to whatever an uploader sends
// (#2713): no member's path leaves the destination, and no member, total or
// member count passes its limit, before or while the bytes are read.
func FuzzOpenAndExtract(f *testing.F) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range []string{"data/a.csv", "../escape.txt", "/abs.txt", "ok.txt"} {
		fw, _ := w.Create(name)
		_, _ = fw.Write([]byte(strings.Repeat("a,b\n", 8)))
	}
	_ = w.Close()
	f.Add(buf.Bytes())
	f.Add([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 255})
	f.Add([]byte("not an archive"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<16 {
			return
		}
		a, err := Open(context.Background(), fuzzSource(b), "", fuzzLimits)
		if err != nil {
			return
		}
		if len(a.Members()) > fuzzLimits.MaxMembers {
			t.Fatalf("%d members passed a limit of %d", len(a.Members()), fuzzLimits.MaxMembers)
		}
		var total int64
		err = a.Extract(context.Background(), func(m Member, r io.Reader) error {
			p := m.Path()
			if path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
				t.Fatalf("member %q resolves to %q, outside the destination", m.Name, p)
			}
			n, err := io.Copy(io.Discard, r)
			total += n
			if n > fuzzLimits.MaxMemberBytes {
				t.Fatalf("member %q yielded %d bytes, over the member limit", m.Name, n)
			}
			if err != nil {
				return fmt.Errorf("reading member %q: %w", m.Name, err)
			}
			return nil
		})
		if err == nil && total > fuzzLimits.MaxTotalBytes {
			t.Fatalf("an extract that succeeded yielded %d bytes, over the total limit", total)
		}
	})
}
