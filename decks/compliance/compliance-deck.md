---
marp: true
title: "Compliance as Architecture"
description: "A solo-maintained open-source project aligned with 23 security & privacy standards — without writing 23 checklists. How architecture, not paperwork, keeps a small Go repo up to code; most slides name the file that backs the claim, and a CI drift gate keeps the tool docs honest against the code."
author: web-researcher-mcp
paginate: true
size: 16:9
style: |
  /* ── Design tokens — matched to assets/social-preview.svg ───────────────── */
  :root {
    --bg: #F9FDFC;          /* off-white surface (social-preview) */
    --ink: #0F172A;         /* headings */
    --body: #334155;        /* body text */
    --muted: #64748B;       /* captions / footer */
    --line: #E2E8F0;        /* hairlines / table borders */
    --card: #F1F5F9;        /* soft card / even rows */
    --indigo: #4F46E5;
    --indigo-soft: #EEF2FF;
    --cyan: #0EA5C9;        /* text-accent cyan (preview) */
    --grad-from: #22F0E8;   /* logo gradient start */
    --grad-to: #5B4CFF;     /* logo gradient end */
  }

  /* Overflow-proof base: every slide is a centered flex column with a hard
     content cap, so long copy shrinks/centers instead of running off-slide. */
  section {
    background: var(--bg);
    color: var(--body);
    font-family: "Inter", "SF Pro Display", -apple-system, system-ui, sans-serif;
    font-size: 23px;
    line-height: 1.4;
    letter-spacing: -0.01em;
    padding: 56px 72px 64px;
    display: flex;
    flex-direction: column;
    justify-content: center;
    overflow: hidden;
  }
  section > * { max-width: 100%; }

  h1 {
    color: var(--ink);
    font-size: 40px;
    font-weight: 800;
    letter-spacing: -0.025em;
    line-height: 1.1;
    margin: 0 0 6px;
    padding-bottom: 14px;
    flex: none;
  }
  /* the social-preview accent bar: short cyan→indigo gradient under the title */
  h1::after {
    content: "";
    display: block;
    width: 84px; height: 4px;
    margin-top: 16px;
    border-radius: 2px;
    background: linear-gradient(90deg, var(--cyan) 0%, var(--indigo) 100%);
  }
  h2 { color: var(--indigo); font-size: 28px; font-weight: 700; letter-spacing: -0.02em; margin: 0; }
  h3 { color: var(--muted); font-weight: 600; }
  strong { color: var(--ink); font-weight: 700; }
  em { color: var(--muted); font-style: italic; }
  a { color: var(--indigo); }
  p { margin: 0.45em 0; }

  code {
    background: var(--indigo-soft);
    color: var(--indigo);
    border-radius: 5px;
    padding: 1px 7px;
    font-family: "Geist Mono", "JetBrains Mono", monospace;
    font-size: 0.84em;
    font-weight: 600;
  }

  table { font-size: 0.8em; border-collapse: collapse; width: 100%; margin: 0.4em 0; }
  th { background: var(--indigo); color: #fff; padding: 9px 14px; border: none; text-align: left; font-weight: 600; }
  tbody tr:nth-child(even) { background: var(--card); }
  td { color: var(--body); border: none; border-bottom: 1px solid var(--line); padding: 8px 14px; vertical-align: top; }
  td strong, th { letter-spacing: -0.01em; }

  blockquote {
    border-left: 4px solid;
    border-image: linear-gradient(180deg, var(--cyan), var(--indigo)) 1;
    background: var(--card);
    color: var(--ink);
    font-size: 1em;
    font-weight: 600;
    padding: 14px 22px;
    margin: 0.5em 0;
    border-radius: 0 8px 8px 0;
  }

  ul, ol { margin: 0.3em 0; padding-left: 1.1em; }
  li { margin-bottom: 0.4em; }
  li::marker { color: var(--cyan); }

  footer {
    color: var(--muted);
    font-size: 13px;
    font-family: "Geist Mono", "JetBrains Mono", monospace;
    letter-spacing: 0;
  }
  section::after { color: var(--muted); font-size: 14px; }  /* pagination */

  /* ── Lead / thesis (light, brand gradient) ──────────────────────────────── */
  section.lead { justify-content: center; }
  section.lead h1 { font-size: 50px; padding-bottom: 0; }
  section.lead h1::after { display: none; }
  section.lead h2 { color: var(--cyan); font-size: 30px; margin-top: 10px; }
  section.lead .tagline { color: var(--body); font-size: 22px; font-weight: 500; margin-top: 22px; max-width: 86%; line-height: 1.45; }
  section.lead code { background: transparent; color: var(--muted); padding: 0; font-weight: 500; font-size: 0.78em; }

  section.thesis {
    background: linear-gradient(135deg, #0F172A 0%, #1E1B4B 100%);
    color: #fff;
    justify-content: center;
  }
  section.thesis h1 { color: #fff; }
  section.thesis strong { color: var(--grad-from); }
  section.thesis em { color: #CBD5E1; }
  section.thesis code { background: rgba(255,255,255,0.08); color: var(--grad-from); }
  section.thesis::after { color: rgba(255,255,255,0.45); }

  /* dense standards-wall slide */
  section.dense { font-size: 20px; }

  /* convergence pillars */
  .pillars { display: flex; gap: 20px; margin: 0.3em 0; }
  .pillar {
    flex: 1; background: #fff; border: 1px solid var(--line);
    border-top: 3px solid var(--cyan);
    border-radius: 10px; padding: 16px 20px;
    box-shadow: 0 1px 3px rgba(15,23,42,0.05);
    line-height: 1.7;
  }
  .pillar strong { color: var(--indigo); }
  .pillar p { margin: 0.5em 0; }
  .pillar .sub { color: var(--muted); font-size: 0.8em; font-weight: 400; }

  /* recurring positioning pills (light, social-preview style) */
  .pills { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 14px; }
  .pill {
    border-radius: 999px; padding: 6px 16px;
    font-size: 0.62em; font-weight: 600; letter-spacing: 0.01em;
    background: var(--indigo-soft); color: var(--indigo);
  }
  .pill.cyan { background: #E0F7FA; color: var(--cyan); }
  .pill.indigo { background: var(--indigo-soft); color: var(--indigo); }

  /* logo watermark, bottom-right of content slides */
  section.wm::before {
    content: "";
    position: absolute;
    right: 44px; bottom: 52px;
    width: 46px; height: 46px;
    background: url("data:image/svg+xml;base64,PD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZz0iVVRGLTgiPz4KPHN2ZyB2aWV3Qm94PSIwIDAgNTEyIDUxMiIgZmlsbD0ibm9uZSIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj4KICA8IS0tIHdlYi1yZXNlYXJjaGVyLW1jcCBsb2dvOiBoZXggZnJhbWUgKyBmb2N1c2VkIGRyb3AgKyBjb3JlIGRvdCAtLT4KCiAgPHJlY3Qgd2lkdGg9IjUxMiIgaGVpZ2h0PSI1MTIiIHJ4PSI5NiIgZmlsbD0iIzBGMTcyQSIvPgogIDxkZWZzPgogICAgPGxpbmVhckdyYWRpZW50IGlkPSJnMSIgeDE9IjEyMCIgeTE9IjcyIiB4Mj0iMzkyIiB5Mj0iNDIwIiBncmFkaWVudFVuaXRzPSJ1c2VyU3BhY2VPblVzZSI+CiAgICAgIDxzdG9wIG9mZnNldD0iMCUiIHN0b3AtY29sb3I9IiMyMkYwRTgiLz4KICAgICAgPHN0b3Agb2Zmc2V0PSIxMDAlIiBzdG9wLWNvbG9yPSIjNUI0Q0ZGIi8+CiAgICA8L2xpbmVhckdyYWRpZW50PgogIDwvZGVmcz4KCiAgPCEtLSBIZXggZnJhbWUg4oCUIHZlcnRpY2FsbHkgY2VudGVyZWQgYWNjb3VudGluZyBmb3IgdGhlIGRyb3AgYmVsb3cgLS0+CiAgPHBhdGggZD0iTTI1NiA4NCBMMzg4IDE2MCBMMzg4IDMxMiBMMjU2IDM4OCBMMTI0IDMxMiBMMTI0IDE2MCBaIgogICAgICAgIHN0cm9rZT0idXJsKCNnMSkiIHN0cm9rZS13aWR0aD0iNDAiIHN0cm9rZS1saW5lam9pbj0icm91bmQiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgZmlsbD0ibm9uZSIvPgoKICA8IS0tIFJvdW5kZWQgZHJvcCDigJQgc2VhbWxlc3NseSBleHRlbmRzIGZyb20gYm90dG9tIHZlcnRleCAtLT4KICA8cGF0aCBkPSJNMjQyIDM4NCBRMjU2IDQ0OCAyNzAgMzg0IgogICAgICAgIHN0cm9rZT0idXJsKCNnMSkiIHN0cm9rZS13aWR0aD0iMjgiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgZmlsbD0ibm9uZSIvPgoKICA8IS0tIENvcmUgZG90IOKAlCBzbGlnaHRseSBhYm92ZSB0cnVlIGNlbnRlciBmb3Igb3B0aWNhbCBiYWxhbmNlIC0tPgogIDxjaXJjbGUgY3g9IjI1NiIgY3k9IjIzMiIgcj0iMjgiIGZpbGw9IiNFMEY4RjUiLz4KPC9zdmc+Cg==") no-repeat center / contain;
    opacity: 0.10;
    pointer-events: none;
  }

  /* title lockup */
  .titlelogo { width: 96px; height: 96px; margin-bottom: 20px; display: block; }
---

<!-- _class: lead -->
<!-- _paginate: false -->

<img class="titlelogo" src="data:image/svg+xml;base64,PD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZz0iVVRGLTgiPz4KPHN2ZyB2aWV3Qm94PSIwIDAgNTEyIDUxMiIgZmlsbD0ibm9uZSIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj4KICA8IS0tIHdlYi1yZXNlYXJjaGVyLW1jcCBsb2dvOiBoZXggZnJhbWUgKyBmb2N1c2VkIGRyb3AgKyBjb3JlIGRvdCAtLT4KCiAgPHJlY3Qgd2lkdGg9IjUxMiIgaGVpZ2h0PSI1MTIiIHJ4PSI5NiIgZmlsbD0iIzBGMTcyQSIvPgogIDxkZWZzPgogICAgPGxpbmVhckdyYWRpZW50IGlkPSJnMSIgeDE9IjEyMCIgeTE9IjcyIiB4Mj0iMzkyIiB5Mj0iNDIwIiBncmFkaWVudFVuaXRzPSJ1c2VyU3BhY2VPblVzZSI+CiAgICAgIDxzdG9wIG9mZnNldD0iMCUiIHN0b3AtY29sb3I9IiMyMkYwRTgiLz4KICAgICAgPHN0b3Agb2Zmc2V0PSIxMDAlIiBzdG9wLWNvbG9yPSIjNUI0Q0ZGIi8+CiAgICA8L2xpbmVhckdyYWRpZW50PgogIDwvZGVmcz4KCiAgPCEtLSBIZXggZnJhbWUg4oCUIHZlcnRpY2FsbHkgY2VudGVyZWQgYWNjb3VudGluZyBmb3IgdGhlIGRyb3AgYmVsb3cgLS0+CiAgPHBhdGggZD0iTTI1NiA4NCBMMzg4IDE2MCBMMzg4IDMxMiBMMjU2IDM4OCBMMTI0IDMxMiBMMTI0IDE2MCBaIgogICAgICAgIHN0cm9rZT0idXJsKCNnMSkiIHN0cm9rZS13aWR0aD0iNDAiIHN0cm9rZS1saW5lam9pbj0icm91bmQiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgZmlsbD0ibm9uZSIvPgoKICA8IS0tIFJvdW5kZWQgZHJvcCDigJQgc2VhbWxlc3NseSBleHRlbmRzIGZyb20gYm90dG9tIHZlcnRleCAtLT4KICA8cGF0aCBkPSJNMjQyIDM4NCBRMjU2IDQ0OCAyNzAgMzg0IgogICAgICAgIHN0cm9rZT0idXJsKCNnMSkiIHN0cm9rZS13aWR0aD0iMjgiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgZmlsbD0ibm9uZSIvPgoKICA8IS0tIENvcmUgZG90IOKAlCBzbGlnaHRseSBhYm92ZSB0cnVlIGNlbnRlciBmb3Igb3B0aWNhbCBiYWxhbmNlIC0tPgogIDxjaXJjbGUgY3g9IjI1NiIgY3k9IjIzMiIgcj0iMjgiIGZpbGw9IiNFMEY4RjUiLz4KPC9zdmc+Cg==" alt="web-researcher-mcp" />

# One maintainer. One open-source repo. Aligned with **23** security & privacy standards.

## We did *not* write 23 checklists.

<br>

<span class="tagline">How good architecture — not a pile of paperwork — keeps a small open-source project audit-ready across the world's privacy and security rules, with no compliance team and no audit budget.</span>

`↳ each technical claim names the file that backs it · a CI drift gate keeps the tool docs honest against the code`

---

<!-- _class: dense wm -->

# The standards wall

**23 frameworks · 6 domains · across the US, EU, UK & global bodies.**

| Domain | Where it applies | Frameworks aligned to |
|--------|------------------|-----------------------|
| **Information security** | Global · US · UK | ISO 27001 · SOC 2 Type II · NIST CSF 2.0 · FedRAMP · BSIMM · UK Cyber Essentials · NCSC CAF |
| **Privacy & data rights** | EU · UK · US · APAC | GDPR / UK GDPR · Global CBPR |
| **Healthcare data** | US | HIPAA · HITRUST CSF |
| **AI & agentic systems** | EU · US · Global | EU AI Act · NIST AI RMF · OWASP LLM Top 10 · OWASP Agentic Top 10 *(2026 draft)* |
| **AI-tool (MCP) security** | Global · US | OWASP MCP · CSA MCP · NSA MCP Guidance |
| **Supply chain & protocol** | EU · Global | EU CRA · NIS2 · FIRST PSIRT · MITRE ATT&CK · RFC 9700/9449 |

## No compliance team. No budget for 23 audits. So how?

<!-- _footer: '↳ proof: docs/SECURITY_AND_COMPLIANCE.md → "Standards Alignment"' -->

---

<!-- _class: wm -->

# The trick: 23 standards → 8 properties

Strip the labels, and they demand **the same handful of things**:

<div class="pillars">
<div class="pillar">

**Access control**

**Encryption** <span class="sub">rest + transit</span>

**Audit trail**

</div>
<div class="pillar">

**Data minimization**

**Tenant isolation**

**Supply-chain integrity**

</div>
<div class="pillar">

**Vulnerability handling**

**Transparency**

</div>
</div>

<br>

Compliance teams call this *control mapping* — and it's usually a 1,400-row
spreadsheet and a consultant. **We built the 8 properties into the code instead.**

> **The payoff:** add the 23rd standard and you write **zero** new code — you
> already satisfy it. Compliance cost stops scaling with the number of regimes.

<!-- _footer: '↳ proof: docs/SECURITY_AND_COMPLIANCE.md "Compliance Posture" · docs/SECURITY.md crosswalks' -->

---

<!-- _class: wm -->

# The architecture that satisfies them

The 8 properties are baked into *how the code is built* — so they hold everywhere,
not just where someone remembered to add them:

- **One way in for every dependency** — nothing reaches in through a back door, so there's one place to audit
- **Swappable parts behind clean seams** — the cache, search, and audit layers can change without rewriting callers
- **One encryption routine** — a single AES-256-GCM helper for all at-rest encryption (opt-in via a key), never re-implemented per feature
- **One safe web-fetch client** — *every* outbound request goes through the same SSRF-checked path
- **An audit log on every tool call** — structured, secrets stripped out, and it never slows the request

> "Compliance through architecture, not bolt-on checklists."

<!-- _footer: '↳ proof: CLAUDE.md "Design Rules" · internal/cache/crypto.go · internal/scraper/ssrf.go' -->

---

<!-- _class: wm -->

# The safest config is the *default* config

Run it the normal way — locally, inside your AI app — and there's **no network
port open, no login to misconfigure, nothing exposed to the internet.** The app
that launched it is the only thing that can reach it. The typical user *can't*
hold it wrong, because there's nothing to set.

The heavyweight controls for shared, multi-user deployments — sign-in, per-customer
isolation, rate limits, encryption, audit logs — only switch on when an operator
deliberately runs it as a network server.

> **Secure by default, permissive by configuration.** Power is unlocked by an
> *explicit* choice, never the reverse.

<!-- _footer: '↳ proof: docs/SECURITY_AND_COMPLIANCE.md Principles 1 & 4, "Deployment Security"' -->

---

<!-- _class: wm -->

# Controls, not certificates: the honest line

"Compliance as architecture" ships the *controls*. It can't ship the *operating
organization* a certificate requires — so here's the split:

| Layer | Who owns it |
|-------|-------------|
| **The project ships the controls** | SSRF-safe fetch, AES-256-GCM at rest + TLS, secrets-masked audit logs, tenant isolation, OAuth 2.1, consent + erasure *primitives*, SBOM + signed releases + a PSIRT process |
| **The operator owns the process** | They're the **data controller** — sign BAAs/DPAs, set the retention *schedule* (code gives TTL knobs, not policy), run access reviews + incident response, choose lawful basis, run DPIAs |
| **A hosted SaaS adds the program** | Trained staff, controls *audited over time*, 24/7 IR, signed customer DPAs, and the actual **SOC 2 / HITRUST / ISO 27001 audit** — none of which a repo can contain |

> So "aligned with 23 standards" means *we provide the technical controls each one
> requires* — not that an organization has been audited against them. A binary
> can't clear a hospital's HIPAA bar; it hands that review its evidence.

<!-- _footer: '↳ proof: docs/SECURITY_AND_COMPLIANCE.md "Operator & Hosted-Service Responsibilities"' -->

---

<!-- _class: wm -->

# Agency sharpens one old threat — and adds a new one

Give an AI a tool that fetches any page, and it now *chooses the URLs*. That
**amplifies an old vuln** and **surfaces a new one**:

**Old vuln, now automated — SSRF (OWASP Web A10).** A hijacked link can steer an
*autonomous* fetch at your internal network. So before any fetch the server
rejects private/reserved IPs and cloud-metadata hosts, connects only to the
exact resolved address (DNS-rebind defense), and re-checks on every redirect.

**A genuinely new class — indirect prompt injection.** A booby-trapped page can
try to hijack the AI reading it. The server strips active markup, caps size, and
stamps every result that carries external text with an `untrusted-external-content`
marker — in the JSON *envelope*, never inside the content where a page could forge
it, and **enforced by a cross-tool drift test** so a new tool can't ship unmarked.
It does **not** enforce the prompt boundary — that's the *host's* job, where the
model and agent loop live.

> Prompt injection is **#1** on OWASP's LLM list, and the agentic rules are still
> being drafted. This tool sits squarely in that gap.

<!-- _footer: '↳ proof: internal/scraper/ssrf.go · internal/tools/scrape.go (envelope "trust" marker) · internal/tools/metadata_test.go (cross-tool gate) · internal/content/sanitize.go · OWASP Web A10 · LLM01' -->

---

<!-- _class: wm -->

# An erasure registry you can't outrun

Privacy starts with collecting little: the cache is content-addressed (not keyed
to a user), and the operator — not us — is the data controller. But the moment a
feature *does* store personal data, "right to be forgotten" has to actually work.

So it's enforced structurally: **every store that holds personal data must
register an Exporter + Eraser.** One `(tenant, user)` request fans out to all of
them — and each store ships a round-trip **release-gate test**.

> Add a new feature and forget to wire its eraser? **CI fails — not an auditor.**
> GDPR access / portability / erasure (Art. 15 / 17 / 20) becomes a property of
> the build, not a promise in a policy PDF.

<!-- _footer: '↳ proof: internal/datasubject/registry.go · internal/session/datasubject_test.go ("the #85 release gate")' -->

---

<!-- _class: wm -->

# Consent as a primitive: record → verify → honor

The AI-tool standard (MCP) says *asking* the user for consent is the **client
app's** job — so most servers do nothing. But whoever *stores* the data is, in
law, the **data controller**, and GDPR / Quebec Law 25 make *them* prove consent
was given and honor a withdrawal — a duty a login token can't discharge.

So the server treats consent as three things it actually does:

- **Records** it — encrypted, logged, with a typed purpose ("memory," "analytics," "workspace").
- **Verifies** it on every access — no record, no processing (it defaults to *off*).
- **Honors** it — a withdrawal automatically erases the data it covered.

<!-- _footer: '↳ proof: internal/consent/consent.go · internal/consent/store.go (HasConsent, fail-closed)' -->

---

<!-- _class: wm -->

# The docs are tested, not trusted

"Keep the docs accurate" isn't a good intention here — a stack of small
mechanisms makes drift hard to write and impossible to merge quietly:

| Layer | What keeps it honest |
|-------|----------------------|
| **Mechanical facts are machine-checked** | Tool lists, output shapes, read/write flags are never hardcoded — they point at the file that defines them |
| **Rules the AI writes by** | `CLAUDE.md` makes "every claim links to its file" a mechanical rule for Claude / Copilot / Cursor |
| **A test reads the docs** | At build time it parses `docs/TOOLS.md`, starts a real server, and fails if the documented tools or shapes don't match reality |
| **Gates that can't be skipped** | The doc-drift check runs in CI on **every** PR — even docs-only ones |

> The *judgment* — threat models, standards crosswalks — lives in prose and gets
> human review. Where a claim is enumerable, the build enforces it.

<!-- _footer: '↳ proof: CLAUDE.md "Documentation Guidelines" · internal/tools/metadata_test.go · .github/workflows/ci.yml (docs-drift)' -->

---

<!-- _class: wm -->

# So what is this actually worth?

The same architecture pays off differently for everyone who touches it:

| If you're… | What you get |
|------------|--------------|
| **A user** | The safe setup is the *default* — nothing to configure, nothing to leak |
| **An operator / buyer** | One small binary you can take to a hospital, an EU regulator, or a federal review — evidence links to code, not slideware |
| **A developer / contributor** | Add a feature and the guardrails (consent, erasure, audit, tests) come *with* it — you can't ship the unsafe version |
| **A founder / eng leader** | Compliance cost scales with *features you turn on*, not with the number of regulations or headcount |

> Compliance stops being a project you fund and becomes a property you inherit.

<!-- _footer: '↳ the ROI of "compliance as architecture"' -->

---

<!-- _class: wm -->

# What transfers to any project

1. **Map standards to primitives, not checklists** — satisfy many at once.
2. **Make the default the safe one** — gate power behind explicit config.
3. **Let compliance scale with features** (tiers) — not a big-bang program.
4. **Encode the rules as constraints, enforce them in CI** — including for AI coding agents.
5. **If a doc can be wrong without a test failing, it will be** — so test it.

<br>

*Honest boundaries: "aligned with," not "certified." The local and server threat
models differ. By default it stores little; the features that do store personal
data are opt-in, consent-gated, and erasable — not absent.*

<!-- _footer: '↳ five portable principles · no compliance framework required' -->

---

<!-- _class: thesis -->
<!-- _paginate: false -->

# Read the code, not the marketing.

Each technical claim here names the file that backs it — open any one and check.
And a CI drift gate keeps the tool docs honest against the code.

<br>

<br>

*Solo maintainer · MIT licensed · **contributors welcome.** Spot a claim that
doesn't match the code? Open an issue or a PR — that's the whole point. Come help
build it: `github.com/zoharbabin/web-researcher-mcp`*
