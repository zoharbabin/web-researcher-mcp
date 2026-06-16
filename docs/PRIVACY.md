# Privacy Policy

**Effective Date:** May 26, 2026  
**Last Updated:** June 16, 2026

## Overview

web-researcher-mcp is open-source software that runs entirely on your device. It gives AI assistants the ability to search the web and read pages on your behalf. We (the developers) have no servers, no accounts, and no way to see what you search for.

This policy explains what data flows where when you use the software.

## What We Do NOT Collect

We want to be unambiguous about this:

- **No telemetry.** We do not track how you use the software.
- **No analytics.** We do not measure usage, sessions, or feature adoption.
- **No personal information.** We never ask for your name, email, or any identifier.
- **No conversation data.** We do not access Claude's memory, chat history, or your uploaded files.
- **No advertising or profiling.** There is nothing to sell because we have nothing.

Because we operate no servers and collect no data, there is nothing stored on our end to access, correct, or delete.

## What Happens When You Use the Software

When you ask your AI assistant to search or read a page, here is exactly what happens:

| What | Where it goes | Who sees it |
|------|--------------|-------------|
| Your search query | Directly to the search provider you configured (Google, Brave, etc.) | The search provider — not us |
| Your API keys | Stored locally on your machine, sent only to the respective provider | Only the provider — never us |
| Search results | Returned to your machine and cached locally | Only you |
| Scraped page content | Fetched directly from the website to your machine (if you enable the optional paid Exa fallback, pages the local tiers cannot read are extracted via Exa instead) | Only you (and Exa, only for pages routed through that optional fallback) |
| Local cache | Stored on your device, optionally encrypted (AES-256) | Only you |

The key point: **your search queries travel directly from your machine to the search provider.** We are not in the middle. We never see what you search for.

## Third-Party Search Providers

When you search, your query goes directly to whichever provider you configured. Each provider has their own data practices:

