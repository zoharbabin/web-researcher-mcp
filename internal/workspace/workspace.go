// Package workspace implements opt-in shared research workspaces (#96) —
// reframed from "the server owns workspaces" to "the server is a tenant-scoped
// shared NAMESPACE the HOST authorizes access to". This split keeps tenant
// isolation a server-enforced guarantee while leaving membership/lifecycle
// (the control plane) to the host/IdP, matching MCP's architecture (the spec
// has no multi-client shared state; membership belongs to the host).
//
// What the SERVER provides (data plane, server-enforced):
//   - A workspace store keyed by workspaceID over the shared encrypted
//     persist.Store (the single intentional non-tenant key).
//   - Copy-in contribution: contributed data is DEEP-COPIED with immutable
//     provenance (contributor tenant/user, source, timestamp) — never a live
//     cross-tenant reference, so isolation is never silently voided.
//   - A membership CHECK on every access (defense-in-depth), derived ONLY from
//     validated OAuth claims (tenant/user in context) — NEVER from workspaceID
//     or a session id (per the MCP auth guidance).
//   - Per-contributor erasure (registered into #85) and workspace TTL.
//   - Audited cross-member data flows.
//
// What the HOST owns (control plane): which users/tenants belong to a
// workspace, creation/lifecycle. The server learns membership via a thin
// admin API the host drives. Off by default (WORKSPACES_ENABLED=false): no
// tools, no store, per-tenant isolation byte-for-byte unchanged.
package workspace

import (
	"context"
	"errors"
)

// ErrNotMember is returned (and surfaced as zero data) when a caller is not a
// member of the workspace they addressed. The release-gating invariant is
// "non-member gets zero bytes".
var ErrNotMember = errors.New("workspace: caller is not a member")

// Member identifies a workspace member by validated tenant+user identity.
type Member struct {
	TenantID string `json:"tenantId"`
	UserID   string `json:"userId"`
}

// Contribution is a deep copy of data a member shared into the workspace,
// stamped with immutable provenance so every item is attributable and erasable
// by its contributor.
type Contribution struct {
	ID                string   `json:"id"`
	WorkspaceID       string   `json:"workspaceId"`
	ContributorTenant string   `json:"contributorTenant"`
	ContributorUser   string   `json:"contributorUser"`
	SourceSessionID   string   `json:"sourceSessionId,omitempty"`
	Note              string   `json:"note"`
	URL               string   `json:"url,omitempty"`
	CreatedAt         string   `json:"createdAt"`
	Tags              []string `json:"tags,omitempty"`
}

// Store is the workspace data plane. All read/contribute operations take the
// caller's validated identity and enforce membership; none trust the
// workspaceID as authorization.
type Store interface {
	// AddMember / RemoveMember are the host-driven control-plane hooks (admin).
	AddMember(ctx context.Context, workspaceID string, m Member) error
	RemoveMember(ctx context.Context, workspaceID string, m Member) error
	IsMember(ctx context.Context, workspaceID string, m Member) bool
	// Contribute deep-copies data into the workspace with provenance. The caller
	// MUST be a member, else ErrNotMember.
	Contribute(ctx context.Context, workspaceID string, caller Member, c Contribution) (Contribution, error)
	// Read returns the workspace's contributions. The caller MUST be a member,
	// else ErrNotMember (zero bytes).
	Read(ctx context.Context, workspaceID string, caller Member) ([]Contribution, error)
	// EraseContributor removes a contributor's items across all workspaces
	// (data-subject erasure, #85).
	EraseContributor(ctx context.Context, tenantID, userID string) (int, error)
	// ExportContributor returns a contributor's items across all workspaces.
	ExportContributor(ctx context.Context, tenantID, userID string) ([]Contribution, error)
}
