package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

func newStore() *StoreImpl { return NewStore(persist.NewMemoryStore(), time.Hour) }

func TestContributeRequiresMembership(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	caller := Member{TenantID: "t1", UserID: "u1"}

	// Not a member yet → ErrNotMember.
	if _, err := s.Contribute(ctx, "ws1", caller, Contribution{Note: "x"}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}

	_ = s.AddMember(ctx, "ws1", caller)
	c, err := s.Contribute(ctx, "ws1", caller, Contribution{Note: "shared finding"})
	if err != nil {
		t.Fatalf("contribute after join: %v", err)
	}
	if c.ContributorTenant != "t1" || c.ContributorUser != "u1" {
		t.Errorf("provenance not stamped from caller: %+v", c)
	}
}

// TestNonMemberGetsZeroBytes is the release-gating invariant for #96: a
// non-member reading a workspace receives ErrNotMember and zero contributions,
// even when the workspace has data.
func TestNonMemberGetsZeroBytes(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	member := Member{TenantID: "t1", UserID: "u1"}
	_ = s.AddMember(ctx, "ws1", member)
	_, _ = s.Contribute(ctx, "ws1", member, Contribution{Note: "members only"})

	outsider := Member{TenantID: "t2", UserID: "u9"}
	got, err := s.Read(ctx, "ws1", outsider)
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember for outsider, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("non-member must get zero bytes, got %d contributions", len(got))
	}
}

// TestMembersReadEachOthersContributions is the core SHARED-workspace guarantee
// (and a regression guard against the per-user session-isolation change leaking
// into workspaces): a workspace is shared across its members, so member B must
// see member A's contribution. Workspace isolation is by MEMBERSHIP, never by
// individual user — unlike sessions, which are user-private.
func TestMembersReadEachOthersContributions(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	alice := Member{TenantID: "t1", UserID: "alice"}
	bob := Member{TenantID: "t1", UserID: "bob"}
	_ = s.AddMember(ctx, "ws1", alice)
	_ = s.AddMember(ctx, "ws1", bob)

	if _, err := s.Contribute(ctx, "ws1", alice, Contribution{Note: "alice's finding"}); err != nil {
		t.Fatalf("alice contribute: %v", err)
	}

	// Bob, a DIFFERENT user in the same workspace, must see Alice's contribution.
	got, err := s.Read(ctx, "ws1", bob)
	if err != nil {
		t.Fatalf("bob read: %v", err)
	}
	if len(got) != 1 || got[0].Note != "alice's finding" {
		t.Fatalf("workspace must be shared across members: bob saw %d contributions %+v", len(got), got)
	}
	if got[0].ContributorUser != "alice" {
		t.Errorf("attribution must stay alice, got %q", got[0].ContributorUser)
	}
}

func TestProvenanceNotCallerControlled(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	caller := Member{TenantID: "t1", UserID: "u1"}
	_ = s.AddMember(ctx, "ws1", caller)

	// Caller tries to forge provenance; the store must overwrite from identity.
	c, _ := s.Contribute(ctx, "ws1", caller, Contribution{
		Note:              "x",
		ContributorTenant: "evil-tenant",
		ContributorUser:   "evil-user",
	})
	if c.ContributorTenant != "t1" || c.ContributorUser != "u1" {
		t.Errorf("forged provenance not overwritten: %+v", c)
	}
}

func TestRemoveMemberRevokesAccess(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	m := Member{TenantID: "t1", UserID: "u1"}
	_ = s.AddMember(ctx, "ws1", m)
	if !s.IsMember(ctx, "ws1", m) {
		t.Fatal("expected member after add")
	}
	_ = s.RemoveMember(ctx, "ws1", m)
	if s.IsMember(ctx, "ws1", m) {
		t.Error("expected non-member after remove")
	}
	if _, err := s.Read(ctx, "ws1", m); !errors.Is(err, ErrNotMember) {
		t.Error("removed member must lose read access")
	}
}

func TestContributorErasureAcrossWorkspaces(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	m := Member{TenantID: "t1", UserID: "u1"}
	_ = s.AddMember(ctx, "ws1", m)
	_ = s.AddMember(ctx, "ws2", m)
	_, _ = s.Contribute(ctx, "ws1", m, Contribution{Note: "a"})
	_, _ = s.Contribute(ctx, "ws2", m, Contribution{Note: "b"})

	items, _ := s.ExportContributor(ctx, "t1", "u1")
	if len(items) != 2 {
		t.Fatalf("expected 2 contributions exported, got %d", len(items))
	}
	n, err := s.EraseContributor(ctx, "t1", "u1")
	if err != nil || n != 2 {
		t.Fatalf("expected 2 erased, got %d err=%v", n, err)
	}
	if items, _ := s.ExportContributor(ctx, "t1", "u1"); len(items) != 0 {
		t.Errorf("expected contributions gone after erase, got %d", len(items))
	}
}

func TestAnonymousNeverMember(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	if s.IsMember(ctx, "ws1", Member{TenantID: "t1", UserID: "anonymous"}) {
		t.Error("anonymous must never be a member")
	}
}

func TestNoopDeniesEverything(t *testing.T) {
	n := NewNoop()
	ctx := context.Background()
	if n.IsMember(ctx, "ws1", Member{TenantID: "t1", UserID: "u1"}) {
		t.Error("Noop must report no membership")
	}
	if _, err := n.Read(ctx, "ws1", Member{TenantID: "t1", UserID: "u1"}); !errors.Is(err, ErrNotMember) {
		t.Error("Noop read must be ErrNotMember")
	}
}

func TestDataSubjectRoundTrip(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	m := Member{TenantID: "t1", UserID: "u1"}
	_ = s.AddMember(ctx, "ws1", m)
	_, _ = s.Contribute(ctx, "ws1", m, Contribution{Note: "x"})

	a := AsDataSubject(s)
	out, err := a.ExportSubject(ctx, datasubject.Subject{TenantID: "t1", UserID: "u1"})
	if err != nil || out == nil {
		t.Fatalf("expected export, got %v err=%v", out, err)
	}
	deleted, err := a.EraseSubject(ctx, datasubject.Subject{TenantID: "t1", UserID: "u1"})
	if err != nil || deleted != 1 {
		t.Fatalf("expected 1 erased, got %d err=%v", deleted, err)
	}
}

// TestMaxContribEvictsOldest verifies the per-workspace contribution cap evicts
// the oldest contributions while membership/sharing semantics are unaffected.
func TestMaxContribEvictsOldest(t *testing.T) {
	ctx := context.Background()
	s := NewStore(persist.NewMemoryStore(), time.Hour).WithMaxContrib(3)
	m := Member{TenantID: "t1", UserID: "u1"}
	_ = s.AddMember(ctx, "ws1", m)

	for _, n := range []string{"a", "b", "c", "d", "e"} {
		if _, err := s.Contribute(ctx, "ws1", m, Contribution{Note: n}); err != nil {
			t.Fatalf("contribute %s: %v", n, err)
		}
	}
	got, err := s.Read(ctx, "ws1", m)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected cap of 3 contributions, got %d", len(got))
	}
	notes := map[string]bool{}
	for _, c := range got {
		notes[c.Note] = true
	}
	if notes["a"] || notes["b"] {
		t.Errorf("oldest contributions should be evicted, got %v", notes)
	}
}
