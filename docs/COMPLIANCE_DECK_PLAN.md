# Deck Plan — "Compliance as Architecture: Lessons from Building a Standards-Ready MCP Server"

A working plan for a short talk/deck sharing the security, privacy, and
compliance lessons, tools, and guidelines built into web-researcher-mcp.

> **Status:** plan only. Slide copy is indicative, not final. Every claim below
> is traceable to a file in this repo (sources noted per slide) so the deck
> never drifts from reality — the same rule the project applies to its docs.

---

## 1. The Core Thesis (the one thing to remember)

**You don't comply with 20+ standards by doing 20+ checklists. You comply by
building a small number of architectural properties that satisfy the shared
requirements underneath all of them — and then you let tests prove it stays true.**

Everything in the deck ladders up to that sentence.

---

## 2. Audience & Framing

This is a **thought-leadership / engineering-culture** talk, not a sales pitch
and not a compliance audit walkthrough. Three audiences can all take something:

| Audience | What they take away |
|----------|--------------------|
| Engineers / OSS maintainers | Architecture patterns that make compliance cheap |
| Security & compliance people | A repeatable "map once, satisfy many" method |
| Eng leaders | A model for compliance that scales with features, not headcount |

Framing device: *"A two-person open-source project shouldn't be able to claim
alignment with HIPAA, GDPR, the EU AI Act, FedRAMP, and the EU Cyber Resilience
Act at once. Here's the architecture that makes it honest."*

---

## 3. Narrative Arc (5 acts, ~16 slides)

Short deck. Tight arc. One idea per slide. Each act below lists slides with the
**single message** and the **repo source** that backs it.

### Act 1 — The Setup: why this is unusually hard (3 slides)

**Slide 1 — Title**
- "Compliance as Architecture"
- Subtitle: *Lessons from making one small Go repo stay up to code across many
  geographies, regulations, and standards.*
- Visual: the faceted prism/lens mark (brand: indigo `#4F46E5` + cyan `#06B6D4`,
  dark background — design dark-mode first). Source: `docs/BRAND_GUIDELINES.md`.

**Slide 2 — The unusual threat surface**
- Message: an AI tool that *searches the web and reads arbitrary pages on your
  behalf* inherits a threat model most apps never face.
- Show the threat table condensed: SSRF, prompt injection via scraped content,
  cost abuse, cross-tenant leakage, cloud-metadata theft, supply-chain.
- Source: `docs/SECURITY_AND_COMPLIANCE.md` → "What We Protect Against".

**Slide 3 — The constraint that makes it interesting**
- Message: no compliance team, no budget for 20 separate audits — just a small
  open-source repo that still has to be defensible to a hospital, an EU
  regulator, and a US federal buyer.
- Tease the standards wall (next act). Set up the central question: *how?*

### Act 2 — The Big Idea: compliance through architecture (3 slides)

**Slide 4 — The standards wall**
- Message: this is the list we target — ISO 27001, SOC 2, NIST CSF 2.0, GDPR/UK
  GDPR, OWASP MCP + LLM + Agentic Top 10, NIST AI RMF, EU AI Act, EU Cyber
  Resilience Act, NIS2, FedRAMP, UK Cyber Essentials, NCSC CAF, HIPAA, HITRUST,
  FIRST PSIRT, MITRE ATT&CK, Global CBPR, RFC 9700/9449, CSA & NSA MCP guidance.
- Visual: a dense logo/standard grid (intentionally overwhelming for effect).
- Source: `docs/SECURITY_AND_COMPLIANCE.md` → "Standards Alignment" table.
- Punchline line: *"We did not write 23 checklists."*

**Slide 5 — The convergence insight (the payoff slide)**
- Message: strip the labels and the standards demand the **same handful of
  things**: access control, encryption at rest + transit, audit trail, data
  minimization, tenant isolation, supply-chain integrity, vulnerability
  handling, transparency.
- Visual: many standards on the left → funnel → ~8 architectural primitives on
  the right. (HITRUST alone maps to 40+ frameworks — make that the proof point.)
- This is the "cross many geographies" answer: map once to primitives, inherit
  many regimes.
- Source: `docs/SECURITY_AND_COMPLIANCE.md` → "Compliance Posture" + `docs/SECURITY.md`
  "Compliance Frameworks" (NIST CSF crosswalk, MITRE ATT&CK mapping).

