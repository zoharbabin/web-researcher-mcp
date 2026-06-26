# Extension Guide — choosing the right extension point

This guide answers one question: **when you want to add something new, which extension point do you use?**

The codebase has six extension points. They are not interchangeable — each maps to a different architectural contract. Pick the wrong one and you either fight the interface or silently miss a capability.

## Quick-pick table

| Extension point | Use when |
|---|---|
| **Tool** | New capability with its own input schema and output shape |
| **Provider** | Alternative backend for an existing search capability |
| **Lens** | Restrict `web_search` to a curated domain list — no custom output |
| **Enrichment Resolver** | Decorate DOI-bearing results post-search with side-channel data |
| **Prompt / Template** | Reusable multi-step research workflow for the model to orchestrate |
| **Resource** | Expose read-only server-side state via a URI scheme |

---

## Decision path

Start here. Work down until you hit a match.

1. **Does it produce output that fits an existing search interface** (web, image, news, academic, patent, answer, structured, filing, case, econ, trial, local)?
   - Yes → **Provider** (new backend, existing tool surface, no schema change)
   - No → continue

2. **Does it only restrict *which sites* a search queries, with no new fields in the response?**
   - Yes → **Lens** (domain allowlist; piggybacks on `web_search` output)
   - No → continue

3. **Does it annotate already-found results using a DOI, without changing the tool surface?**
   - Yes → **Enrichment Resolver** (`DOIRegistry`, `OAResolver`, `RetractionResolver`, or `DOIResolver`)
   - No → continue

4. **Does it expose a new capability with its own parameters and response shape?**
   - Yes → **Tool**

5. **Is it a pre-built research workflow the model should execute rather than raw data retrieval?**
   - Yes → **Prompt / Template**

6. **Is it server-side state (counters, health, artifacts) accessed by URI with no input?**
   - Yes → **Resource**

---

## Tool

A Tool is the right choice when nothing else fits: your feature has its own input parameters and produces output with a shape that doesn't map to any existing interface.

**When to use:**
- New retrieval capability with distinct structured output (`brand_research`, `local_search`, `clinical_search`)
- An action that mutates state or creates an external artifact (`archive_source`, `memory_save`)
- A synthesis or verification step that consumes inputs from other tools (`verify_citation`, `audit_bibliography`)

**When NOT to use:**
- The output shape matches an existing provider interface → use **Provider**
- You only need to restrict search to certain sites → use **Lens**
- You need to annotate existing DOI-bearing results → use **Enrichment Resolver**

**Hard case — brand_research:** brand logos, colors, and social handles can't be expressed as `web_search` results. The output schema is entirely different. Brand research must be a Tool, with optional BrandFetch and web-search enrichment layers inside the handler.

**Integration checklist:**
1. `internal/tools/<name>.go` — typed input struct + `register<Name>()` + `readOnlyAnnotations()` or `writeAnnotations()`
2. `internal/tools/registry.go` — add `register<Name>(srv, deps)` to `RegisterAll()`
3. `internal/tools/tools_test.go` + `metadata_test.go` (`expectedTools`) — tests and drift guard
4. `docs/TOOLS.md` — `## Tool N: \`name\`` section (drift-tested by CI)
5. `make gen-python-client` → stage and commit regenerated `python/web_researcher_mcp/`

See `CLAUDE.md → How to Add a Tool` for the full step-by-step.

---

## Provider

A Provider is a new backend for a capability the codebase already knows how to surface. The tool layer doesn't change — it just gets a new option in the provider map.

**When to use:**
- A new search engine or data source whose results fit `web_search`, `academic_search`, `patent_search`, `answer`, `structured_search`, `filing_search`, `legal_search`, `econ_search`, `clinical_search`, or `local_search`
- Adding a second source for an existing capability (e.g. a second clinical-trial registry alongside ClinicalTrials.gov)

**When NOT to use:**
- The output has fields that don't exist in the existing tool's response → use **Tool**
- You need to filter which sites existing providers query → use **Lens**

**Hard case — World Bank under `econ_search`:** World Bank's indicators fit `EconProvider` output exactly (series ID, country, period, value). No new tool needed — it wires in under `deps.EconProviders` alongside FRED and OECD.

**Integration checklist:**
1. `internal/search/<name>.go` — implement the matching interface; `var _ Provider = (*XProvider)(nil)` assertion
2. `internal/search/provider.go` (for `search.Provider`) or `domain.go` / `synthesis.go` / `structured_domains.go` — add to `Supported…Providers` + `New…ProviderByName` + `Available…Providers`
3. `internal/config/config.go` + `.env.example` — env var + required-when-selected check
4. Wire `circuit.New(...)` inside `Available…Providers` (every provider gets its own breaker)

See `CLAUDE.md → How to Add a Search Provider` for the full step-by-step.

---

## Lens

A Lens is a JSON domain allowlist. When a caller sets the `lens` parameter on `web_search`, the router injects `site:` operators for every domain in the list. The output is ordinary `web_search` results — no new fields, no new schema.

