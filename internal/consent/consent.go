// Package consent records, verifies, and honors user consent for regulated
// data processing. It is deliberately NOT a consent-acquisition UI: per the
// MCP specification, obtaining consent is the HOST application's responsibility
// ("Hosts must obtain explicit user consent before exposing user data to
// servers"). This server is the data controller for the personal data it
// persists (long-term memory #88, user analytics #92, workspaces #96), so
// GDPR Art. 7(1) / Quebec Law 25 require it to demonstrate consent and honor
// withdrawal — duties no OAuth token alone can prove.
//
// Architecture (hybrid): the host ASSERTS a consent decision (ideally as a
// verified OAuth claim on the per-tenant token, or via an explicit Record
// call); this package VERIFIES, RECORDS (encrypted, per-tenant, audited), and
// HONORS it. Every regulated write calls Checker.HasConsent(purpose) and
// no-ops/refuses when false. Withdrawal triggers the #85 erasure path.
package consent

import (
	"context"
	"errors"
)

// Purpose is a typed processing purpose. Consumers MUST use these constants
// rather than free-form strings so a feature can never check a purpose that no
// one can grant (and vice-versa).
type Purpose string

const (
	// PurposeMemory covers opt-in long-term, cross-session research memory (#88).
	PurposeMemory Purpose = "memory"
	// PurposeAnalytics covers opt-in per-user usage analytics (#92).
	PurposeAnalytics Purpose = "analytics"
	// PurposeWorkspace covers opt-in shared research workspaces (#96).
	PurposeWorkspace Purpose = "workspace"
)

// AllPurposes is the canonical set, used for validation and enumeration.
var AllPurposes = []Purpose{PurposeMemory, PurposeAnalytics, PurposeWorkspace}

// Audit event types for consent changes (dotted-namespace convention, shared
// across the codebase). Emitted by the consent admin/tool surface, not by the
// store, so the store stays a pure persistence layer.
const (
	EventGrant    = "consent.grant"
	EventWithdraw = "consent.withdraw"
)

// Valid reports whether p is a recognized purpose.
func (p Purpose) Valid() bool {
	for _, known := range AllPurposes {
		if p == known {
			return true
		}
	}
	return false
}

// ErrUnknownPurpose is returned when an unrecognized purpose is recorded.
var ErrUnknownPurpose = errors.New("unknown consent purpose")

// Record is a single, durable consent decision. Granted=false represents an
// explicit withdrawal (retained for auditability — withdrawal is not deletion
// of the record, it is a state change that downstream erasure acts on).
type Record struct {
	TenantID    string  `json:"tenantId"`
	UserID      string  `json:"userId"`
	Purpose     Purpose `json:"purpose"`
	Granted     bool    `json:"granted"`
	TermsVer    string  `json:"termsVersion,omitempty"`
	DecidedAt   string  `json:"decidedAt"` // RFC3339, stamped by the caller
	DecidedFrom string  `json:"decidedFrom,omitempty"`
}

// Checker is the read-side contract enforced at every regulated processing
// point. It is intentionally tiny so hot paths stay cheap and consumers cannot
// accidentally couple to storage details.
type Checker interface {
	// HasConsent reports whether the subject (tenant+user in ctx) currently has
	// a granted, non-withdrawn consent for purpose. It must be fail-closed:
	// any error, missing record, or anonymous user yields false.
	HasConsent(ctx context.Context, purpose Purpose) bool
}

// Manager is the full record/verify/honor contract: the Checker read side plus
// the write side used by the consent admin/tool surface.
type Manager interface {
	Checker
	// Record persists a consent decision (grant or withdrawal). Returns
	// ErrUnknownPurpose for an unrecognized purpose.
	Record(ctx context.Context, rec Record) error
	// Query returns the current decision for a purpose, or ok=false if none.
	Query(ctx context.Context, tenantID, userID string, purpose Purpose) (Record, bool)
	// Withdraw is a convenience for recording a withdrawal. Callers wire the
	// resulting downstream erasure (#85) separately.
	Withdraw(ctx context.Context, tenantID, userID string, purpose Purpose, decidedAt string) error
}
