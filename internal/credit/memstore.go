package credit

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
)

// MemStore is an in-memory ledger, for tests and an offline run. It holds the
// same invariants the Postgres one does — the contract test drives both — and
// it is the reason a test about money needs no database.
type MemStore struct {
	mu      sync.Mutex
	entries []Entry
	clock   func() time.Time

	auth    Authority
	members Members
}

// NewMemStore builds an empty in-memory ledger. A nil Authority or Members
// means the check it would perform is skipped, which is what a unit test that
// is not about permissions wants.
func NewMemStore(auth Authority, members Members) *MemStore {
	return &MemStore{clock: time.Now, auth: auth, members: members}
}

// Balance sums everything but the holds, which are commitments rather than
// spend.
func (s *MemStore) Balance(_ context.Context, h Holder) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sum(h, false), nil
}

// Available sums everything including the holds: what may still be committed.
func (s *MemStore) Available(_ context.Context, h Holder) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sum(h, true), nil
}

// sum folds one holder's entries. withHolds distinguishes the two balances; it
// is the single place the difference between them is expressed.
func (s *MemStore) sum(h Holder, withHolds bool) int64 {
	var n int64
	for _, e := range s.entries {
		if e.Holder != h {
			continue
		}
		if !withHolds && e.Kind.commits() {
			continue
		}
		n += e.Amount
	}
	return n
}

// BalancesFor folds each requested holder in one pass. A holder with no entry is
// omitted; the caller reads a missing key as zero.
func (s *MemStore) BalancesFor(_ context.Context, holders []Holder) (map[Holder]int64, error) {
	want := make(map[Holder]bool, len(holders))
	for _, h := range holders {
		want[h] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[Holder]int64, len(want))
	for _, e := range s.entries {
		if !want[e.Holder] || e.Kind.commits() {
			continue
		}
		out[e.Holder] += e.Amount
	}
	return out, nil
}

// NegativeWallets returns every wallet in the red, most-owed first. Pots are
// excluded — a negative pot is an overdraft, not a debt an account owes.
func (s *MemStore) NegativeWallets(_ context.Context) ([]WalletBalance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bal := map[Holder]int64{}
	for _, e := range s.entries {
		if e.Kind.commits() {
			continue
		}
		bal[e.Holder] += e.Amount
	}
	out := []WalletBalance{}
	for h, n := range bal {
		if n >= 0 {
			continue
		}
		if email, ok := WalletEmail(h); ok {
			out = append(out, WalletBalance{Email: email, Balance: n})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Balance < out[j].Balance })
	return out, nil
}

