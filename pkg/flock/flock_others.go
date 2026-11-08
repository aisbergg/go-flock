//go:build !unix && !windows

package flock

import (
	"errors"
	"io/fs"
	"os"
)

// All lock calls return fs.ErrUnsupported on platforms without a flock
// implementation. These stubs exist so the package still links on
// wasm/js/wasip1/plan9. Callers must guard the call with errors.Is.
var errUnsupported = &fs.PathError{
	Op:   "flock",
	Path: "",
	Err:  errors.ErrUnsupported,
}

func openLockFile(path string, cfg config) (*os.File, error) {
	return nil, errUnsupported
}

func openEphemeralCreate(path string, cfg config) (*os.File, bool, error) {
	return nil, false, errUnsupported
}

func platformFlock(fh *os.File, flag flockFlag) error {
	return errUnsupported
}

func platformFlockTry(fh *os.File, flag flockFlag) error {
	return errUnsupported
}

func platformUnlock(fh *os.File) error {
	return errUnsupported
}

func sameFileAtPath(path string, fh *os.File) (bool, error) {
	return false, errUnsupported
}
