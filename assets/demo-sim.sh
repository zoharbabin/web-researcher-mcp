#!/bin/bash
# Simulates Claude Code output for demo recording.
# Shows the FULL value loop: prompt -> tool calls -> synthesized answer.
# Usage: ./demo-sim.sh <scene>

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
PURPLE='\033[0;35m'
RED='\033[0;31m'
DIM='\033[2m'
BOLD='\033[1m'
RESET='\033[0m'
WHITE='\033[1;37m'
UNDERLINE='\033[4m'

type_prompt() {
  local text="$1"
  printf "${DIM}>${RESET} ${WHITE}"
  while IFS= read -r -n1 char; do
    printf '%s' "$char"
    sleep 0.03
  done <<< "$text"
  printf "${RESET}"
  echo
  echo
  sleep 0.5
}

stream_text() {
  local text="$1"
  local delay="${2:-0.012}"
  while IFS= read -r -n1 char; do
    printf '%s' "$char"
    sleep "$delay"
  done <<< "$text"
}

header() {
  local title="$1"
  echo
  echo -e "${PURPLE}*${RESET} ${BOLD}Claude Code${RESET}  ${DIM}with web-researcher-mcp${RESET}  ${DIM}|${RESET}  ${CYAN}${title}${RESET}"
  echo -e "${DIM}--------------------------------------------------------------------------${RESET}"
  echo
}

# --- Scene: news search ------------------------------------------------------

scene_news() {
  header "News Search"
  type_prompt "What are the most important AI developments announced today?"

  echo -e "${CYAN}⠋${RESET} ${DIM}Calling${RESET} news_search${DIM}...${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠙${RESET} ${DIM}Searching news (freshness: day, sort: relevance)${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠹${RESET} ${DIM}Filtering opinion/editorial content${RESET}"
  sleep 0.3
  echo -e "${GREEN}✓${RESET} ${DIM}5 articles found, 3 factual announcements selected${RESET}"
  echo
  sleep 0.4

  stream_text "Here are today's key AI developments:"
  echo
  echo
  sleep 0.2
  echo -e "  ${BOLD}1. New open-weight model achieves GPT-4 parity at 70B params${RESET}"
  echo -e "     ${DIM}TechCrunch • 2 hours ago${RESET}"
  stream_text "     Trained on 15T tokens with novel architecture. Weights released"
  echo
  stream_text "     under Apache 2.0. Runs on a single A100."
  echo
  echo
  sleep 0.2
  echo -e "  ${BOLD}2. EU AI Act enforcement begins for high-risk systems${RESET}"
  echo -e "     ${DIM}Reuters • 4 hours ago${RESET}"
  stream_text "     Companies must register deployments with national authorities."
  echo
  stream_text "     Penalties up to 7% of global revenue for non-compliance."
  echo
  echo
  sleep 0.2
  echo -e "  ${BOLD}3. Major cloud provider launches inference-optimized instances${RESET}"
  echo -e "     ${DIM}The Verge • 6 hours ago${RESET}"
  stream_text "     Custom silicon delivers 2.5x throughput for models above 70B"
  echo
  stream_text "     parameters. Available in all regions starting today."
  echo
  echo
  echo -e "${DIM}Source: news_search | Freshness: last 24h | Provider: Brave${RESET}"
}

# --- Scene: search_and_scrape with synthesis ---------------------------------