**Slide 6 — The architecture that does the satisfying**
- Message: the primitives are *structural properties of the code*, not features.
- Zero global state (DI via `tools.Dependencies`), interface-driven swaps
  (`cache.Cache`, `search.Provider`, `audit.Auditor`), encrypted-at-rest by one
  shared AES-256-GCM layer, SSRF-safe client used everywhere, async audit on
  every tool call.
- "Compliance through architecture, not bolt-on checklists." (Principle 3.)
- Source: `CLAUDE.md` Design Rules; `docs/SECURITY_AND_COMPLIANCE.md` Principles.

### Act 3 — The Lessons (the interesting, non-obvious part) (5 slides)

**Slide 7 — Lesson: secure by default beats secure by configuration**
- Message: the most common deployment (local STDIO via Claude Code/Cursor) has
  **no network listener, no auth to misconfigure, no attack surface**.
  Multi-tenant security only switches on in HTTP mode.
- Insight: "STDIO is zero-trust-by-default" — the safest config is the default
  config, so the typical user can't hold it wrong.
- Source: `docs/SECURITY_AND_COMPLIANCE.md` Principles 1 & 4, "Deployment Security".

**Slide 8 — Lesson: AI tools have brand-new threat classes**
- Message: classic OWASP isn't enough. A scraper is an SSRF cannon; scraped
  content is an *indirect prompt-injection* vector.
- Show the two defenses: (1) SSRF — validate every resolved IP against 19 CIDR
  blocks, connect to the pinned IP (DNS-rebinding proof), re-check each redirect;
  (2) content sanitization + boundary marking + `contentType` signaling so the
  model knows scraped bytes are untrusted.
- Source: `docs/SECURITY_AND_COMPLIANCE.md` "SSRF Protection" & "Content Security";
  `internal/scraper/ssrf.go`.

**Slide 9 — Lesson: the strongest privacy posture is having nothing**
- Message: no servers, no accounts, no telemetry — queries go straight from the
  user's machine to the provider they chose. GDPR/CCPA become almost trivial
  because there is no data on our side to access, sell, or delete.
- Nuance worth showing: this is a *deliberate* design stance, and even the
  optional HTTP mode keeps data on the operator's own infrastructure.
- Source: `docs/PRIVACY.md` ("What We Do NOT Collect", the data-flow table).

**Slide 10 — Lesson: tiered compliance — pay only for what you turn on**
- Message: compliance scales with capability. Tier 1 (retrieval) carries the
  baseline; Tiers 2–4 (analytics, generation, personalization) each add only the
  governance their regulatory exposure demands. Lower tiers never pay for
  higher-tier infrastructure.
- Visual: 4-step staircase, each step labeled with the regimes it unlocks
  (Tier 3 → EU AI Act Art. 50 labeling; Tier 4 → GDPR Art. 22 / PIPL / Quebec
  Law 25). "Compliance infrastructure ships WITH the feature, never bolted on."
- Source: `docs/SECURITY_AND_COMPLIANCE.md` "Tiered compliance model".
- **This is the most original idea in the deck — give it room.**

**Slide 11 — Lesson: docs (and compliance claims) rot unless tests guard them**
- Message: "If documentation can be wrong without a test failing, it will
  eventually be wrong." So CI fails on doc drift — tool docs must match the
  registry, output schemas must match real responses, every tool must declare
  its annotations.
- Insight: this turns compliance claims into *verifiable* claims — "read the
  code, not the marketing."
- Source: `docs/LESSONS_LEARNED.md` Lesson 5; `internal/tools/metadata_test.go`
  (`TestToolsDocMatchesRegistry`, `TestAllToolsHaveAnnotations`,
  `TestOutputSchemaMatchesResponse`).

### Act 4 — The Tools & Guidelines we built (3 slides)

**Slide 12 — Guardrails for humans *and* AI agents**
- Message: we wrote the rules down as mechanical constraints, then made them
  apply to AI coding agents too (Claude Code, Copilot, Cursor) — because most
  code now arrives via an agent.
- Show highlights: 11 contributor security rules + AI-agent-specific rules
  ("never disable a security check to make a test pass", "never `--no-verify`",
  "use the SSRF-safe client, never `http.DefaultClient`", constant-time secret
  compare), plus explicit **security-review triggers** (auth, ssrf.go, cache
  keys, Dockerfile, CI).
- Source: `docs/SECURITY_AND_COMPLIANCE.md` "Contributor Security Rules" &
  "AI Agent Coding Rules"; `CLAUDE.md` Security Rules.

