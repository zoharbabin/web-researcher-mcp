package content

import (
	"strings"
	"testing"
)

// TestLooksLikeBotWall: short interstitial content is detected; a long real
// article that merely mentions a marker word is NOT (size-bounded guard).
func TestLooksLikeBotWall(t *testing.T) {
	t.Parallel()
	botWalls := []string{
		"Checking your browser before accessing example.com",
		"Please verify you are a human by completing the security check.",
		"Just a moment... cf-browser-verification",
		"Enable JavaScript and cookies to continue",
		"Please verify that you're not a robot to continue.", // CourtListener-style
		"JavaScript is disabled in your browser.",
		// Frontify brand-portal login wall (returned with HTTP 200 when go-rod
		// is fingerprinted as a bot on public portals).
		"Please enter your viewer credentials or request access to the Brand Owner.",
		"request access to the brand owner",
		// Reddit challenge page, HTTP 200 (live search_and_scrape finding).
		"Prove your humanity\n\nWe’re committed to safety and security. But not for bots. Complete the challenge below and let us know you’re a real person.\n\nReddit, Inc. © \"2026\". All rights reserved.\nUser Agreement\nPrivacy Policy\nContent Policy\nHelp",
	}
	for _, c := range botWalls {
		if !LooksLikeBotWall(c) {
			t.Errorf("expected bot-wall detection for %q", c)
		}
	}
	// A long article that happens to contain "captcha" must NOT be flagged.
	long := "This paper studies CAPTCHA usability. " + strings.Repeat("Real article body discussing the methodology and results in depth. ", 30)
	if LooksLikeBotWall(long) {
		t.Error("a long article mentioning captcha must not be classified as a bot-wall")
	}
	// Empty / ordinary short content is not a bot-wall.
	if LooksLikeBotWall("Welcome to my homepage.") {
		t.Error("ordinary short content must not be a bot-wall")
	}
}

// TestLooksLikeBotWall_Anubis: regression guard for GitHub issue #263.
// Anubis (github.com/TecharoHQ/anubis) returns HTTP 200 with a ~1075-byte PoW
// interstitial. Before the fix, its phrases were absent from botWallMarkers AND
// BotWallMaxBytes=600 was smaller than the Anubis body, so both guards failed.
func TestLooksLikeBotWall_Anubis(t *testing.T) {
	t.Parallel()

	// Representative Anubis interstitial body (~1075 bytes in production).
	// Contains the three canonical Anubis template phrases.
	anubisBody := `Making sure you're not a bot!
Anubis is checking to make sure that you are actually a human,
and to protect the server against the scourge of AI companies
that scrape sites without regard for the wishes of the site owners.
Anubis uses a Proof-of-Work scheme in the vein of Hashcash,
a proposed proof-of-work scheme for reducing email spam.
This is a placeholder solution so that more time can be spent
on building better solutions to this problem.`

	if !LooksLikeBotWall(anubisBody) {
		t.Error("Anubis PoW interstitial must be detected as a bot-wall")
	}

	// Each Anubis marker must trigger independently on a minimal short string.
	anubisMarkers := []string{
		"Making sure you're not a bot!",
		"protect the server against the scourge of AI companies",
		"Anubis uses a proof-of-work scheme in the vein of Hashcash",
		"This is a placeholder solution so that more time can be spent",
	}
	for _, m := range anubisMarkers {
		if !LooksLikeBotWall(m) {
			t.Errorf("Anubis marker must be detected as bot-wall: %q", m)
		}
	}

	// A legitimate academic article ABOUT proof-of-work / anti-scraping that is
	// long enough (> BotWallMaxBytes) must NOT be flagged — size gate must hold.
	longPoWArticle := "We study proof-of-work schemes for spam prevention. " +
		"Anubis uses a proof-of-work scheme in the vein of Hashcash. " +
		strings.Repeat("The methodology examines computational hardness assumptions and the trade-off between verifier cost and prover work in distributed systems. ", 20)
	if LooksLikeBotWall(longPoWArticle) {
		t.Errorf("a long article about PoW (len=%d) must not be flagged as a bot-wall", len(longPoWArticle))
	}
}

