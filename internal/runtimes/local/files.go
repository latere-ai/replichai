// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import "os"

// writeFile writes body to path with the given mode, creating parents. It is
// the one place file modes are applied, so a credential file cannot be created
// world-readable by a caller that forgot.
func writeFileMode(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, body, mode)
}

// writeFile writes an ordinary file.
func writeFile(path string, body []byte) error { return writeFileMode(path, body, 0o600) }

func dir(path string) string {
	for index := len(path) - 1; index >= 0; index-- {
		if path[index] == '/' {
			if index == 0 {
				return "/"
			}
			return path[:index]
		}
	}
	return "."
}
