//go:build windows

package spool

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

const minFreeBytes int64 = 16 << 20

func lockFile(f *os.File) error {
	var ov windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
}
func unlockFile(f *os.File) error {
	var ov windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov)
}
func checkFreeSpace(dir string, recordBytes int64) error {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, nil, nil); err != nil {
		return err
	}
	if available < uint64(recordBytes+minFreeBytes) {
		return ErrDiskSpace
	}
	return nil
}
func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) ||
		errors.Is(err, syscall.Errno(112)) ||
		errors.Is(err, syscall.Errno(39))
}
