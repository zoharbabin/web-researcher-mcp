# How Sessions Survive Context Loss

When an AI assistant runs out of memory mid-research, what happens to the work it already did?

This document explains the problem, how web-researcher-mcp solves it, and the design decisions behind the approach. Whether you're evaluating tools, building on this project, or just curious about LLM state management — this covers it.

---

## The Problem

Large language models have a fixed context window. When a conversation gets long enough, older content gets **compacted** — summarized or dropped to make room for new tokens. This is normal and necessary. But it creates a specific problem for multi-step research:

```
Step 1: Search for X → found A, B, C
Step 2: Read source A → discovered D
Step 3: Search for D → found E, F
Step 4: Read source E → discovered G
     ↓
  [context compaction happens here]
     ↓
Step 5: The AI no longer remembers steps 1-3, the session ID,
        or what gaps remain. Research stalls or restarts from zero.
```

This isn't a bug — it's a fundamental constraint of how LLMs work. The model literally cannot see information that was compacted away. Any tool that accumulates state across multiple calls (like a research session) must account for this.

### Why This Matters in Practice

Research on LLM tool use ([Patil et al., 2023](https://arxiv.org/abs/2305.15334)) showed that without proper grounding, models produce incorrect API calls as task complexity grows — they need external state to stay accurate. The MemGPT paper ([Packer et al., 2023](https://arxiv.org/abs/2310.08560)) demonstrated that explicitly paging state in and out — rather than relying on the context window to hold everything — produces dramatically better results on long-horizon tasks.

The practical impact: without persistence, any research session longer than ~8 steps risks losing accumulated findings when the context window fills up. For a literature review, competitive analysis, or patent landscape search, that can mean hours of wasted work.

---

## How We Solve It

Four mechanisms work together:

### 1. Every step is written to disk immediately

When the AI records a research step, it's persisted to encrypted disk within the same call — not buffered, not deferred. If the server crashes one millisecond after the call returns, the step is safe.

```
AI calls sequential_search(step 5)
  → Manager appends step to Session
  → Session written to disk (atomic: temp → fsync → rename)
  → In-memory index updated
  → Response returned to AI
```

The write is **atomic**: a temporary file is written, flushed to the physical disk, then renamed over the previous file in a single OS operation. This means the file on disk is always either the old valid state or the new valid state — never a half-written corrupt mess.

### 2. A lightweight index stays in memory for instant access

Loading the full session from disk on every read would be wasteful. Instead, we maintain a smaller index in memory for fast access:

| Field | In Memory (index) | On Disk (full session) |
|---|---|---|
| Research goal | ✓ | ✓ |
| Step count | ✓ | ✓ |
| Per-step summary | One-line (≤120 chars) | Full description + reasoning |
| Recent steps | Last 3 (full detail) | All steps (full detail) |
| Knowledge gaps | ✓ | ✓ |
| Source URLs + titles | ✓ | ✓ |
| Rejected approaches | — | ✓ (per step) |
| Timestamps | ✓ | ✓ |

Disk is the source of truth — it has everything. The in-memory index is a lightweight view rebuilt from disk on startup. The only data that requires a disk read to access is the full detail of older steps (beyond the last 3) and per-step rejected approaches.

The index is rebuilt from disk on server startup — so even a full restart (server update, machine reboot) doesn't lose sessions.

### 3. Sources are tracked server-side (not by the LLM)

When search tools (`web_search`, `scrape_page`, `search_and_scrape`, `news_search`, `academic_search`, `patent_search`) are called with a `sessionId` parameter, the server automatically records discovered URLs and titles as session sources. This happens server-side — no LLM relay needed.

Why this matters: if the LLM were responsible for reporting sources back to the session, it could hallucinate URLs, forget to record some, or lose them during compaction. By recording at the server level — where the actual API responses are — the source list is always accurate and complete.

Sources are deduplicated by URL (the same page found via multiple searches is stored once) and included in every session response and recovery output.

### 4. An explicit recovery tool pages state back in

After context compaction, the AI calls `get_research_session` with the session ID. This returns the index — enough context to understand where the research stands and what to do next, without flooding the (now-limited) context window with the full history.

```
[Context compacted — AI lost session state]
     ↓
AI calls get_research_session("session-id-here")
     ↓
Returns: goal, summary, step index, last 3 steps, open gaps, all source URLs
     ↓
AI continues research from where it left off
```

If the AI needs details about a specific earlier step, it can request just that one step by number — minimizing context usage.

---

## Design Decisions and Why

### Why explicit recovery instead of automatic injection?

We considered automatically injecting session state into every response. We rejected it because:

1. **Context budget**: Injecting full state into every call wastes tokens when the AI hasn't lost context yet (the common case for steps 1-8).
2. **Unpredictable compaction**: We can't know *when* compaction happens — it's controlled by the client, not the tool server. Injecting preemptively means guessing wrong most of the time.
3. **MemGPT principle**: Research shows that explicit retrieval (the AI decides when it needs state) outperforms implicit injection (the system always provides state). The model learns to ask when uncertain.

This follows the pattern established by MemGPT ([Packer et al., 2023](https://arxiv.org/abs/2310.08560)): give the model a tool to page state in, rather than trying to keep everything resident.

### Why a two-tier architecture (memory + disk)?

| Alternative | Why we didn't use it as the default |
|---|---|
| Memory only | Lost on restart. Unacceptable for 4-hour sessions. |
| Disk only | Too slow for reads — every `GetIndex` call would need I/O. |
| Database (Redis/SQLite) as the *default* | Adds deployment complexity for a tool that should be `go install && done`. (Redis is available as an **opt-in** backend — see below.) |
| Memory + lazy flush | Risk of data loss on crash. We chose correctness over throughput. |

The two-tier approach gives us: instant reads (memory), durability (disk), crash safety (atomic writes), and zero external dependencies.

**Opt-in distributed backend (multi-pod HTTP).** `session.Manager` is an interface (`internal/session/interface.go`). The default is the memory+disk `MemoryManager`; in HTTP mode, setting `REDIS_URL` swaps in a Redis-backed `session.Manager` (`internal/redisbackend/session.go`) so sessions survive pod restarts and are visible across pods — sticky sessions become optional. Redis values keep the same AES-256-GCM-at-rest guarantee. See [DEPLOYMENT.md → Horizontal Scaling](DEPLOYMENT.md#horizontal-scaling). When a follow-up step still misses (expired, or no Redis), the client gets a typed `session_not_found` error with a `recoveryHint` rather than a silently-forked session.

### Why AES-256-GCM encryption at rest?

Research sessions can contain sensitive queries — medical conditions, legal strategies, competitive intelligence, trade secrets. Even on a local disk, encryption at rest is a baseline expectation for:

- **Enterprise compliance** (SOC 2 CC6.1, FedRAMP SC-28)
- **Multi-tenant HTTP deployments** where sessions from different users share a filesystem
- **Defense in depth** — if the disk is compromised, session content remains protected

GCM mode provides both confidentiality (nobody can read it) and authenticity (nobody can tamper with it without detection). Each file gets a random 12-byte nonce, preventing identical sessions from producing identical ciphertext. Each blob also binds its key (a SHA-256 of the logical key, the same value used for the on-disk filename) as GCM additional authenticated data, so a ciphertext cannot be silently moved to a different key's file.

Encryption is optional — omit `CACHE_ENCRYPTION_KEY` and sessions are stored as plaintext. For single-user STDIO mode on a personal machine, this is a reasonable tradeoff.

### Why 4 hours idle TTL (not 30 minutes, not infinite)?

The TTL determines how long a session survives without activity before being cleaned up.

- **Too short** (30 min): A researcher takes a lunch break, comes back, session gone.
- **Too long** (24h+): Disk fills with abandoned sessions. Stale research misleads the AI if accidentally recovered.
- **Sliding window**: Every access (read or write) resets the in-memory timer. The disk expiry header is updated on writes only. A session actively being used never expires during runtime; after a server restart, the timer resumes from the last write.

Four hours accommodates:
- Context compaction + recovery (usually happens within minutes)
- Coffee breaks and interruptions
- Switching between tasks and coming back

The TTL is configurable via `SESSION_TTL` for organizations with different requirements.

### Why response mode switching at step 9?

For short sessions (1-8 steps), the step index plus recent steps fit comfortably in a context window. But at step 9+, even the index starts competing with the AI's working memory for the current task.

The automatic switch to summary mode at step 9 is based on empirical observation of Claude's context utilization during research tasks. Beyond 8 steps, the step index alone grows large enough that adding a synthesized summary helps the AI orient faster without reading every entry.

Summary mode returns:
- The research goal (what are we doing)
- A synthesized summary (where are we)
- One-line index of all steps (what happened)
- Last 3 full steps (recent context)
- Active gaps (what's left)
- All discovered source URLs (what we found)

This is typically 5-10 KB — enough to continue coherently without overwhelming the window.

The AI can override this with `responseMode: "full"` to skip the synthesized summary and receive the step index directly. To retrieve full details of a specific earlier step, the AI uses `get_research_session` with a `stepId`.

### Why per-tenant isolation?

In HTTP mode (shared server), sessions are keyed by `{tenantID}:{sessionID}`. Tenant A cannot access Tenant B's sessions — not by guessing session IDs, not by enumeration, not by accident.

In STDIO mode (single user), the tenant ID defaults to "default" — no isolation needed because there's only one user.

---

## For Contributors: Data Flow Reference

### Write Path (AppendStep)

```
1. Acquire mutex
2. Check index exists and TTL not expired
3. Check MaxSteps not exceeded
4. Load full Session from disk (decrypt if configured)
5. Append step, update timestamps
6. Write back to disk (encrypt → temp → fsync → rename)
7. Rebuild SessionIndex from Session
8. Update in-memory index
9. Release mutex
```

### Source Tracking Path (AddSources)

```
1. Acquire mutex
2. Check index exists
3. Load full Session from disk
4. Deduplicate new sources by URL against existing
5. Append new sources
6. Write back to disk
7. Rebuild index
8. Release mutex
```

Called automatically by search tools when `sessionId` is provided.

### Read Path (GetIndex)

```
1. Acquire mutex
2. Look up index by tenantID:sessionID
3. Check TTL not expired
4. Update LastUsed timestamp
5. Return index (no disk I/O)
6. Release mutex
```

### Recovery Path (get_research_session)

Without `stepId` — returns the index from memory (no disk I/O):
```
1. Acquire mutex
2. Look up index by tenantID:sessionID
3. Check TTL not expired
4. Update LastUsed timestamp
5. Return index (goal, summary, steps, gaps, sources)
6. Release mutex
```

With `stepId` — loads a specific step from disk:
```
1. Acquire mutex
2. Look up index (verify session exists and alive)
3. Load full Session from disk
4. Find step by number
5. Return single step with full detail
6. Release mutex
```

### Startup Path (Rebuild)

```
1. Scan data directory for .session files
2. For each file:
   a. Check 8-byte timestamp header (skip if expired)
   b. Decrypt payload
   c. Unmarshal JSON → Session
   d. Verify: sha256(tenantID:sessionID) matches filename
   e. Build index, populate maps
3. Remove corrupt/expired files
4. Start cleanup goroutine (every 15 min)
```

### Cleanup

A background goroutine runs every 15 minutes:
- Iterates all index entries
- Removes any where `now - LastUsed > SessionTTL`
- Deletes corresponding disk files

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SESSION_TTL` | `4h` | Idle timeout (resets on every read/write in memory; disk header updates on writes) |
| `SESSION_DATA_DIR` | `{CACHE_DIR}/sessions` | Where encrypted session files live |
| `SESSION_MAX_STEPS` | `200` | Max steps before session auto-completes |
| `CACHE_ENCRYPTION_KEY` | — | 64 hex chars for AES-256-GCM (omit for plaintext) |
| `CACHE_ENCRYPTION_KEY_PREV` | — | Optional 64-hex previous key for zero-downtime rotation. Sessions encrypted under the old key are decrypted via fallback and lazily re-encrypted with the current key on the next write |

---

## Comparison with Other Approaches

| Approach | Survives compaction? | Survives restart? | Privacy | Deployment complexity |
|---|---|---|---|---|
| **Rely on context window** | No | No | N/A | None |
| **System prompt injection** | Partially (fixed budget) | No | N/A | None |
| **External database (Postgres/etc.)** | Yes | Yes | Depends | High (requires infra) |
| **Cloud-hosted session service** | Yes | Yes | Low (3rd party sees data) | Medium |
| **web-researcher-mcp default (memory + encrypted disk)** | Yes | Yes | High (local, encrypted) | None (built-in) |
| **web-researcher-mcp + opt-in `REDIS_URL`** | Yes | Yes (+ across pods) | High (encrypted at rest) | Medium (operator runs Redis) |

---

## Further Reading

- [MemGPT: Towards LLMs as Operating Systems](https://arxiv.org/abs/2310.08560) — Packer et al., 2023 (UC Berkeley). The foundational paper on explicit memory management for LLMs — paging state in and out of a limited context window.
- [Generative Agents: Interactive Simulacra of Human Behavior](https://arxiv.org/abs/2304.03442) — Park et al., 2023 (Stanford / Google). Implements a two-tier memory architecture (full memory stream + retrieval index) to work around context limits — the closest architectural precedent to our design.
- [ReAct: Synergizing Reasoning and Acting in Language Models](https://arxiv.org/abs/2210.03629) — Yao et al., 2022 (Princeton / Google Brain). Establishes the reason-then-act loop that underpins our explicit retrieval pattern: the model reasons it needs state, then acts by calling the recovery tool.
- [Voyager: An Open-Ended Embodied Agent with Large Language Models](https://arxiv.org/abs/2305.16291) — Wang et al., 2023 (NVIDIA). Persistent skill library surviving across sessions using an indexed store — parallels our session persistence architecture (description index for fast lookup, full content loaded on demand).
- [Gorilla: Large Language Model Connected with Massive APIs](https://arxiv.org/abs/2305.15334) — Patil et al., 2023 (UC Berkeley). How retrieval augmentation improves LLM accuracy when calling external APIs — supporting our server-side source tracking over LLM self-reporting.
- [Lost in the Middle](https://arxiv.org/abs/2307.03172) — Liu et al., 2023 (Stanford). Why LLMs retrieve information better from the start and end of long contexts — supporting our choice to keep recovery responses compact rather than dumping full history.
- [MCP Specification — Session Management](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#session-management) — The Model Context Protocol's transport-level session lifecycle definition.