// TestLooksLikeBotWall_SoftGate: regression guard for GitHub issue #661.
// Crunchbase/SimilarWeb-style anti-bot pages return HTTP 200 with a short,
// well-formed page whose real content ends in a login/signup/subscribe CTA
// instead of a hard CAPTCHA/JS-challenge interstitial. These must be detected
// as a bot-wall when short, but a long real article that merely mentions one
// of the same phrases in passing (e.g. a footer CTA) must NOT be flagged.
func TestLooksLikeBotWall_SoftGate(t *testing.T) {
	t.Parallel()

	// Short, gate-dominated content — the entire extracted page IS the gate.
	softGates := []string{
		"Create a free account to see this company's full profile.",
		"Sign up to continue reading this report.",
		"Log in to continue viewing SimilarWeb traffic estimates.",
		"Subscribe to continue reading this article.",
		"View full profile: sign up for free to view detailed company data.",
		"Unlock full access to this report by creating an account.",
		"Sign up for free to view the complete traffic analysis.",
	}
	for _, c := range softGates {
		if !LooksLikeBotWall(c) {
			t.Errorf("expected soft-gate bot-wall detection for %q", c)
		}
	}

	// Negative: a long, real article that happens to contain one of the new
	// soft-gate phrases once, in a footer-like trailing sentence, must NOT be
	// flagged — the size gate (BotWallMaxBytes) must hold exactly as it does
	// for the pre-existing hard-challenge markers.
	longArticleWithFooterCTA := strings.Repeat(
		"This in-depth analysis covers the company's market position, funding history, and competitive landscape in detail. ", 30,
	) + "Sign up to continue receiving our newsletter with more analysis like this."
	if LooksLikeBotWall(longArticleWithFooterCTA) {
		t.Errorf("a long real article (len=%d) mentioning a soft-gate phrase in a footer must not be flagged as a bot-wall", len(longArticleWithFooterCTA))
	}

	// Ordinary short content that doesn't match any marker is not a bot-wall.
	if LooksLikeBotWall("Welcome to our company page. We build great products.") {
		t.Error("ordinary short content must not be a soft-gate bot-wall")
	}
}

func TestLooksLikeBotWall_Incapsula(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		"Request unsuccessful. Incapsula incident ID: 123-456",
		`<script src="/_Incapsula_Resource?x=1"></script>`,
	} {
		if !LooksLikeBotWall(c) {
			t.Errorf("LooksLikeBotWall(%q) = false, want true", c)
		}
	}
}

// TestLooksLikeBotWall_AccessRestricted: the block page a publisher serves a
// flagged device or network is a bot-wall, not content. The text is the page
// WSJ showed an automated login window (2026-11-14).
func TestLooksLikeBotWall_AccessRestricted(t *testing.T) {
	t.Parallel()
	wsjBlock := "Access is temporarily restricted\n\nWe detected unusual activity from your device or network.\n\n" +
		"Reasons may include:\n\nRapid taps or clicks\nJavaScript disabled or not working\n" +
		"Automated (bot) activity on your network\nUse of developer or inspection tools\n\nNeed help?\nSubmit feedback."
	for _, c := range []string{
		wsjBlock,
		"Access is temporarily restricted",
		"We detected unusual activity from your device or network.",
	} {
		if !LooksLikeBotWall(c) {
			t.Errorf("LooksLikeBotWall(%q) = false, want true", c)
		}
	}
	// A long article that quotes the phrase is real content, not a wall.
	long := "Readers who see the message \"Access is temporarily restricted\" are on a flagged network. " +
		strings.Repeat("The report examines how publishers detect automated traffic and what readers can do. ", 40)
	if LooksLikeBotWall(long) {
		t.Errorf("a long article quoting the phrase (len=%d) must not be flagged", len(long))
	}
}

// TestLooksLikeAccessBlock: hard blocks count, login and signup gates do not,
// and a DataDome shell is seen through its frame URL.
func TestLooksLikeAccessBlock(t *testing.T) {
	t.Parallel()
	blocks := []string{
		"Access is temporarily restricted\nWe detected unusual activity from your device or network.",
		"Checking your browser before accessing example.com",
		"https://geo.captcha-delivery.com/captcha/?initialCid=abc",
	}
	for _, c := range blocks {
		if !LooksLikeAccessBlock(c) {
			t.Errorf("expected access block for %q", c)
		}
	}
	notBlocks := []string{
		"Log in to continue\nEmail\nPassword",
		"Sign in\nCreate a free account",
		"Sign up to continue",
		"Please enter your viewer credentials or request access to the Brand Owner.",
		"",
	}
	for _, c := range notBlocks {
		if LooksLikeAccessBlock(c) {
			t.Errorf("login page %q must not be an access block", c)
		}
	}
	// A scrape never sees frame URLs, so a short page naming the host stays a page.
	if LooksLikeBotWall("tracker list: geo.captcha-delivery.com") {
		t.Error("the frame-URL marker must not apply to scraped content")
	}
	// Soft gates still stop a scrape.
	if !LooksLikeBotWall("Log in to continue") {
		t.Error("a short login gate must still be a bot wall for scrapes")
	}
}