**Slide 13 — The automated gate**
- Message: rules nobody enforces are decoration. One command is the CI gate:
  `make verify` = fmt + vet + lint + gosec + govulncheck + race tests + e2e +
  build. Plus CodeQL, Dependabot, pinned tool versions, and network-free e2e
  security tests (SSRF, blocked schemes, OAuth/scope gate) that run without keys.
- Visual: a pipeline strip; call out that security scanners are *pinned in
  `go.mod`* so local and CI run identical versions.
- Source: `CLAUDE.md` Commands; `.github/workflows/ci.yml`;
  `docs/SECURITY_AND_COMPLIANCE.md` "Continuous Security Scanning".

**Slide 14 — Supply chain + vulnerability handling**
- Message: every release ships SBOM + cosign signatures + checksums; vuln
  handling follows the FIRST PSIRT lifecycle (receive → triage w/ CVSS v4.0 +
  CWE → remediate → coordinated disclosure → learn). This is what satisfies the
  EU CRA, NIS2, and the supply-chain pieces of everything else.
- Source: `docs/SECURITY_AND_COMPLIANCE.md` "Supply Chain Security" &
  "Vulnerability Management"; `.github/workflows/release.yml`.

### Act 5 — Takeaways (2 slides)

**Slide 15 — What transfers to any project**
- Five portable principles, stated as imperatives:
  1. Map standards to primitives, not checklists — satisfy many at once.
  2. Make the default the safe one; gate power behind explicit config.
  3. Let compliance scale with features (tiers), not with a big-bang program.
  4. Encode the rules as constraints, then enforce them in CI — including for AI
     agents.
  5. If a doc can be wrong without a test failing, it will be — so test it.

**Slide 16 — Close**
- Restate the thesis, then the proof offer: *"Every claim in this deck links to
  a file. Read the code, not the marketing."*
- Pointers: `docs/SECURITY_AND_COMPLIANCE.md` (one-stop), `docs/SECURITY.md`
  (deep technical), repo URL, MIT license.

---

## 4. Optional / Backup Slides (cut for time, keep in appendix)

- **The Go rewrite** — "don't fight your runtime": orphan processes, 430MB→25MB,
  4-tier scraping. Good if the audience is engineering-heavy.
  Source: `docs/LESSONS_LEARNED.md`.
- **Audience-routed docs** — one `SECURITY_AND_COMPLIANCE.md` with a "Who this is
  for" router (user / admin / developer / compliance officer). A lesson in
  documentation design.
- **MITRE ATT&CK mapping** — controls → techniques table, for security audiences.
- **Encryption + key rotation** — AES-256-GCM, `CACHE_ENCRYPTION_KEY_PREV` zero-
  downtime rotation, FIPS build option. For FedRAMP/HIPAA crowds.

---

## 5. Design Direction

- **Brand:** indigo `#4F46E5` primary, cyan `#06B6D4` accent, Slate 900 `#0F172A`
  surface. Dark-mode first. Geist/Inter for headers, Geist Mono/JetBrains Mono
  for code. (Source: `docs/BRAND_GUIDELINES.md`.)
- **Tone:** plain language. Lead with the problem a person feels, not the
  architecture. Keep protocol jargon (MCP, STDIO, JWKS) off headline slides; put
  it in speaker notes or appendix. (Source: brand "Tone rules" + plain-language
  preference.)
- **One idea per slide.** The standards wall is the only deliberately dense slide
  — its density *is* the message.
- **Recurring motif:** every lesson slide carries a tiny "↳ proof:
  `path/to/file`" footer to reinforce "verifiable, not marketing."

---

## 6. Recommended Length & Variants

- **Core deck:** 16 slides, ~12–15 min talk.
- **Lightning version:** Slides 1, 4, 5, 10, 13, 16 (~5 min) — thesis + the two
  most original ideas (convergence, tiers) + the gate + close.
- **Deep version:** add the four appendix slides → ~25 min + Q&A.

---

## 7. Build Options (pick one — see the question I'll ask)

| Format | Best for | How we'd build it |
|--------|----------|-------------------|
| Marp / reveal.js Markdown | Lives in the repo, version-controlled, diff-able, drift-checkable like the docs | One `.md` in `docs/` rendered to HTML/PDF |
| Google Slides / PPTX outline | Hand off to a designer, present from anywhere | Structured outline + speaker notes doc |
| Speaker notes only | You design slides yourself | Per-slide script + the data points |

My recommendation: **Marp Markdown in-repo** — it matches this project's whole
ethos (docs as code, drift-resistant, every claim next to its source), and it
renders to both an HTML deck and a PDF.