// Entries lists a holder's ledger, newest first.
func (s *MemStore) Entries(_ context.Context, h Holder, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = defaultEntryLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Entry{}
	for _, e := range s.entries {
		if e.Holder == h {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Topup creates credits in one email's wallet.
func (s *MemStore) Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if amount <= 0 {
		return ErrNotPositive
	}
	if err := requireAdmin(ctx, s.auth, byAdmin); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.append(Entry{Holder: Wallet(email), Amount: amount, Kind: KindTopup, Actor: byAdmin.Email})
}

// TopupPaid posts a purchased credit into a wallet, idempotent on ref. No
// principal: authority came from a verified webhook signature, upstream.
func (s *MemStore) TopupPaid(_ context.Context, email string, credited int64, ref, actor string) error {
	if err := checkPaid(email, credited, ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasRef(ref) {
		return nil
	}
	return s.append(Entry{Holder: Wallet(email), Amount: credited, Kind: KindTopup, Actor: actor, ExternalRef: ref})
}

// RefundPaid reverses a purchase with a negative adjust, idempotent on ref. The
// wallet may go negative — the debt a clawed-back purchase leaves.
func (s *MemStore) RefundPaid(_ context.Context, email string, amount int64, ref, actor string) error {
	if err := checkPaid(email, amount, ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasRef(ref) {
		return nil
	}
	return s.append(Entry{Holder: Wallet(email), Amount: -amount, Kind: KindAdjust, Actor: actor, ExternalRef: ref})
}

// Spend charges a pot for a model call that is not a session, idempotent on ref.
func (s *MemStore) Spend(_ context.Context, pot Holder, amount int64, ref, actor string) error {
	if strings.TrimSpace(ref) == "" {
		return ErrNoRef
	}
	if amount < 0 {
		return ErrNotPositive
	}
	if amount == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasRef(ref) {
		return nil
	}
	return s.append(Entry{Holder: pot, Amount: -amount, Kind: KindDraft, Actor: actor, ExternalRef: ref})
}

// hasRef reports whether an entry with this external reference already exists.
// The caller holds the lock. It is the mem store's idempotency, mirroring the
// partial unique index the Postgres one leans on.
func (s *MemStore) hasRef(ref string) bool {
	for _, e := range s.entries {
		if e.ExternalRef == ref {
			return true
		}
	}
	return false
}

// EntryByExternalRef finds the entry carrying ref. The reference is unique, so
// there is at most one.
func (s *MemStore) EntryByExternalRef(_ context.Context, ref string) (Entry, bool, error) {
	if strings.TrimSpace(ref) == "" {
		return Entry{}, false, ErrNoRef
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ExternalRef == ref {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// TotalOutstanding sums every holder's settled balance — wallets and pots, less
// open holds — the credits sold and not yet delivered as compute.
func (s *MemStore) TotalOutstanding(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, e := range s.entries {
		if e.Kind.commits() {
			continue
		}
		n += e.Amount
	}
	return n, nil
}

// Allocate moves credits from the caller's wallet into a project's pot.
func (s *MemStore) Allocate(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := requireRole(ctx, s.members, by, project, access.RoleCollaborator); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sum(Wallet(by.Email), true) < amount {
		return ErrInsufficient
	}
	return s.transfer(Wallet(by.Email), Pot(project), amount, KindAllocate, by.Email, project, projectName)
}

// Reclaim moves credits from a project's pot back to the caller's wallet.
func (s *MemStore) Reclaim(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := requireRole(ctx, s.members, by, project, access.RoleLead); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Against Available, not Balance: credits held for a running session are
	// already committed to it and cannot be pulled back out.
	if s.sum(Pot(project), true) < amount {
		return ErrInsufficient
	}
	return s.transfer(Pot(project), Wallet(by.Email), amount, KindReclaim, by.Email, project, projectName)
}

// transfer writes both sides of a move. Under the same lock, so a reader never
// sees money in neither place. projectID/projectName are stamped on both sides
// so the wallet side and the pot side agree on the project (024b).
func (s *MemStore) transfer(from, to Holder, amount int64, kind Kind, actor, projectID, projectName string) error {
	if err := s.append(Entry{Holder: from, Amount: -amount, Kind: kind, Actor: actor, ProjectID: projectID, ProjectName: projectName}); err != nil {
		return err
	}
	return s.append(Entry{Holder: to, Amount: amount, Kind: kind, Actor: actor, ProjectID: projectID, ProjectName: projectName})
}

// append records one entry. The caller holds the lock.
func (s *MemStore) append(e Entry) error {
	id, err := newID()
	if err != nil {
		return err
	}
	e.ID = id
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.clock().UTC()
	}
	s.entries = append(s.entries, e)
	return nil
}

// requireAdmin re-checks the caller's tier. Shared by both stores, so an
// admin-only write holds even where a route was mounted without the guard —
// the belt-and-braces access and platform already use.
func requireAdmin(ctx context.Context, auth Authority, p access.Principal) error {
	if auth == nil {
		return nil
	}
	tier, _, err := auth.Resolve(ctx, p)
	if err != nil {
		return err
	}
	if tier != access.TierAdmin {
		return ErrNotAdmin
	}
	return nil
}

// requireRole re-checks the caller's role in a project against the minimum this
// operation needs. Shared by both stores for the same reason.
func requireRole(ctx context.Context, members Members, p access.Principal, project string, least access.Role) error {
	if members == nil {
		return nil
	}
	role, ok, err := members.Role(ctx, p, project)
	if err != nil {
		return err
	}
	if !ok || role < least {
		if least == access.RoleLead {
			return ErrNotLead
		}
		return ErrNotMember
	}
	return nil
}

// Fund moves credits from an email's wallet into a project's pot, with no
// membership check — see creationOps for why the create path cannot ask that
// question.
func (s *MemStore) Fund(_ context.Context, email, project, projectName string, amount int64) error {
	if err := checkMove(email, project, amount); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sum(Wallet(email), true) < amount {
		return ErrInsufficient
	}
	return s.transfer(Wallet(email), Pot(project), amount, KindAllocate, email, project, projectName)
}

// Unwind returns everything a project's pot can still commit to an email's
// wallet, as the system.
func (s *MemStore) Unwind(_ context.Context, project, email string) error {
	if err := checkMove(email, project, 1); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	amount := s.sum(Pot(project), true)
	if amount <= 0 {
		return nil
	}
	// The project is being torn down, so there is no name worth snapshotting and
	// nothing to link to; the id is stamped so the two sides still agree.
	return s.transfer(Pot(project), Wallet(email), amount, KindReclaim, ActorSystem, project, "")
}
