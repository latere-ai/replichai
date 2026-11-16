/-!
How a refund takes credit back, written by hand against `RefundReversal` in
internal/credit. A purchase credited `credited` for a charge of `charged`.
Each refund event reports the cumulative amount refunded so far, and its
reversal takes back the purchase's share of that, less what earlier reversals
of the same purchase already took. A Go test checks `RefundReversal` against
`reversal` below.
-/

namespace Proofs.Refund

/-- What a purchase owes back once `refunded` of its `charged` is refunded. -/
def owed (credited charged refunded : Nat) : Nat :=
  min credited (credited * refunded / charged)

/-- The reversal one refund event posts, given what earlier ones took. -/
def reversal (credited charged refunded already : Nat) : Nat :=
  owed credited charged refunded - already

/-- What a dispute takes: whatever the refunds left. -/
def disputed (credited already : Nat) : Nat := credited - already

/-- The total taken back after the refund events `rs`, in arrival order. -/
def total (credited charged : Nat) (rs : List Nat) : Nat :=
  rs.foldl (fun a r => a + reversal credited charged r a) 0

theorem owed_le (c ch r : Nat) : owed c ch r ≤ c := Nat.min_le_left _ _

/-- One reversal never takes the total past what the purchase credited. -/
theorem reversal_bounded (c ch r a : Nat) (h : a ≤ c) :
    a + reversal c ch r a ≤ c := by
  have := owed_le c ch r
  unfold reversal; omega

/-- A full refund takes back exactly what the purchase credited. -/
theorem full_refund_exact (c ch a : Nat) (hpos : 0 < ch) (h : a ≤ c) :
    a + reversal c ch ch a = c := by
  unfold reversal owed
  rw [Nat.mul_div_cancel c hpos, Nat.min_self]
  omega

/-- A dispute after any refunds takes the total to exactly what was credited. -/
theorem dispute_exact (c a : Nat) (h : a ≤ c) : a + disputed c a = c := by
  unfold disputed; omega

/-- Posting a reversal is taking the larger of what was taken and what is owed. -/
theorem step_is_max (c ch r a : Nat) :
    a + reversal c ch r a = max a (owed c ch r) := by
  unfold reversal; omega

theorem foldl_step (c ch : Nat) (rs : List Nat) (a : Nat) :
    rs.foldl (fun a r => a + reversal c ch r a) a
      = rs.foldl (fun m r => max m (owed c ch r)) a := by
  induction rs generalizing a with
  | nil => rfl
  | cons r rs ih => simp only [List.foldl]; rw [step_is_max]; exact ih _

theorem foldl_max_le (f : Nat → Nat) (c : Nat) (hf : ∀ r, f r ≤ c) :
    ∀ (rs : List Nat) (a : Nat), a ≤ c → rs.foldl (fun m r => max m (f r)) a ≤ c := by
  intro rs
  induction rs with
  | nil => intro a h; exact h
  | cons r rs ih => intro a h; exact ih _ (by show max a (f r) ≤ c; have := hf r; omega)

theorem foldl_max_ge_acc (f : Nat → Nat) :
    ∀ (rs : List Nat) (a : Nat), a ≤ rs.foldl (fun m r => max m (f r)) a := by
  intro rs
  induction rs with
  | nil => intro a; exact Nat.le_refl a
  | cons r rs ih => intro a; exact Nat.le_trans (Nat.le_max_left a (f r)) (ih _)

theorem foldl_max_ge_mem (f : Nat → Nat) :
    ∀ (rs : List Nat) (a x : Nat), x ∈ rs → f x ≤ rs.foldl (fun m r => max m (f r)) a := by
  intro rs
  induction rs with
  | nil => intro a x hx; cases hx
  | cons r rs ih =>
    intro a x hx
    cases hx with
    | head => exact Nat.le_trans (Nat.le_max_right _ _) (foldl_max_ge_acc f rs _)
    | tail _ hx => exact ih _ _ hx

/-- The total is the largest amount any one event said was owed, so it does
not depend on the order the events arrive in. -/
theorem total_is_largest_owed (c ch : Nat) (rs : List Nat) :
    total c ch rs = rs.foldl (fun m r => max m (owed c ch r)) 0 :=
  foldl_step c ch rs 0

/-- Whatever refunds arrive, in whatever order, the total never exceeds what
the purchase credited. -/
theorem total_bounded (c ch : Nat) (rs : List Nat) : total c ch rs ≤ c := by
  rw [total_is_largest_owed]
  exact foldl_max_le _ c (owed_le c ch) rs 0 (Nat.zero_le c)

/-- Once the full refund is among the events, in any position, the total is
exactly what the purchase credited. -/
theorem total_full_refund (c ch : Nat) (rs : List Nat) (hpos : 0 < ch)
    (hfull : ch ∈ rs) : total c ch rs = c := by
  apply Nat.le_antisymm (total_bounded c ch rs)
  rw [total_is_largest_owed]
  have hc : owed c ch ch = c := by
    unfold owed; rw [Nat.mul_div_cancel c hpos, Nat.min_self]
  have := foldl_max_ge_mem (owed c ch) rs 0 ch hfull
  omega

end Proofs.Refund
