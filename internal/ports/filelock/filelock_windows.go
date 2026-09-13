// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0
package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// TryLock locks byte zero, including for an empty file. Every instance uses
// the same byte range. FAIL_IMMEDIATELY keeps startup nonblocking and permits
// LockFileEx to use a stack-owned OVERLAPPED without pending asynchronous I/O.
func TryLock(file *os.File) error {
	if file == nil {
		return os.ErrInvalid
	}
	var position windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &position)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		err = ErrBusy
	}
	return lockError("lock", file, err)
}

// Unlock releases the same byte range without closing the caller's handle.
func Unlock(file *os.File) error {
	if file == nil {
		return os.ErrInvalid
	}
	var position windows.Overlapped
	return lockError("unlock", file, windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &position))
}
