//go:build aix || (solaris && !illumos)

package flock

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openLockFile(path string, cfg config) (*os.File, error) {
	// AIX and (non-illumos) Solaris cannot hold an exclusive flock on a
	// read-only descriptor, so open the file RDWR.
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, cfg.perm)
}

func openEphemeralCreate(path string, cfg config) (*os.File, bool, error) {
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, cfg.perm)
	if err == nil {
		return fh, true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, false, err
	}
	fh, err = os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, false, err
	}
	return fh, false, nil
}

func platformFlock(fh *os.File, flag flockFlag) error {
	lt := unix.F_WRLCK
	if flag == flockShared {
		lt = unix.F_RDLCK
	}
	// Spurious EDEADLK can be returned on AIX/Solaris when the kernel
	// computes deadlock graphs at process, not thread, granularity. Retry
	// briefly; if it persists, return the error to the caller.
	return setlkw(fh.Fd(), unix.F_SETLKW, lt)
}

func platformFlockTry(fh *os.File, flag flockFlag) error {
	lt := unix.F_WRLCK
	if flag == flockShared {
		lt = unix.F_RDLCK
	}
	err := setlkw(fh.Fd(), unix.F_SETLK, lt)
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EAGAIN) {
		return ErrLockHeld
	}
	return err
}

func platformUnlock(fh *os.File) error {
	return setlkw(fh.Fd(), unix.F_SETLKW, unix.F_UNLCK)
}

// setlkw issues a POSIX fcntl lock call and returns the error to the
// caller for inspection.
func setlkw(fd uintptr, cmd, lt int) error {
	return unix.FcntlFlock(fd, cmd, &unix.Flock_t{
		Type:   int16(lt),
		Whence: 0,
		Start:  0,
		Len:    0,
	})
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
