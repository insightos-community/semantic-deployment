//go:build linux || darwin

// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0
package filelock

import (
	"errors"
	"os"
	"syscall"
)

// TryLock acquires an exclusive lock without waiting for another owner.
func TryLock(file *os.File) error {
	if file == nil {
		return os.ErrInvalid
	}
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		err = ErrBusy
	}
	return lockError("lock", file, err)
}

// Unlock releases a lock; the caller still owns and must close the file.
func Unlock(file *os.File) error {
	if file == nil {
		return os.ErrInvalid
	}
	return lockError("unlock", file, syscall.Flock(int(file.Fd()), syscall.LOCK_UN))
}
