# Search Provider Setup

This guide covers how to obtain API keys for each supported search provider. You only need credentials for the providers you plan to use — configure one for single-provider mode, or multiple for multi-provider routing with automatic fallback.

---

## Google Custom Search (Programmable Search Engine)

**Free tier**: 100 queries/day (paid: $5 per 1,000 queries)

### Step 1: Get an API Key

1. Go to the [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project (or select an existing one)
3. Navigate to **APIs & Services > Library**
4. Search for "Custom Search API" and enable it
5. Go to **APIs & Services > Credentials**
6. Click **Create Credentials > API Key**
7. Copy the key

### Step 2: Create a Programmable Search Engine

1. Go to [Programmable Search Engine](https://programmablesearchengine.google.com/)
2. Click **Add** to create a new search engine
3. Under "What to search", select **Search the entire web**
4. Give it a name and click **Create**
5. Copy the **Search Engine ID** (cx)

### Step 3: Configure

```bash
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIzaSy...your-key
export GOOGLE_CUSTOM_SEARCH_ID=017576662512468239146:omuauf_gy1x
```

---

## Brave Search

**Free tier**: 2,000 queries/month (paid plans available)

### Step 1: Get an API Key

1. Go to [Brave Search API](https://brave.com/search/api/)
2. Click **Get Started** and create an account
3. Subscribe to the **Free** plan (or a paid plan for higher volume)
4. Navigate to your dashboard and copy your API key

### Step 2: Configure

```bash
export BRAVE_API_KEY=BSAxxxxxxxxxxxxxxxxxx
```

To use Brave as your primary (or only) provider:

```bash
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=BSAxxxxxxxxxxxxxxxxxx
```

---

## Serper.dev

**Free tier**: 2,500 queries (one-time credit, then paid)

### Step 1: Get an API Key

1. Go to [Serper.dev](https://serper.dev/)
2. Sign up for an account
3. Your API key is shown on the dashboard immediately after sign-up
4. Copy the key

### Step 2: Configure

```bash
export SERPER_API_KEY=your-serper-key
```

To use Serper as your primary provider:

```bash
export SEARCH_PROVIDER=serper
export SERPER_API_KEY=your-serper-key
```

---

## SearXNG (Self-Hosted)

**Free**: Open source, self-hosted — no API key needed, no query limits

SearXNG is a privacy-respecting metasearch engine that aggregates results from multiple sources. Ideal for air-gapped deployments or organizations that need full control over search infrastructure.

### Step 1: Run SearXNG

The fastest way is Docker:

```bash
docker run -d --name searxng \
  -p 8080:8080 \
  -e SEARXNG_SECRET=your-secret-key \
  searxng/searxng:latest
```

For production deployments, see the [SearXNG documentation](https://docs.searxng.org/) for configuration options (engine selection, rate limiting, result ranking).

### Step 2: Enable JSON API

SearXNG needs the JSON format enabled. Create or edit `settings.yml`:

```yaml
search:
  formats:
    - html
    - json
```

### Step 3: Configure

```bash
export SEARCH_PROVIDER=searxng
export SEARXNG_URL=http://localhost:8080
```

---

## SearchAPI.io

**Free tier**: 100 searches/month (paid plans available)

### Step 1: Get an API Key

1. Go to [SearchAPI.io](https://www.searchapi.io/)
2. Sign up for an account
3. Navigate to your dashboard
4. Copy your API key

### Step 2: Configure

```bash
export SEARCHAPI_API_KEY=your-searchapi-key
```

To use SearchAPI.io as your primary provider:

```bash
export SEARCH_PROVIDER=searchapi
export SEARCHAPI_API_KEY=your-searchapi-key
```

---

## Multi-Provider Routing

For maximum reliability, configure multiple providers and let the server route automatically with fallback:

```bash
# All providers configured
export BRAVE_API_KEY=BSA...
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...
export GOOGLE_CUSTOM_SEARCH_ID=017...
export SERPER_API_KEY=...

# Priority-ordered routing with automatic failover
export SEARCH_ROUTING=brave,google,serper
```

If Brave is down or rate-limited, requests automatically fall to Google, then Serper. Each provider has an independent circuit breaker.

For per-operation routing (different providers for different search types):

```bash
export SEARCH_ROUTING='{"web":"brave,google","news":"brave,serper","images":"google,brave","default":"brave,google,searchapi"}'
```

See [docs/DEPLOYMENT.md](DEPLOYMENT.md) for advanced routing configuration.

---

## Choosing a Provider

| Provider | Best For | Limitations |
|----------|----------|-------------|
| **Brave** | High-volume whole-web search, privacy | Newer service, smaller index |
| **Google PSE** | Broadest index, image search, custom PSE engines | 100/day free, slower for news |
| **Serper** | Google-identical results without PSE setup | One-time free credit only |
| **SearXNG** | Air-gapped/private deployments, no vendor lock-in | Requires self-hosting |
| **SearchAPI.io** | Multiple engine backends via unified API | Smaller free tier |

**Recommendation**: Start with Brave (generous free tier, fast) and add Google as a fallback. Use `SEARCH_ROUTING=brave,google` for the best balance of speed and coverage.
