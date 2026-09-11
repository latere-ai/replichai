// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"os"
	"path/filepath"
)

// writeFileMode writes body to path with the given mode, creating parents. It
// is the one place file modes are applied, so a credential file cannot be
// created world-readable by a caller that forgot.
func writeFileMode(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, body, mode)
}
