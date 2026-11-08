//go:build windows

package flock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// winLockfileSharedLock mirrors the LOCKFILE_EXCLUSIVE_LOCK flag's absence.
// See https://msdn.microsoft.com/en-us/library/windows/desktop/aa365203(v=vs.85).aspx
const winLockfileSharedLock = 0x00000000

// ErrorLockViolation is the Windows error code for a lock that would block.
const ErrorLockViolation windows.Errno = 0x21 // 33

func openLockFile(path string, cfg config) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, cfg.perm)
}

func openEphemeralCreate(path string, cfg config) (*os.File, bool, error) {
	handle, err := openWindowsFile(path, windows.CREATE_NEW)
	if err == nil {
		return os.NewFile(uintptr(handle), path), true, nil
	}
	if !errors.Is(err, windows.ERROR_FILE_EXISTS) {
		if isWindowsVanished(err) {
			return nil, false, errLockFileVanished
		}
		return nil, false, err
	}
	handle, err = openWindowsFile(path, windows.OPEN_EXISTING)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, false, os.ErrNotExist
		}
		if isWindowsVanished(err) {
			return nil, false, errLockFileVanished
		}
		return nil, false, err
	}
	return os.NewFile(uintptr(handle), path), false, nil
}

// openWindowsFile opens or creates a file. The handle allows delete
// sharing. DeleteFile requires every open handle to allow delete sharing.
func openWindowsFile(path string, disposition uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		disposition,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
}

// isWindowsVanished reports whether the error means that the file is
// delete-pending. The file disappears when the last handle closes.
func isWindowsVanished(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

func platformFlock(fh *os.File, flag flockFlag) error {
	lockFlag := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if flag == flockShared {
		lockFlag = winLockfileSharedLock
	}
	err := windows.LockFileEx(windows.Handle(fh.Fd()), lockFlag, 0, 1, 0, &windows.Overlapped{})
	if err != nil && !errors.Is(err, windows.Errno(0)) {
		return err
	}
	return nil
}

func platformFlockTry(fh *os.File, flag flockFlag) error {
	lockFlag := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if flag == flockShared {
		lockFlag = winLockfileSharedLock
	}
	err := windows.LockFileEx(
		windows.Handle(fh.Fd()),
		lockFlag|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{},
	)
	if err != nil && !errors.Is(err, windows.Errno(0)) {
		if errors.Is(err, ErrorLockViolation) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return ErrLockHeld
		}
		return err
	}
	return nil
}

func platformUnlock(fh *os.File) error {
	err := windows.UnlockFileEx(windows.Handle(fh.Fd()), 0, 1, 0, &windows.Overlapped{})
	if err != nil && !errors.Is(err, windows.Errno(0)) {
		return err
	}
	return nil
}

// sameFileAtPath reports whether the path points at the same file as fh.
// The function compares the volume serial number and the file index. It
// returns false when the path does not exist.
func sameFileAtPath(path string, fh *os.File) (bool, error) {
	handle, err := openWindowsFile(path, windows.OPEN_EXISTING)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return false, nil
		}
		if isWindowsVanished(err) {
			// The file is delete-pending. The path no longer resolves to
			// a file that an acquirer can open.
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(handle)

	var pathInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &pathInfo); err != nil {
		return false, err
	}
	var fileInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(fh.Fd()), &fileInfo); err != nil {
		return false, err
	}
	return pathInfo.VolumeSerialNumber == fileInfo.VolumeSerialNumber &&
		pathInfo.FileIndexHigh == fileInfo.FileIndexHigh &&
		pathInfo.FileIndexLow == fileInfo.FileIndexLow, nil
}
