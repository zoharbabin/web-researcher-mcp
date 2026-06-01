# Privacy / Regulatory / Compliance / Security — Issue Prioritization

A prioritized view of the **open** GitHub issues that touch privacy, regulation,
compliance, or security (PRCS), with the reasoning behind each rank.

> Snapshot date: 2026-05-31. Reviewed all 37 open issues; 14 are PRCS-relevant
> (listed below), 4 are PRCS-adjacent (noted at the end), the rest are
> feature/provider/pipeline work outside this scope.

---

## How these are ranked

Priority here is **not** the issue's own `Pn` label (that's product priority).
It's *compliance/security urgency*, scored on four factors:

1. **Ships-today impact** — does it affect the security/privacy posture of what
   runs in production *right now* (local STDIO + multi-tenant HTTP)?
2. **Promise debt** — do our own docs already commit to it? An unkept doc promise
   is a drift/honesty risk and directly undercuts the deck's "read the code, not
   the marketing" claim.
3. **Blocker status** — does it unblock other PRCS work?
4. **Effort vs. external dependency** — cheap wins and externally-gated items
   are called out.

A feature that is **opt-in and default-off** carries *no* urgency until someone
builds the feature that consumes it — by design, its compliance ships *with* it.

---

## The prioritized list

| Rank | Issue | Category | Why this rank | Ships-today? | Effort | Blocked by |
|------|-------|----------|---------------|--------------|--------|-----------|
| **P0** | **#84** flip `CORS_STRICT` to fail-closed | Security | Secure-by-default for a server that can hold API keys in shared deployments. Docs in 4 places already promise the flip — pure promise debt. Affects **every** HTTP deployment today. | ✅ Yes | Low | — |
| **P0** | **#85** GDPR data-subject-rights endpoints (access/erasure/portability) | Regulatory | `docs/SECURITY.md` marks Art. 15/17/20 as "planned, not implemented." Real requirement for enterprise/gov HTTP tenants, and the **deck now cites this gap** — closing it removes the single largest honesty caveat. | ✅ Yes (HTTP) | Medium | — |
| **P1** | **#42** Redis backend for distributed state | Security | Per-tenant rate limits are **per-pod** today: a tenant hitting N pods gets N× the limit, defeating cost/abuse protection on billed APIs. `REDIS_URL` is accepted but dead config. Integrity gap for any multi-instance deployment + foundation for #43. | ⚠️ Multi-instance only | High | — |
| **P1** | **#89** Consent-management subsystem | Compliance | Foundational: legally-required recorded consent for *every* regulated feature (#88, #92, #94, #95). Inert until one ships, but must exist **before** any of them. Build it with the first consuming feature, not after. | ❌ Inert until used | Medium | — |
| **P1** | **#43** Horizontal-scaling gaps for multi-tenant HTTP | Security | Reliability/integrity of the multi-tenant guarantees (isolation, rate-limit math, audit pod-ID). Some parts (docs, typed `SessionNotFoundError`, pod ID in audit) ship now; the rest waits on #42. | ⚠️ Multi-instance only | Medium (split) | Partly #42 |
| **P1** | **#71** Complete SignPath OSS code signing | Security (supply chain) | Windows `.exe` is unsigned → SmartScreen warnings erode supply-chain trust. Pipeline is fully wired behind a flag; it's a **quick win once SignPath assigns the cert** — externally gated, low effort. | ✅ Yes (releases) | Low | External (SignPath cert) |
| **P2** | **#91** Tenant-level aggregate analytics | Privacy | Aggregate-only (no per-user profiling) → "legitimate interest" zone, no consent infra needed, but **privacy docs must describe it** before shipping. Low regulatory load. | ❌ Not built | Medium | — |
| **P3** | **#94** Opt-in summarization (AI-labeled) | Regulatory | EU AI Act Art. 50 labeling. Default-off; compliance (non-disableable label + source links) ships with the feature. No urgency until built. | ❌ Default-off | Medium | #89 (maybe) |
| **P3** | **#90** Generative UI components (AI-labeled) | Regulatory | Same AI-labeling obligation as #94; always carries raw underlying data. Default-off. | ❌ Default-off | Medium | #48 |
| **P3** | **#88** Opt-in long-term memory | Privacy / regulatory | Persistent per-user history → retention + DSR obligations the current TTL design avoids. Must build on #89 + #85 first. Default-off. | ❌ Default-off | High | #89, #85 |
| **P3** | **#92** Opt-in user-level analytics | Privacy | User-level profiling under GDPR/Quebec Law 25 → consent-gated. Default-off. | ❌ Default-off | Medium | #89 |
| **P3** | **#96** Opt-in shared research workspaces | Privacy / security | Most governance-heavy: deliberately crosses the per-tenant isolation boundary. Copy-not-reference design; owner retains delete rights. Default-off. | ❌ Default-off | High | #89 |
| **P3** | **#93** Opt-in scheduled recurring searches | Privacy | Unattended processing + longer-lived storage → needs bounded lifetime + auto-disable. Default-off. | ❌ Default-off | Medium | — |
| **P3** | **#95** Content-based source recommendations | Privacy | Explicitly the *non-profiling* version (uses content quality signals, not user behavior); fences off the GDPR Art. 22 personalized variant. Transparent by design. Default-off. | ❌ Default-off | Low–Medium | — |

---

## Recommended sequencing

**Do now (closes promise debt on the shipped product):**
1. **#84** — small, secure-by-default, already promised. One flag flip + migration note.
2. **#85** — operates on the small real surface (tenant/user-scoped sessions + audit), not a new user store. Removes the deck's biggest caveat.
3. **#71** — flip the signing flag the moment SignPath issues the cert (no eng work, real trust gain).

**Do next (multi-tenant integrity, if/when scaling to multiple instances):**
4. **#42 → #43** — only urgent for multi-instance HTTP; ship #43's Redis-independent parts (docs, typed errors, pod-ID) regardless.

**Do with the first regulated feature (not before):**
5. **#89** — the consent foundation, built alongside whichever of #88/#92/#94/#95 lands first.

**No urgency (opt-in, default-off, compliance ships with the feature):**
6. Everything in P3 + #91. These exist to prove the *tiered-compliance* model — each is inert and harmless until explicitly enabled.

---

## PRCS-adjacent (reviewed, intentionally excluded from the ranking)

| Issue | Why adjacent, not core |
|-------|------------------------|
| **#58** routing observability | Explicitly designed as operator/debug data, **not** content the LLM reads — an information-disclosure *boundary* decision, but operational, not a compliance obligation. |
| **#81** `diagnostics://` MCP Resource | Operator diagnostics with info-disclosure care (config state, recent errors); operational tier-3 error UX, no regulatory driver. |
| **#83** cache-key contract doc | Documents an already-**fixed** idempotency/cross-provider-collision bug (`cachekey_test.go` guards it). Correctness/isolation-adjacent, but a docs chore. |
| **#65** research session export | Reproducible audit trail for professional/legal use — compliance-*defensibility* value, but fundamentally a feature, not a control. |

---

## Relationship to the compliance deck

The deck's accuracy depends on exactly two of these, and both are **already
handled honestly** in the current draft:

- **#85** — deck states erasure is "tenant-scoped purge + TTL today; formal
  per-user endpoints on the roadmap," and footnotes the issue. ✅ No overclaim.
- **#84** — deck never claims fail-closed-by-default CORS. ✅ No overclaim.

**Conclusion: no issue must be implemented before sharing the deck.** Shipping
#84 + #85 would let a future deck revision *upgrade* the GDPR claim from
"roadmap" to "implemented" — a strengthening, not a correction.
