# 024a — Settings as a destination, with sub-tabs

**Superseded in part by [022e](022e-buying-credits.md).** Settings is a destination with sub-tabs, as below — but two, not three. Credits moved to a dedicated `/wallet` screen, so `SettingsTab` is `'account' | 'profile'` and there is no `/settings/credits`.

**Status:** built. A child of [024](024-settings-is-a-place.md).

**Scope:** give personal settings a home in the rail and a shape that scales — a `Settings` nav item below Trash, and the one screen split into three sub-tabs addressed as `/settings/account`, `/settings/profile`, `/settings/credits`. Pure frontend. No API or schema change; the credits tab renders the existing wallet statement as-is, so [024b](024b-the-ledger-names-its-project.md) can change only what a row shows.

**Depends on:** [013](013-the-address-bar-is-the-state.md) (the routing this extends — a screen is a URL, and a sub-tab is a segment of it, exactly as a project tab is).

## Problem

Settings has no destination: it opens only from the person's card at the foot of the rail. And it is one scroll stacking four unrelated things — identity and access, the wallet, appearance, and the researcher profile a sign-off is published under. Each wants its own place; the profile in particular is a form, not a line in an account summary.

## Decisions

**A `Settings` item joins the rail's foot group, below Trash.** It sits beside Trash in `rail-group-foot` — both are personal chrome rather than workspace destinations. The person's card at the foot still opens settings; the nav item is the discoverable, foldable-icon way in, and stays clickable when the rail is folded like every other destination. The tab is `on` for `view.name === 'settings'` regardless of sub-tab.

**Settings is one view with a tab, addressed at three paths.** `View` gains `{ name: 'settings'; tab?: SettingsTab }` where `SettingsTab = 'account' | 'profile' | 'credits'`. `pathOf` gets its own `case`, mirroring `project`: the default tab (`account`) is omitted, so the bare `/settings` is canonical and `/settings/profile` and `/settings/credits` name the other two. `viewOf` parses `/settings/(account|profile|credits)`; an unknown trailing segment resolves to the bare settings view and the address is corrected, the same rule a project's unknown tab already follows. `SCREENS` keeps only `/settings` so the reverse-lookup stays unambiguous.

**The three tabs divide the page by what each is.**
- **Account** — the identity header, the access tier, appearance (theme), and sign out. What you *are* on the platform.
- **Profile** — the researcher record (`ProfileCard`) a published sign-off is made under. What you publish *as*.
- **Credits** — the wallet balance and its statement. What you have to *spend*. The statement renders exactly as today; 024b changes it.

**The tab strip is the app's existing one.** The `detail-tabs`/`tab` control the project detail already uses, so settings does not invent a second tab language. Switching a tab navigates (pushes history), so Back returns to the previous tab and a link opens on the tab it names.

**The topbar crumb names the tab.** The crumb map (`App.tsx`) already turns a view into a label; a settings view resolves to `Settings` and, past the root, the tab's own word, so the trail reads `Settings › Credits`.

## Acceptance criteria

- A `Settings` item renders in the rail below Trash, active on any settings sub-tab, and reachable (as an icon) when the rail is folded.
- `/settings`, `/settings/account`, `/settings/profile`, `/settings/credits` each resolve to the right tab; `/settings/account` canonicalises to `/settings`; `/settings/nonsense` lands on the bare view with the address corrected. Held by `routes.test.ts`, extended with these paths and the no-collision rule.
- Switching tabs updates the address and pushes history; Back returns to the prior tab.
- The Account tab shows identity, access, appearance, and sign out; Profile shows the researcher form; Credits shows the wallet balance and statement unchanged.
- `navSettings` and the three tab labels exist in **both** locales (en and zh).
- `frontend/test/sidebar.test.mjs` and `routing.test.mjs` pass with the new item and paths.

## Leaves out

- Any change to what a ledger row shows — that is [024b](024b-the-ledger-names-its-project.md). The statement is moved into the Credits tab intact.
- Removing the foot-of-rail person card; it stays as a second way in.
