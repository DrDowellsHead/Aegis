//go:build darwin

package spool

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const minFreeBytes int64 = 16 << 20

func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

func checkFreeSpace(dir string, recordBytes int64) error {
	var fs unix.Statfs_t
	if err := unix.Statfs(dir, &fs); err != nil { return err }
	if uint64(fs.Bavail)*uint64(fs.Bsize) < uint64(recordBytes+minFreeBytes) { return ErrDiskSpace }
	return nil
}

func isDiskFull(err error) bool {
	return errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT)
}
