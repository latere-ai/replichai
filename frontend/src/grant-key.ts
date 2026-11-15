// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A credit grant must land once however many times the request is sent. The
// admin's confirmation makes one key for the grant they mean to make; sending the
// same request again, after a dropped connection or a timeout, carries the same
// key so the server credits once. A grant that went through frees its key, so the
// next grant of the same amount to the same person is a new grant.

// grantKeys hands out one key per pending grant, identified by what it grants.
export function grantKeys(newKey: () => string = () => crypto.randomUUID()) {
  const pending = new Map<string, string>()
  const sig = (email: string, amount: number) => `${email.toLowerCase()}|${amount}`
  return {
    // keyFor is the key of this grant, made on first use and reused after.
    keyFor(email: string, amount: number): string {
      const s = sig(email, amount)
      let k = pending.get(s)
      if (!k) {
        k = newKey()
        pending.set(s, k)
      }
      return k
    },
    // settled frees the key once the server has answered for the grant.
    settled(email: string, amount: number): void {
      pending.delete(sig(email, amount))
    },
  }
}
