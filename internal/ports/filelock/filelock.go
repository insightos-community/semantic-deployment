// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0

// Package filelock provides the nonblocking, process-owned instance lock used
// by both the supervisor and the debug stack. The caller owns the file handle:
// it must stay open while locked, and closing it (including process exit)
// releases the lock. Never unlink a live lock file: its path is shared by
// competing processes. Lock operations must not race with closing the handle.
package filelock

import (
	"errors"
	"os"
)

// ErrBusy means another handle or process already owns the instance lock.
var ErrBusy = errors.New("instance lock is already held")

func lockError(operation string, file *os.File, err error) error {
	if err == nil {
		return nil
	}
	return &os.PathError{Op: operation, Path: file.Name(), Err: err}
}