| Provider | What they receive | Their privacy policy |
|----------|------------------|---------------------|
| Google Custom Search | Your query, your API key | [policies.google.com/privacy](https://policies.google.com/privacy) |
| Brave Search | Your query, your API key | [brave.com/privacy](https://brave.com/privacy/browser/) |
| Serper.dev | Your query, your API key | [serper.dev/privacy](https://serper.dev/privacy) |
| SearchAPI.io | Your query, your API key | [searchapi.io/privacy](https://www.searchapi.io/privacy-policy) |
| Tavily | Your query, your API key | [tavily.com/privacy](https://www.tavily.com/privacy) |
| Exa | Your query, your API key — and, only if you enable the optional paid scrape fallback, the page URL being read | [exa.ai/privacy-policy](https://exa.ai/privacy-policy) |
| SearXNG | Your query (self-hosted — no third party) | N/A (you control the server) |
| DuckDuckGo | Your query (zero-config default when no provider is configured) | [duckduckgo.com/privacy](https://duckduckgo.com/privacy) |
| HackerNews | Your query, sent to the Algolia HN Search API (keyless) | [algolia.com/policies/privacy](https://www.algolia.com/policies/privacy/) |
| OpenAlex | Your academic query; your optional contact email if `OPENALEX_EMAIL` is set (improves rate limits) | [openalex.org/legal](https://openalex.org/legal/privacy-policy) |
| CrossRef | Your academic query | [crossref.org/privacy](https://www.crossref.org/privacy/) |
| Semantic Scholar | Your academic / citation query (optional API key) | [allenai.org/privacy-policy](https://allenai.org/privacy-policy) |
| Unpaywall | A paper's DOI plus your contact email (open-access PDF lookup) | [unpaywall.org](https://unpaywall.org/) |
| SEC EDGAR | Your filing query / ticker, and a contact email in the request User-Agent (SEC requires it) | [sec.gov/privacy](https://www.sec.gov/privacy) |
| CourtListener | Your legal query (optional API token) | [free.law/privacy-policy](https://free.law/privacy-policy/) |
| FRED (St. Louis Fed) | Your economic query / series ID, your API key | [stlouisfed.org/legal](https://www.stlouisfed.org/legal) |
| World Bank | Your economic query / indicator code (keyless) | [worldbank.org/en/about/legal/privacy-notice](https://www.worldbank.org/en/about/legal/privacy-notice) |
| OECD | Your economic query / indicator code (keyless) | [oecd.org/en/about/privacy.html](https://www.oecd.org/en/about/privacy.html) |
| Eurostat | Your economic query / indicator code (keyless) | [ec.europa.eu/info/privacy-policy](https://ec.europa.eu/info/privacy-policy_en) |
| PubMed (NCBI) | Your academic query; your optional contact email if `PUBMED_EMAIL` is set | [nlm.nih.gov/web_policies.html](https://www.nlm.nih.gov/web_policies.html) |
| ClinicalTrials.gov | Your clinical-trial query (keyless) | [clinicaltrials.gov/about-site/privacy-policy](https://clinicaltrials.gov/about-site/privacy-policy) |

These are public-data APIs queried only when you call the matching tool (`academic_search`/`citation_graph`, `filing_search`, `legal_search`, `econ_search`, `clinical_search`). Only the search term, identifier, and any configured key/contact email are sent — no personal data beyond what you put in the query. EDGAR's contact email is a deliberate disclosure the SEC requires for automated access.

**You choose which provider to use.** If you want maximum privacy, SearXNG lets you self-host the entire search backend with no third-party involvement.

## MCP Platform Disclosure

When this software runs as a Claude tool (via MCP), Anthropic may independently collect tool call metadata according to their own privacy policy at [anthropic.com/privacy](https://www.anthropic.com/privacy). This is between you and Anthropic — our software does not add any additional data collection on top of what the MCP platform itself logs.

We confirm that this software:
- Does NOT access Claude's memory or conversation history
- Does NOT collect extraneous conversation data
- Does NOT query or extract data from user-generated files
- Only processes the specific query parameters passed to each tool call

## HTTP Server Mode (Optional)

If you choose to deploy the software as an HTTP server (multi-tenant mode), additional data is processed **on your own infrastructure**:

- **OAuth tokens** — validated for authentication, not stored. Revoked token IDs (JTIs) may be persisted locally to an encrypted store so a revocation survives a restart; the stored value is an opaque ID with an expiry, never the token contents or any claim.
- **Tenant identifiers** — used to isolate rate limits and sessions between users
- **Audit logs** — tool invocations are logged locally (no raw queries by default, only a length/hash). Audit files older than the configured retention window are deleted automatically.
- **Rate limit counters** — in-memory by default and cleared on restart. With `RATE_LIMIT_PERSIST=true` the per-tenant daily-quota counters are written to a local encrypted store so quotas survive a restart — still on your own machine, never transmitted.
- **Tenant aggregate analytics** — the server keeps **aggregate counts** per tenant (total calls, error rate, cache-hit rate, provider breakdown, latency percentiles) for billing and capacity planning, exposed only to the operator via the admin-gated `GET /admin/analytics` endpoint. This is **aggregate-only**: no per-query text, no per-user records, no content — just tallies keyed by tenant identifier. The lawful basis is the operator's **legitimate interest** in running and billing the service; because it is non-identifying at the individual level, it requires no separate consent. It is held in memory and is not transmitted off your infrastructure. (Per-*user* analytics is a distinct, consent-gated, off-by-default feature — see Your Rights.)

This mode is entirely self-hosted. We still do not receive or have access to any of this data. You are the data controller for your deployment.

## Local Data Storage

- **Cache location:** Your machine only (in-memory + optional encrypted disk)
- **Encryption:** AES-256-GCM when disk caching is enabled
- **Default retention:** Search results expire after 30 minutes, scraped pages after 1 hour, research sessions after 4 hours of inactivity
- **Clearing cache:** Restart the server or delete the cache directory
- **No remote sync:** Cache is never transmitted anywhere

## Your Rights

### For EU/EEA residents (GDPR)

This software runs entirely on your device (or your own server in HTTP mode). We do not act as a data controller or processor for any personal data you process using this software, as no personal data is transmitted to or accessible by us.

When you use third-party search APIs through this software, those API providers act as independent data controllers. Please review their respective privacy policies (linked above) for information about how they handle your data.

**HTTP-mode operators** are the data controller for their own deployment. To honor data-subject requests, the server provides admin-gated endpoints (see `docs/SECURITY.md` and `docs/DEPLOYMENT.md`):

- **Access & portability (Art. 15/20):** `GET /admin/data?tenant_id=&user_id=` exports, as JSON, everything the server holds for a subject across all stores.
- **Erasure (Art. 17):** `DELETE /admin/data?tenant_id=&user_id=` purges that data (memory + encrypted disk) and withdraws the subject's consent; the erasure is itself audited.

Because the server is designed to minimize per-user data (sessions are TTL-bounded, the cache is content-addressed and non-personal), the data actually subject to these requests is the subject's sessions plus any opt-in regulated-feature data (long-term memory, user analytics, workspace contributions) the operator has enabled.

**Shared workspaces** (opt-in, off by default) are the one place data deliberately crosses a per-user boundary, and only within a tenant: a contribution is a **copy** stamped with the contributor's identity, never a live link to their private data. Membership is managed by the host application (the server enforces the membership check on every access — a non-member receives nothing — but does not own the membership policy). Each contributor retains erasure rights over their own contributions across all workspaces, and workspace data is itself retention-bounded (`WORKSPACE_TTL`).

### For California residents (CCPA/CPRA)

We do not collect, sell, or share personal information as defined by the California Consumer Privacy Act. Because this software operates locally on your device and we have no access to your data, there is no personal information held by us to request access to, deletion of, or correction of.

### For everyone

You have full control over your data. You can:
- **Stop sharing queries** — stop using the software, or switch to a self-hosted SearXNG provider
- **Delete local cache** — restart the server or delete the cache directory
- **Revoke API keys** — delete them from your provider's dashboard
- **Audit the code** — the full source is open at [github.com/zoharbabin/web-researcher-mcp](https://github.com/zoharbabin/web-researcher-mcp)

## Children's Privacy

This software is not directed at children under 16. We do not knowingly collect information from children because we do not collect information from anyone.

## Security

- SSRF protection prevents the software from being used to probe internal networks
- Content sanitization strips potentially malicious content from scraped pages
- Rate limiting prevents abuse in HTTP server mode
- The full security architecture is documented at [SECURITY.md](https://github.com/zoharbabin/web-researcher-mcp/blob/main/docs/SECURITY.md)
- The source code is open for audit

## Open Source Transparency

This software is MIT-licensed and fully open source. You can read every line of code to verify these claims. Our security and architecture documentation is public. If you find a discrepancy between this policy and the code's behavior, please report it as a security issue.

## Changes to This Policy

If we change this policy, the change will appear in a GitHub commit with a clear diff. We will note material changes in release notes. The "Last Updated" date at the top will always reflect the most recent revision.

## Contact

- **GitHub Issues:** [github.com/zoharbabin/web-researcher-mcp/issues](https://github.com/zoharbabin/web-researcher-mcp/issues)
- **Maintainer:** Zohar Babin — [github.com/zoharbabin](https://github.com/zoharbabin)
