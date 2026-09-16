//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package main

import (
	"errors"
	"os"
	"time"
)

var ErrLockTimeout = errors.New("capture file lock timed out")

// The in-process mutex still serializes requests on these platforms. Production
// deployment is Linux, where filelock_unix.go also coordinates multiple processes.
func acquireFileLock(path string, _ time.Duration) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return file.Close, nil
}
