# 022f — The frozen account, in the UI

**Status:** validated — ready to build. Leaf of [022](022-money-comes-in-without-an-admin.md). Frontend (`frontend/src`).

**Scope:** the freeze made visible, on both sides. For the frozen person: a global banner and disabled spend/create controls, whose only way out is the top-up modal. For the operator: the admin console's per-account state (normal/frozen/banned), a filter to the accounts that owe, an unfreeze button, and the sold-liability figure.

**Depends on:** [022d](022d-a-negative-wallet-is-frozen.md) (`GET /me` carries `frozen`; the unfreeze route and the `Frozen` flag + negative filter on the admin list), [022e](022e-buying-credits.md) (the `TopUpModal` the banner reuses), [022c](022c-refunds-and-the-solvency-read.md) (the `GET /admin/solvency` number).

## Goal

Make the server-side freeze legible: the frozen user sees why and the one way out, and the operator sees who owes and can act.

## What to do

1. **Me.frozen** — add `frozen bool` to the `Me` type (`api.ts`); it comes from `GET /me` (022d).
2. **Banner** — a global `banner warn` (frozen-banner) in `Shell` above `.main` when `me.frozen`, explaining the freeze and carrying a **Top up** button that opens 022e's `TopUpModal`.
3. **Disable spend/create** when `me.frozen`: the "New project" button (`App.tsx`), `PapersCard` start/add, `ProjectCreditsCard` allocate — disabled with a tooltip; the only enabled money action is Top up.
4. **Console — the people table**: show each row's state (normal/frozen/banned) from `GrantView.Frozen`, a filter toggle to the negative-wallet accounts (`?filter=negative`), and an **Unfreeze** button calling `POST /admin/access/{email}/unfreeze` (and its re-freeze inverse).
5. **Console — solvency**: render the `GET /admin/solvency` figure (formatted micro-USD) beside the rate card.
6. **i18n** — new `en` keys (frozen banner, unfreeze, filter label, solvency label, and the `account_frozen` branch in `startErrLabel`).

## Tests

- vitest/RTL: the banner shows when `me.frozen` and hides otherwise; spend/create controls are disabled while frozen; the banner's Top up opens the modal.
- Console: rows render the three states; the negative filter narrows the list; Unfreeze calls the route; the solvency figure renders from a stubbed value.
- `startErrLabel` maps `account_frozen` to its copy.

## Boundaries

- No backend — the `frozen` field, the unfreeze route, the freeze list, and the solvency read are 022c/022d.
- Reuses 022e's `TopUpModal`; does not define its own.
- Do not add a `zh` locale; add `en` keys only.
