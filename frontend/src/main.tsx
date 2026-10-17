// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
// latere-ui's material layer first: its controls (select, popover, dialog,
// segmented) draw their surfaces with its `.lu-glass-*` classes, and their own
// sheets load with the components App imports below.
import 'latere-ui/glass'
import App from './App'
import { TipHost } from './ui'
// The ink ladder, scoped to `data-design="ink"` on <html>, then the app's own
// sheet, which adds only what the ladder does not name and sets latere-ui's
// controls in the app's sizes.
import 'latere-ui/ink'
import './styles.css'

// TipHost is mounted beside the app, not inside it: it draws every tooltip from
// one bubble in the top layer, and every screen, including the ones App returns
// before the shell, has controls that carry one.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    <TipHost />
  </StrictMode>,
)
