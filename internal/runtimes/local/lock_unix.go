// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package local

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on the file at path, creating it
// when absent, and returns the function that releases it. The lock is held by
// the open file, so it is released if the process dies.
func lockFile(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // a fixed name under this session's own directory
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	// Closing the file releases the lock.
	return f.Close, nil
}