**When to use:**
- You want to restrict `web_search` to a known-good set of sites for a domain (legal databases, medical publishers, open-access repositories)
- Optional: add a `cx` field to route to a Google Programmable Search Engine, or a `goggle` URL for Brave re-ranking

**When NOT to use:**
- You need structured output specific to the domain (e.g. case docket metadata, clinical trial phases) → use **Tool** + **Provider**
- The new capability requires a different API entirely, not just filtered web search → use **Tool**

**Hard case — legal research:** A lens can restrict searches to `courtlistener.com`, `law.cornell.edu`, etc. But a `legal_search` tool with typed docket fields, citation numbers, and jurisdiction metadata cannot be expressed as filtered web search results. Both exist: `legal_search` (Tool + CourtListener Provider) for structured case law, and `legal` / `legal-proceedings` lenses for unstructured site-restricted research.

**Integration checklist:**
1. `lenses/<name>.json` — `{"name":"…","description":"…","domains":[…]}` (optionally `"cx":"…"` or `"goggle":"…"`)
2. `make sync-lenses` — copies to `internal/search/lenses_embed/`; `TestEmbeddedLensesMatchRoot` fails CI if out of sync
3. `lenses/README.md` — add a row to the table

Schema reference: `lenses/README.md`.

---

## Enrichment Resolver

An Enrichment Resolver is an interface that decorates already-found results with side-channel data. Resolvers operate post-search — they take a DOI and add fields (retraction status, open-access PDF link, existence confirmation) to results the tool already has.

**When to use:**
- You have a side-channel API keyed on DOI that adds provenance or integrity signals to existing results
- The new data belongs on the same result object, not as a separate tool call

**When NOT to use:**
- You need a new query axis or input parameters → use **Tool** or **Provider**
- The data source isn't keyed on DOI → consider a **Tool** instead

**The four resolver interfaces** (all in `internal/search/`):

| Interface | What it adds | Implementation |
|---|---|---|
| `DOIRegistry` | Cross-registrar DOI existence (doi.org handle API) | `doi_registry.go` |
| `DOIResolver` | Exact-entity DOI lookup (title-match guard against fabrication) | `domain.go` |
| `OAResolver` | Open-access PDF link via Unpaywall | `unpaywall.go` |
| `RetractionResolver` | Retraction/expression-of-concern status via Crossref Retraction Watch | `retraction.go` |

**Integration checklist:**
1. `internal/search/<name>.go` — implement the resolver interface
2. `main.go` — construct and wire into the matching `deps` field
3. Tool handlers that consume it: best-effort (never fail the tool call on resolver error); nil-check before calling

---

## Prompt / Template

An MCP Prompt is a pre-built research workflow the model invokes by name. It returns a filled-in message sequence the model uses to orchestrate a multi-step investigation — not raw data.

**When to use:**
- You want to guide the model through a standard research pattern (systematic review, competitive analysis, due-diligence checklist) without hardcoding the steps into a tool
- The workflow reuses existing tools in a defined sequence

**When NOT to use:**
- The workflow needs to return structured data to a caller → use **Tool**
- It's just a search with different parameters → use a **Lens**

**Integration checklist:**
1. `internal/resources/prompts.go` — add a `mcp.Prompt` entry to the registered prompt list
2. No schema drift test covers prompts; keep descriptions current manually

---

## Resource

An MCP Resource exposes read-only server-side state via a URI scheme. Resources accept no input parameters — they are polled or read on demand.

**When to use:**
- Operator-facing state: health signals, error rings, provider stats (`diagnostics://`, `stats://`)
- The lenses catalog (`lenses://catalog`) — metadata about registered lenses
- Large payloads returned by reference (`research://artifact/{id}`) — the tool stores the payload; the caller fetches it via the resource URI

**When NOT to use:**
- The caller needs to pass parameters → use **Tool**
- The data changes in response to input → use **Tool**

**Integration checklist:**
1. `internal/resources/resources.go` — register the URI template + handler
2. If adding a new URI scheme, document it in `docs/DEPLOYMENT.md` (operator-facing) or `docs/TOOLS.md` (if surfaced in tool output)

---

## Canonical ambiguous cases

These come up repeatedly. The answer is always the same.

**"Could this be a lens?"**  
Only if the output is plain web search results. If you need any custom fields — company data, typed metadata, domain-specific schema — it's a Tool. Lenses filter; they don't transform.

**"Could this be a provider instead of a tool?"**  
Only if the output matches an existing capability interface exactly. Check the interface in `internal/search/` first. If you'd need to add fields to make it fit, it needs a new Tool.

**"Should this be an enrichment resolver or a tool?"**  
Enrichment resolvers are keyed on a DOI that already exists in a search result. If your side-channel data requires the caller to supply a query or URL, it's a Tool. If it silently annotates results as a side effect, it's a resolver.

**"Prompt or tool?"**  
A Prompt guides the model to use existing tools in sequence. A Tool produces data directly. If a caller outside the model loop needs to consume the output programmatically, it must be a Tool.
