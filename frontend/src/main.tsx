// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { TipHost } from './ui'
import './styles.css'

// TipHost is mounted beside the app, not inside it: it draws every tooltip from
// one bubble portalled to <body>, and every screen, including the ones App
// returns before the shell, has controls that carry one.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    <TipHost />
  </StrictMode>,
)
