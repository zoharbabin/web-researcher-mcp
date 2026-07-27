package tools

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

// Divergence thresholds — heuristic, no LLM call. overlapRatio compares two
// sentences' shared significant terms via content.ClaimTermCoverage.
const (
	consensusOverlapThreshold = 0.8 // sentence pair counts as "the same claim"
	consensusModelFraction    = 0.8 // fraction of OTHER models that must restate it
	contradictionOverlapFloor = 0.6 // same topic, lower bar than consensus since polarity differs
	minSentenceLen            = 12
)

// PanelDivergence is the structured agreement/disagreement summary across a
// research_panel run, per issue #302's output contract.
type PanelDivergence struct {
	ConsensusPoints     []string             `json:"consensus_points"`
	Contradictions      []PanelContradiction `json:"contradictions"`
	UniqueToModel       map[string][]string  `json:"unique_to_model"`
	Confidence          string               `json:"confidence"`
	ConfidenceRationale string               `json:"confidence_rationale"`
}

// PanelContradiction is one claim where two panel members took opposing
// positions.
type PanelContradiction struct {
	Claim     string            `json:"claim"`
	Positions map[string]string `json:"positions"`
}

// panelClaim is one candidate sentence extracted from a single model's
// response, tagged with the model ID it came from.
type panelClaim struct {
	modelID string
	text    string
	lower   string
}

// AnalyzeDivergence compares every panel member's response and extracts
// consensus, contradictions, and model-unique claims. Deterministic and
// dependency-free: reuses content.ClaimTermCoverage (keyword overlap) and
// content.HasContrastCue (negation/refutation polarity) — no synthesis LLM
// call, per issue #302's explicit design.
func AnalyzeDivergence(responses map[string]string) PanelDivergence {
	var claims []panelClaim
	for modelID, text := range responses {
		for _, s := range splitPanelSentences(text) {
			claims = append(claims, panelClaim{modelID: modelID, text: s, lower: strings.ToLower(s)})
		}
	}

	modelIDs := make([]string, 0, len(responses))
	for id := range responses {
		modelIDs = append(modelIDs, id)
	}

	var consensus []string
	contradictions := []PanelContradiction{}
	unique := make(map[string][]string, len(responses))
	consensusSeen := make(map[string]bool)
	contradictedPairs := make(map[string]bool)

	for i, claim := range claims {
		otherModels := make(map[string]bool)
		hasContradictionPartner := false
		var contradictionPartner panelClaim

		for j, other := range claims {
			if i == j || other.modelID == claim.modelID {
				continue
			}
			overlap := sentenceOverlap(claim.text, other.text)
			if overlap >= consensusOverlapThreshold {
				otherModels[other.modelID] = true
			}
			if overlap >= contradictionOverlapFloor && overlap < consensusOverlapThreshold {
				if content.HasContrastCue([]string{claim.lower}) != content.HasContrastCue([]string{other.lower}) {
					hasContradictionPartner = true
					contradictionPartner = other
				}
			}
		}

		otherModelCount := len(modelIDsExcept(modelIDs, claim.modelID))
		if otherModelCount > 0 && float64(len(otherModels))/float64(otherModelCount) >= consensusModelFraction {
			key := claim.lower
			if !consensusSeen[key] {
				consensusSeen[key] = true
				consensus = append(consensus, claim.text)
			}
			continue
		}

		if hasContradictionPartner {
			pairKey := contradictionPairKey(claim, contradictionPartner)
			if !contradictedPairs[pairKey] {
				contradictedPairs[pairKey] = true
				contradictions = append(contradictions, PanelContradiction{
					Claim: claim.text,
					Positions: map[string]string{
						claim.modelID:                claim.text,
						contradictionPartner.modelID: contradictionPartner.text,
					},
				})
			}
			continue
		}

		if len(otherModels) == 0 {
			unique[claim.modelID] = append(unique[claim.modelID], claim.text)
		}
	}

	confidence, rationale := panelConfidence(len(responses), len(consensus), len(contradictions))

	return PanelDivergence{
		ConsensusPoints:     consensus,
		Contradictions:      contradictions,
		UniqueToModel:       unique,
		Confidence:          confidence,
		ConfidenceRationale: rationale,
	}
}

func panelConfidence(modelsSucceeded, consensusCount, contradictionCount int) (string, string) {
	switch {
	case modelsSucceeded < 2:
		return "low", "fewer than 2 models succeeded; no cross-model comparison was possible"
	case contradictionCount == 0:
		return "high", fmt.Sprintf("%d models agreed on core claims with no contradictions detected", modelsSucceeded)
	case consensusCount > contradictionCount:
		return "medium", fmt.Sprintf("%d contradiction(s) detected alongside %d consensus point(s)", contradictionCount, consensusCount)
	default:
		return "low", fmt.Sprintf("%d contradiction(s) detected with limited consensus (%d point(s))", contradictionCount, consensusCount)
	}
}

// sentenceOverlap returns the fraction of a's significant terms that also
// appear in b, via content.ClaimTermCoverage (a treated as the "claim").
func sentenceOverlap(a, b string) float64 {
	matched, total := content.ClaimTermCoverage(b, a)
	if total == 0 {
		return 0
	}
	return float64(matched) / float64(total)
}

func modelIDsExcept(modelIDs []string, exclude string) []string {
	out := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		if id != exclude {
			out = append(out, id)
		}
	}
	return out
}

func contradictionPairKey(a, b panelClaim) string {
	if a.modelID < b.modelID {
		return a.modelID + "|" + a.lower + "||" + b.modelID + "|" + b.lower
	}
	return b.modelID + "|" + b.lower + "||" + a.modelID + "|" + a.lower
}

// splitPanelSentences breaks a model's response into candidate claim
// sentences on ./!/? and newline boundaries. Mirrors content's internal
// sentence splitter (unexported there, so re-implemented minimally here);
// fragments shorter than minSentenceLen are dropped as noise.
func splitPanelSentences(text string) []string {
	var sentences []string
	var b strings.Builder
	flush := func() {
		s := strings.TrimSpace(b.String())
		if len(s) >= minSentenceLen {
			sentences = append(sentences, s)
		}
		b.Reset()
	}
	runes := []rune(text)
	for i, r := range runes {
		b.WriteRune(r)
		switch r {
		case '\n':
			flush()
		case '.', '!', '?':
			if i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
				flush()
			}
		}
	}
	flush()
	return sentences
}
