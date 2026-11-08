//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package flock

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// platformFlock implements the per-platform flock semantics. The bool
// argument controls blocking vs non-blocking.
func toUnixFlag(ff flockFlag) int {
	switch ff {
	case flockExclusive:
		return unix.LOCK_EX
	case flockShared:
		return unix.LOCK_SH
	}
	return unix.LOCK_EX
}

// openLockFile opens or creates the lock file. On AIX and non-illumos
// Solaris, the kernel rejects exclusive locking on a non-writable descriptor.
// Those platforms use fcntl F_SETLK through flock(3), so the
// flock_unix_fcntl.go variant overrides this with O_RDWR. Linux, Darwin, the
// BSDs, and Illumos all flock(2) an RDONLY file happily.
func openLockFile(path string, cfg config) (*os.File, error) {
	flag := os.O_CREATE | os.O_RDONLY
	return os.OpenFile(path, flag, cfg.perm)
}

// openEphemeralCreate performs the O_CREAT|O_EXCL create attempt. On
// success it returns the FD and created=true. On EEXIST it opens the
// existing file with O_RDONLY and returns created=false, leaving the
// flock dance to the caller.
func openEphemeralCreate(path string, cfg config) (fh *os.File, created bool, err error) {
	fh, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, cfg.perm)
	if err == nil {
		return fh, true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, false, err
	}
	fh, err = os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Vanished between EEXIST and our open; the caller will retry.
			return nil, false, err
		}
		return nil, false, err
	}
	return fh, false, nil
}

func platformFlock(fh *os.File, flag flockFlag) error {
	return unix.Flock(int(fh.Fd()), toUnixFlag(flag))
}

func platformFlockTry(fh *os.File, flag flockFlag) error {
	err := unix.Flock(int(fh.Fd()), toUnixFlag(flag)|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrLockHeld
	}
	return err
}

func platformUnlock(fh *os.File) error {
	return unix.Flock(int(fh.Fd()), unix.LOCK_UN)
}

// sameFileAtPath reports whether the path points at the same file as fh.
// The function compares the device and the inode number. It returns false
// when the path does not exist.
func sameFileAtPath(path string, fh *os.File) (bool, error) {
	pathInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	fileInfo, err := fh.Stat()
	if err != nil {
		return false, err
	}
	pathStat, ok := pathInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false, nil
	}
	fileStat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false, nil
	}
	return pathStat.Dev == fileStat.Dev && pathStat.Ino == fileStat.Ino, nil
}
