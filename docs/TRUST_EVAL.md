# Trust & Accuracy Eval Results

This project's anti-hallucination claim — that `verify_citation`, `audit_bibliography`, and the search/scrape pipeline catch fabricated, retracted, or mischaracterized sources rather than asserting them — is backed by a labeled-gold-set accuracy eval, not just design intent. This page publishes the measured numbers so the claim is citable, not just asserted.

## What's measured

Three eval suites, each `//go:build live` (hits real external APIs, opt-in, never part of the required per-PR test gate):

| Suite | File | What it drives |
|---|---|---|
| Trust suite | `internal/tools/trust_eval_live_test.go` | Real `verify_citation` + `audit_bibliography` paths against a curated gold set of known-fabricated, known-retracted, known-real, and mischaracterized citations, via live Crossref / DOI-handle-registry lookups |
| GEO-defense suite | `internal/search/geo_eval_live_test.go`, `internal/tools/geo_eval_*_test.go` | Real `web_search`/`search_and_scrape` against the failure modes documented in arXiv:2607.05217 — hard `site:` scoping vs. soft prompt-steering, fluency-blind domain reputation, claim corroboration, never-fabricate-on-zero-results |
| research_panel divergence | `internal/tools/research_panel_eval_test.go` | Real multi-model panel end-to-end, checking that an uncontroversial question yields high confidence with no contradictions while a contested one surfaces at least one contradiction or lower confidence |

Continuous verification: all three run on a weekly scheduled CI job (`trust-eval` in `.github/workflows/ci.yml`, Mondays 08:00 UTC) and on demand via `workflow_dispatch`. Each run appends its precision/recall/pass-fail output to the GitHub Actions job summary, so trend visibility survives without a hand-run. Live network + third-party rate limits (Crossref, DOI registry, search/LLM providers) make this suite too flaky to gate individual PRs on — it is a recurring regression guard on the moat, not a merge gate. Locally: `make test-eval`, `make test-geo-eval`, `go test -tags=live -run TestResearchPanelEvalAccuracy ./internal/tools/...`.

## Measured results

Run locally on 2026-08-07 (Go 1.25.12, commit `9f134eb`) with `CROSSREF_EMAIL`/`OPENALEX_EMAIL` set and full internet access. These are the actual numbers from that run — not projected or hand-calibrated.

### Trust suite (`TestTrustSuiteAccuracy_*`, 22.2s)

Existence + retraction signal, by gold-set category (26 real DOIs across 5 categories + 11 fabricated):

| Category | Existence precision/recall | Retraction precision/recall |
|---|---|---|
| canonical (7 cases) | 1.00 / 1.00 (tp=7) | 1.00 / 1.00 (tp=2, tn=5) |
| recent_retraction (5 cases) | 1.00 / 1.00 (tp=5) | 1.00 / 1.00 (tp=5) |
| regional_publisher (5 cases) | 1.00 / 1.00 (tp=5) | 1.00 / 1.00 (tn=5) |
| preprint_retraction_chain (4 cases) | 1.00 / 1.00 (tp=4) | 1.00 / 1.00 (tp=4) |
| malformed_edge_case (5 cases) | 1.00 / 1.00 (tp=5) | 1.00 / 1.00 (tn=5) |
| fabricated (11 cases) | 1.00 / 1.00 (tn=11) | 1.00 / 1.00 (tn=11) |
| **aggregate** | **1.00 / 1.00** (tp=26, tn=11, fp=0, fn=0) | **1.00 / 1.00** (tp=11, tn=26, fp=0, fn=0) |

Zero false positives across every category on this run — the invariant the suite enforces (`t.Errorf` on any `fp > 0`, except a calibrated 1-FP tolerance reserved for `recent_retraction`'s retraction signal alone, per #482, unused on this run).

Other signals:

| Signal | Result |
|---|---|
| Mischaracterization (audit_bibliography claim check) | precision=1.00, recall=0.50 (tp=1, tn=1, fn=1) — one off-topic claim under-flagged as `partially_addressed` rather than `not_addressed`; by design the suite tolerates under-flagging (never false-accusing) so this is not a failure |
| verify_citation claim check | precision=1.00, recall=1.00 (tp=1, tn=1) |
| Scraped-DOI retraction end-to-end | PASS — scraping a known-retracted Nature paper's landing page surfaced `detectedDoi=10.1038/nature12968` with `retractionStatus.retracted=true` |
| Unchecked classification | PASS — title-only entries with no resolvable identifier correctly classified `unchecked`, never falsely `ok` or `not_found` |
| titleMatch | precision=1.00, recall=1.00 (tp=1, tn=3) — a real DOI with an invented title is flagged `mismatch`; a real DOI with the correct (or partial) title is never flagged |

### GEO-defense suite (`TestGeoEval_*`, ~9s)

| Eval | Result |
|---|---|
| Hard `site:`-scoping containment (Eval 1) | 100% (64/64) across 8 lens+query pairs — every result stayed inside its lens's domain list, vs. the arXiv:2607.05217 paper's measured 12%→21% improvement from *soft* prompt-steering alone |
| Authoritative-source recall (Eval 5, logged not gated) | 3/3 — `pubmed.ncbi.nlm.nih.gov` for a clinical-trial query, `supremecourt.gov` for a Supreme Court opinion, `nvd.nist.gov` for a CVE |
| Corroboration agreement/disagreement/silence | PASS — agreeing sources raise no flag, disagreeing or silent sources raise `no_independent_corroboration` |
| Never-fabricate-on-zero-results | PASS — a lensed query with no coverage returns no results with `hints.reason=filters_too_restrictive`, never a fabricated answer |
| Reputation fluency invariance | PASS — an unlisted host gets no reputation tier regardless of how fluent its snippet reads; a listed host's tier doesn't change with snippet fluency |

### research_panel divergence eval (`TestResearchPanelEvalAccuracy`, 48.8s)

Both gold cases (a settled fact and a contested opinion) skipped on this run: only 1 of 2 queried panel members succeeded (`models_queried:2 models_succeeded:1`), and divergence analysis needs at least 2 members to compare. This environment had only one LLM credential (`OPENAI_API_KEY`) configured; a full run needs at least 2 of `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_AI_API_KEY`. The scheduled CI job requests all four as secrets so this suite exercises its actual divergence checks when at least two resolve.

## Reproducing

```bash
export CROSSREF_EMAIL=you@example.com
export OPENALEX_EMAIL=you@example.com   # optional, falls back to CROSSREF_EMAIL for some providers
make test-eval        # trust suite
make test-geo-eval     # GEO-defense suite (prefers GOOGLE_CUSTOM_SEARCH_API_KEY/ID, falls back to DuckDuckGo)
go test -tags=live -count=1 -v -run TestResearchPanelEvalAccuracy ./internal/tools/...   # needs >=2 of the 4 LLM keys
```

Every suite skips cleanly (not a failure) when its required credentials are absent.