scene_research() {
  header "Search & Scrape"
  type_prompt "What are the key differences between Go's iter package and Python generators? Give me a comparison with code examples."

  echo -e "${CYAN}⠋${RESET} ${DIM}Calling${RESET} search_and_scrape${DIM}...${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠙${RESET} ${DIM}Searching: Go iter package vs Python generators${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠹${RESET} ${DIM}Scraping go.dev/blog/range-functions...${RESET} ${GREEN}Tier 1: markdown${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠸${RESET} ${DIM}Scraping pkg.go.dev/iter...${RESET} ${GREEN}Tier 1: markdown${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠼${RESET} ${DIM}Scraping docs.python.org/3/howto/functional.html...${RESET} ${YELLOW}Tier 2: stealth HTTP${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠴${RESET} ${DIM}Deduplicating content (removed 6 similar paragraphs)${RESET}"
  sleep 0.2
  echo -e "${CYAN}⠦${RESET} ${DIM}Scoring sources: relevance, authority, freshness${RESET}"
  sleep 0.2
  echo -e "${GREEN}✓${RESET} ${DIM}3 sources extracted in 1.8s (6,240 tokens)${RESET}"
  echo
  sleep 0.4

  echo -e "Based on my research across three authoritative sources:"
  echo
  sleep 0.2
  stream_text "**Go's iter package** (Go 1.23+) introduces range-over-func, which lets"
  echo
  stream_text "you write iterator functions that work with \`for range\` loops:"
  echo
  echo
  sleep 0.2
  echo -e "${DIM}  func Backward[E any](s []E) iter.Seq[E] {"
  echo -e "      return func(yield func(E) bool) {"
  echo -e "          for i := len(s)-1; i >= 0; i-- {"
  echo -e "              if !yield(s[i]) { return }"
  echo -e "          }"
  echo -e "      }"
  echo -e "  }${RESET}"
  echo
  sleep 0.2
  stream_text "**Key differences from Python generators:**"
  echo
  echo
  sleep 0.1
  stream_text "  1. Go uses push-based iteration (caller provides yield func)"
  echo
  stream_text "     Python uses pull-based (caller calls next())"
  echo
  stream_text "  2. Go iterators are just functions — no special syntax needed"
  echo
  stream_text "     Python requires yield keyword and generator protocol"
  echo
  stream_text "  3. Go iter.Seq is composable via standard function composition"
  echo
  stream_text "     Python uses itertools or chained comprehensions"
  echo
  echo
  sleep 0.2
  echo -e "${DIM}Sources: go.dev/blog/range-functions (0.94), pkg.go.dev/iter (0.91),${RESET}"
  echo -e "${DIM}docs.python.org/3/howto/functional.html (0.87)${RESET}"
}

# --- Scene: search lenses ----------------------------------------------------

scene_lenses() {
  header "Search Lenses"
  type_prompt "Using the medical lens, find evidence-based guidelines for treating type 2 diabetes with GLP-1 agonists in elderly patients."

  echo -e "${CYAN}⠋${RESET} ${DIM}Calling${RESET} web_search ${YELLOW}lens=medical${RESET}${DIM}...${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠙${RESET} ${DIM}Restricting to: pubmed.ncbi.nlm.nih.gov, who.int, nejm.org,${RESET}"
  echo -e "  ${DIM}cochranelibrary.com, nature.com/nm, diabetes.org...${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠹${RESET} ${DIM}Injecting site: operators (12 medical domains)${RESET}"
  sleep 0.3
  echo -e "${GREEN}✓${RESET} ${DIM}4 results from peer-reviewed sources only${RESET}"
  echo
  sleep 0.4

  stream_text "Based on current clinical guidelines from peer-reviewed sources:"
  echo
  echo
  sleep 0.2
  stream_text "**GLP-1 RAs in elderly T2D patients — Key recommendations:**"
  echo
  echo
  sleep 0.1
  stream_text "  • Start low, titrate slowly — GI side effects more pronounced"
  echo
  stream_text "    in patients >65 (ADA Standards of Care, Section 13)"
  echo
  stream_text "  • Preferred over sulfonylureas due to lower hypoglycemia risk"
  echo
  stream_text "  • Semaglutide and dulaglutide have cardiovascular benefit data"
  echo
  stream_text "    specifically in older populations (SUSTAIN-6, REWIND trials)"
  echo
  stream_text "  • Monitor renal function — dose adjustment needed at eGFR <30"
  echo
  stream_text "  • Weight loss benefit must be balanced against sarcopenia risk"
  echo
  echo
  sleep 0.2
  echo -e "${DIM}Sources: diabetes.org/standards (ADA), pubmed.ncbi.nlm.nih.gov,${RESET}"
  echo -e "${DIM}nejm.org/doi/10.1056/NEJMoa2032183${RESET}"
  echo
  echo -e "${YELLOW}Note:${RESET} ${DIM}Medical lens filtered out 47 non-authoritative results (blogs,${RESET}"
  echo -e "${DIM}supplements sites, forums) — only peer-reviewed sources shown.${RESET}"
}

