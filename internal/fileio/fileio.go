// Package fileio writes the small files a session workdir accumulates.
// Session timers read those files on their own schedule while commands
// write them, so a reader never sees a half-written file. That is atomicity
// against concurrent readers, not crash durability: nothing here fsyncs, so
// a write can be lost whole if the machine dies.
package fileio

import (
	"io/fs"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path through a temporary file in the same
// directory and renames it into place, so a concurrent reader sees either
// the previous file or this one, never a truncated write.
func WriteAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; the caller's mode has to be applied
	// before the rename, or a reader can find it unreadable.
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
