package content

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// Source-credibility signal (#159): a transparent, descriptive reputation tier
// per domain, complementing the typed SourceClassification (#62) with a
// reliability axis so a model can hedge by source quality. Strictly descriptive
// — it NEVER gates, filters, or reorders results.
//
// Design choices that keep this neutral, auditable, and safe:
//   - The default dataset is a HIGH-AUTHORITY ALLOWLIST only (governments,
//     established primary sources, major reference works). We deliberately do
//     NOT ship per-outlet "low" verdicts — branding specific publishers
//     unreliable is an editorial/defamation minefield and not our call. Unlisted
//     domains are "unknown" (no false confidence). The full tier vocabulary
//     (high|mixed|low|unknown) exists so OPERATORS can extend with their own
//     curated dataset for their context.
//   - The default ships embedded (go:embed) so it works zero-config regardless
//     of working directory; it is plain JSON in the repo, fully auditable.
//   - An operator can replace the whole dataset (LoadReputationDataset), so the
//     signal is swappable, not a black box.

// DomainReputation is the descriptive reputation of a single domain.
type DomainReputation struct {
	// Tier is the reliability band: high | mixed | low | unknown.
	Tier string `json:"tier"`
	// Basis names the dataset/criteria behind the tier, so the signal is auditable.
	Basis string `json:"basis,omitempty"`
	// Note is an optional human-readable rationale.
	Note string `json:"note,omitempty"`
}

// Reputation tier constants.
const (
	ReputationHigh    = "high"
	ReputationMixed   = "mixed"
	ReputationLow     = "low"
	ReputationUnknown = "unknown"
)

// reputationDataset is the on-disk/embedded shape: a version + basis label and a
// host→tier map. Hosts are registrable domains (no scheme, no "www.").
type reputationDataset struct {
	Version string                      `json:"version"`
	Basis   string                      `json:"basis"`
	Domains map[string]DomainReputation `json:"domains"`
}

//go:embed data/domain_reputation.json
var defaultReputationJSON []byte

var (
	reputationMu      sync.RWMutex
	reputationDomains map[string]DomainReputation
	reputationOnce    sync.Once
)

// initReputation loads the embedded default dataset once. Safe and lazy: the
// first LookupDomainReputation triggers it; LoadReputationDataset can override.
func initReputation() {
	reputationOnce.Do(func() {
		var ds reputationDataset
		if err := json.Unmarshal(defaultReputationJSON, &ds); err == nil {
			setReputation(ds)
		} else {
			reputationMu.Lock()
			reputationDomains = map[string]DomainReputation{}
			reputationMu.Unlock()
		}
	})
}

func setReputation(ds reputationDataset) {
	m := make(map[string]DomainReputation, len(ds.Domains))
	for host, rep := range ds.Domains {
		h := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
		if h == "" || rep.Tier == "" {
			continue
		}
		if rep.Basis == "" {
			rep.Basis = ds.Basis
		}
		m[h] = rep
	}
	reputationMu.Lock()
	reputationDomains = m
	reputationMu.Unlock()
}

// LoadReputationDataset replaces the active reputation dataset from raw JSON
// (operator override). Returns an error on malformed JSON, leaving the previous
// dataset intact. Empty input is a no-op error so a misconfiguration is loud.
func LoadReputationDataset(data []byte) error {
	var ds reputationDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return err
	}
	reputationOnce.Do(func() {}) // mark initialized so the embedded default won't clobber the override
	setReputation(ds)
	return nil
}

// LookupDomainReputation returns the reputation for a registrable host, or a
// pointer to an "unknown" reputation when the host isn't in the dataset. Never
// returns nil — callers can always read .Tier. Matches the host and its parent
// (so "abc.bbc.co.uk" inherits "bbc.co.uk").
func LookupDomainReputation(host string) DomainReputation {
	initReputation()
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	if host == "" {
		return DomainReputation{Tier: ReputationUnknown}
	}
	reputationMu.RLock()
	defer reputationMu.RUnlock()
	if rep, ok := reputationDomains[host]; ok {
		return rep
	}
	// Parent-domain match: walk up labels (abc.example.com → example.com).
	labels := strings.Split(host, ".")
	for i := 1; i < len(labels)-1; i++ {
		parent := strings.Join(labels[i:], ".")
		if rep, ok := reputationDomains[parent]; ok {
			return rep
		}
	}
	return DomainReputation{Tier: ReputationUnknown}
}