# --- Scene: patent search (with real providers) ------------------------------

scene_patents() {
  header "Patent Search"
  type_prompt "Find Apple's recent patents related to on-device LLM inference. Focus on US patents from 2024 onward."

  echo -e "${CYAN}⠋${RESET} ${DIM}Calling${RESET} patent_search${DIM}...${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠙${RESET} ${DIM}Generating assignee variations: Apple, Apple Inc, Apple Inc.${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠹${RESET} ${DIM}Querying USPTO API (office: US)${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠸${RESET} ${DIM}Cross-referencing EPO OPS for patent family data${RESET}"
  sleep 0.4
  echo -e "${CYAN}⠼${RESET} ${DIM}Filtering by CPC: G06N (neural networks)${RESET}"
  sleep 0.3
  echo -e "${GREEN}✓${RESET} ${DIM}5 patents found via USPTO + EPO (circuit breakers: all healthy)${RESET}"
  echo
  sleep 0.4

  stream_text "Here are Apple's recent US patents on on-device LLM inference:"
  echo
  echo
  sleep 0.2
  echo -e "  ${BOLD}US-2025-0142893${RESET} — Efficient Transformer Inference on Mobile Devices"
  echo -e "  ${DIM}Filed: Mar 2024 | Granted: Jan 2025${RESET}"
  echo -e "  Describes a quantization-aware training method for deploying"
  echo -e "  large language models under 4GB memory constraints."
  echo
  sleep 0.2
  echo -e "  ${BOLD}US-2025-0298741${RESET} — Speculative Decoding for On-Device Language Models"
  echo -e "  ${DIM}Filed: Jun 2024 | Granted: Apr 2025${RESET}"
  echo -e "  Uses a small draft model to predict token sequences, verified"
  echo -e "  by the larger model — 3x faster inference on Apple Silicon."
  echo
  sleep 0.2
  echo -e "  ${BOLD}US-2026-0051283${RESET} — Adaptive Context Window Management"
  echo -e "  ${DIM}Filed: Sep 2024 | Published: Feb 2026${RESET}"
  echo -e "  Dynamic attention pruning that maintains quality while reducing"
  echo -e "  KV-cache memory by 60% for long conversations."
  echo
  sleep 0.2
  stream_text "**Key insight:** Apple is building a full on-device inference stack —"
  echo
  stream_text "quantization, speculative decoding, and memory management — likely"
  echo
  stream_text "targeting offline operation for their next AI update."
  echo
  echo
  echo -e "${DIM}Providers: USPTO API + EPO OPS | Assignee: Apple Inc | Office: US${RESET}"
}

# --- Scene: intelligent scraping with tier escalation ------------------------

scene_scrape() {
  header "Smart Scraping Pipeline"
  type_prompt "Get the abstract and citation info from this arxiv paper: arxiv.org/abs/2401.04088"

  echo -e "${CYAN}⠋${RESET} ${DIM}Calling${RESET} scrape_page${DIM}...${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠙${RESET} ${DIM}URL: arxiv.org/abs/2401.04088${RESET}"
  sleep 0.3
  echo -e "  ${DIM}|-${RESET} ${DIM}Tier 1: markdown negotiation...${RESET} ${RED}✗ not supported${RESET}"
  sleep 0.3
  echo -e "  ${DIM}|-${RESET} ${DIM}Tier 2: stealth HTTP...${RESET} ${GREEN}✓ content extracted${RESET} ${DIM}(340ms)${RESET}"
  sleep 0.2
  echo -e "  ${DIM}|-${RESET} ${DIM}Parsed with goquery: article > .abstract${RESET}"
  sleep 0.2
  echo -e "  ${DIM}|-${RESET} ${DIM}Extracted 1,847 tokens | Quality: high (82% text ratio)${RESET}"
  sleep 0.2
  echo -e "  ${DIM}\-${RESET} ${DIM}Auto-generated citation (APA + MLA)${RESET}"
  echo
  echo -e "${GREEN}✓${RESET} ${DIM}Extracted in 340ms via Tier 2 (no browser needed)${RESET}"
  echo
  sleep 0.4

  stream_text "**Mixtral of Experts** (Jiang et al., 2024)"
  echo
  echo
  sleep 0.2
  stream_text "Abstract: We introduce Mixtral 8x7B, a Sparse Mixture of Experts"
  echo
  stream_text "language model. Mixtral has the same architecture as Mistral 7B with"
  echo
  stream_text "the difference that each layer uses 8 feedforward blocks (experts)."
  echo
  stream_text "A router network selects two experts per token at each layer. This"
  echo
  stream_text "allows the model to use 47B total parameters while only activating"
  echo
  stream_text "13B per token, achieving 6x faster inference than a dense 47B model."
  echo
  echo
  sleep 0.2
  echo -e "${DIM}Citation (APA):${RESET}"
  echo -e "  Jiang, A. Q., et al. (2024). Mixtral of Experts."
  echo -e "  ${DIM}arXiv preprint arXiv:2401.04088.${RESET}"
  echo
  echo -e "${DIM}Extraction: Tier 2 (stealth HTTP) | 4-tier pipeline: markdown -> stealth -> HTML -> browser${RESET}"
}

