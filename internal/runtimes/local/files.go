// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"errors"
	"os"
	"path/filepath"
)

// writeFileMode writes body to path with the given mode, creating parents. It
// is the only place file modes are applied, so a caller cannot create a
// credential file world-readable by omitting the mode.
//
// Every path is under the session's own directory. One that names a file a
// specification carries, an attachment or an input, has first been contained
// by containedPath, which refuses a path that escapes its directory.
func writeFileMode(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, body, mode) //nolint:gosec // G703: under the session directory; a specification's path is contained first
}

// linkFile makes path a symbolic link to target, replacing whatever a previous
// stage left there. The link is what lets a harness read and refresh one file
// of the operator's login from a configuration directory the session owns.
func linkFile(path, target string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, path)
}
