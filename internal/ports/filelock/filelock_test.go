// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0
package filelock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func openLock(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestContentionUnlockAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "实例 lock")
	first, second := openLock(t, path), openLock(t, path)
	if err := TryLock(first); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(second); !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock = %v, want ErrBusy", err)
	}
	if err := Unlock(first); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(second); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(first); err != nil {
		t.Fatalf("lock after owner closed: %v", err)
	}
	if err := Unlock(first); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidHandleIsNotContention(t *testing.T) {
	if err := TryLock(nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatal(err)
	}
	file := openLock(t, filepath.Join(t.TempDir(), "lock"))
	file.Close()
	if err := TryLock(file); err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("closed handle error = %v", err)
	}
}

func helperCommand(t *testing.T, path string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockHelper$")
	cmd.Env = append(os.Environ(), "SEMANTIC_FILELOCK_HELPER=1", "SEMANTIC_FILELOCK_PATH="+path)
	return cmd
}

func TestProcessContentionAndCrashRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "跨进程 lock")
	file := openLock(t, path)
	if err := TryLock(file); err != nil {
		t.Fatal(err)
	}
	contender := helperCommand(t, path)
	output, err := contender.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 || string(output) != "busy\n" {
		t.Fatalf("contender: %v, %q", err, output)
	}
	if err := Unlock(file); err != nil {
		t.Fatal(err)
	}
	owner := helperCommand(t, path)
	stdout, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := owner.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stderr bytes.Buffer
	owner.Stderr = &stderr
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			owner.Process.Kill()
			owner.Wait()
		}
	}()
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "locked\n" {
		t.Fatalf("owner: %q, %v, %s", ready, err, stderr.String())
	}
	if err := TryLock(file); !errors.Is(err, ErrBusy) {
		t.Fatalf("live owner: %v", err)
	}
	// Termination bypasses application cleanup. The OS must release the lock.
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Wait(); err == nil {
		t.Fatal("killed helper exited successfully")
	}
	waited = true
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := TryLock(file)
		if err == nil {
			break
		}
		// Windows documents that OS cleanup can lag process termination.
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("lock after owner crash: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := Unlock(file); err != nil {
		t.Fatal(err)
	}
}

func TestLockHelper(t *testing.T) {
	if os.Getenv("SEMANTIC_FILELOCK_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	file, err := os.OpenFile(os.Getenv("SEMANTIC_FILELOCK_PATH"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		os.Exit(21)
	}
	if err := TryLock(file); err != nil {
		if errors.Is(err, ErrBusy) {
			os.Stdout.WriteString("busy\n")
			os.Exit(17)
		}
		os.Exit(22)
	}
	os.Stdout.WriteString("locked\n")
	var input [1]byte
	os.Stdin.Read(input[:])
	// Deliberately no Unlock/Close/defer: exercise OS process-exit semantics.
	os.Exit(0)
}