# --- Scene: sequential multi-step research -----------------------------------

scene_sequential() {
  header "Sequential Research"
  type_prompt "I need a comprehensive comparison of vector databases for production RAG. Compare Pinecone, Weaviate, and Qdrant on cost, performance, and hybrid search."

  echo -e "${CYAN}⠋${RESET} ${DIM}Calling${RESET} sequential_search${DIM}...${RESET}"
  sleep 0.3
  echo -e "${CYAN}⠙${RESET} ${DIM}Step 1/4: Searching \"vector database benchmark 2025\"${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠹${RESET} ${DIM}Step 2/4: Searching \"Pinecone vs Weaviate vs Qdrant pricing\"${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠸${RESET} ${DIM}Step 3/4: Searching \"hybrid search BM25 vector reranking\"${RESET}"
  sleep 0.5
  echo -e "${CYAN}⠼${RESET} ${DIM}Step 4/4: Scraping benchmark data from ann-benchmarks.com${RESET}"
  sleep 0.4
  echo -e "${CYAN}⠴${RESET} ${DIM}Cross-referencing 12 sources, deduplicating...${RESET}"
  sleep 0.3
  echo -e "${GREEN}✓${RESET} ${DIM}4 steps completed | 12 sources | Session stored for follow-up${RESET}"
  echo
  sleep 0.4

  stream_text "## Vector Database Comparison for Production RAG"
  echo
  echo
  sleep 0.2
  echo -e "  ${BOLD}              Pinecone      Weaviate       Qdrant${RESET}"
  echo -e "  ${DIM}-------------------------------------------------------------${RESET}"
  echo -e "  Hosting      Managed only  Self/Cloud     Self/Cloud"
  echo -e "  Hybrid       ✓ (sparse)    ✓ (BM25+vec)   ✓ (sparse+dense)"
  echo -e "  Latency p99  <50ms         ~80ms          ~40ms"
  echo -e "  Free tier    Starter       Sandbox        1GB cloud"
  echo -e "  Reranking    Built-in      Module         Custom"
  echo
  sleep 0.3
  stream_text "**Recommendation for production RAG:**"
  echo
  echo
  stream_text "  • Budget-sensitive self-hosted -> Qdrant (Rust, low resource usage)"
  echo
  stream_text "  • Managed + fastest time-to-prod -> Pinecone (no ops overhead)"
  echo
  stream_text "  • Need BM25 + vector in one query -> Weaviate (native hybrid)"
  echo
  echo
  sleep 0.2
  echo -e "${DIM}Research session active — ask follow-up questions to dig deeper.${RESET}"
  echo -e "${DIM}Sources: ann-benchmarks.com, vendor docs, StackOverflow benchmarks${RESET}"
}

case "$1" in
  news) scene_news ;;
  research) scene_research ;;
  lenses) scene_lenses ;;
  patents) scene_patents ;;
  scrape) scene_scrape ;;
  sequential) scene_sequential ;;
  *) echo "Usage: $0 {news|research|lenses|patents|scrape|sequential}" ;;
esac
