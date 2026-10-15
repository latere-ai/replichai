// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { installElastic } from './elastic'
import { TipHost } from './ui'
// The ink ladder first, scoped to `data-design="ink"` on <html>, then the
// app's own sheet, which adds only what the ladder does not name (047).
import 'latere-ui/ink'
import './styles.css'

// TipHost is mounted beside the app, not inside it: it draws every tooltip from
// one bubble portalled to <body>, and every screen, including the ones App
// returns before the shell, has controls that carry one.
// One wheel listener for the whole app: a pull past the end of a pane stretches
// that pane and nothing around it (047).
installElastic()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    <TipHost />
  </StrictMode>,
)
