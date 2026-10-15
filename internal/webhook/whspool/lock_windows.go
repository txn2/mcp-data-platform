//go:build windows

package whspool

import (
	"errors"
	"fmt"
	"os"
)

// lockDir takes the spool by creating its lock file exclusively. A process
// that died holding it leaves the file behind, which an operator removes;
// Windows is not a platform the receiver is deployed on.
func lockDir(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, fileMode) // #nosec G304 -- the spool's own lock file
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("another process holds this spool; give each replica its own directory")
		}
		return nil, fmt.Errorf("opening the lock: %w", err)
	}
	return f, nil
}

// unlockDir releases the lock.
func unlockDir(f *os.File) error {
	if f == nil {
		return nil
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("whspool: releasing the lock: %w", err)
	}
	return nil
}

// syncDirUnsupported reports a directory sync the file system does not
// offer: Windows syncs no directory.
func syncDirUnsupported(error) bool { return true }
