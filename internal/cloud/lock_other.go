// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package cloud

// lockFile is a no-op where the platform has no flock: the in-process lock
// still serializes one process's refreshes.
func lockFile(string) (func() error, error) {
	return func() error { return nil }, nil
}
