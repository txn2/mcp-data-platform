//go:build !windows

package whspool

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockDir takes an exclusive lock on path, refusing a spool another process
// holds: two processes appending to one directory would each drain the
// other's files.
func lockDir(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode) // #nosec G304 -- the spool's own lock file
	if err != nil {
		return nil, fmt.Errorf("opening the lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- a file descriptor fits an int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another process holds this spool; give each replica its own directory")
		}
		return nil, fmt.Errorf("locking: %w", err)
	}
	return f, nil
}

// unlockDir releases the lock.
func unlockDir(f *os.File) error {
	if f == nil {
		return nil
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- as above
	if err := f.Close(); err != nil {
		return fmt.Errorf("whspool: releasing the lock: %w", err)
	}
	return nil
}

// syncDirUnsupported reports a directory sync the file system does not
// offer, which leaves nothing to do.
func syncDirUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)
}
