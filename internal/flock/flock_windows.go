package flock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockLength = 1

func lockExclusive(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, lockLength, 0, &windows.Overlapped{})
}

func lockExclusiveRequired(f *os.File) error { return lockExclusive(f) }

func tryLockExclusive(f *os.File) (bool, error) {
	return tryLock(f, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
}

func tryLockShared(f *os.File) (bool, error) {
	return tryLock(f, windows.LOCKFILE_FAIL_IMMEDIATELY)
}

func tryLock(f *os.File, flags uint32) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, lockLength, 0, &windows.Overlapped{})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockLength, 0, &windows.Overlapped{})
}
