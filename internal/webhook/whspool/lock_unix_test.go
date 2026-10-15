//go:build !windows

package whspool

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLockDirReportsWhatItCannotOpen(t *testing.T) {
	if _, err := lockDir(filepath.Join(t.TempDir(), "missing", lockName)); err == nil {
		t.Fatal("a lock in a directory that does not exist was taken")
	}
	if err := unlockDir(nil); err != nil {
		t.Fatalf("releasing no lock: %v", err)
	}
}

func TestSyncDirUnsupported(t *testing.T) {
	if !syncDirUnsupported(syscall.EINVAL) || !syncDirUnsupported(syscall.ENOTSUP) {
		t.Error("a file system that offers no directory sync is reported as a failure")
	}
	if syncDirUnsupported(errors.New("disk on fire")) {
		t.Error("a real sync failure is passed over")
	}
}

// A source directory that cannot be read stops the spool from opening, rather
// than opening without the files it holds.
func TestOpenFailsOnASourceItCannotRead(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	if err := os.MkdirAll(src, dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, dirMode) })
	if _, err := Open(Options{Dir: dir, Replica: "r"}); err == nil {
		t.Fatal("a spool opened over a source directory it cannot read")
	}
	if _, err := Open(Options{Dir: filepath.Join(dir, "a", "nested"), Replica: "r"}); err == nil {
		t.Fatal("a spool opened in a directory it cannot create")
	}
}

// A file the spool cannot create in a source directory it cannot write to
// fails the append.
func TestAppendFailsWhereItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{Dir: dir, Replica: "r"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test
	src := filepath.Join(dir, "a")
	if err := os.MkdirAll(src, dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o500); err != nil { // #nosec G302 -- a directory made read-only for the test
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, dirMode) })
	if _, err := s.Append("a", Record{Hash: "h"}); err == nil {
		t.Fatal("an append into a directory it cannot write succeeded")
	}
}
