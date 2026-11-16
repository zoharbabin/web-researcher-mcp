package content

import "strings"

// BotWallMaxBytes bounds how large extracted content can be and still be judged a
// bot-wall interstitial. A real article is far larger; an interstitial ("Checking
// your browser…", a CAPTCHA shell, or an Anubis PoW gate) is small. Set to 2048
// to cover Anubis (~1075 B) while remaining well below any real article body.
// Real article bodies, including claim-check fetches, are far larger, so there is no risk of false positives on real content.
const BotWallMaxBytes = 2048

// botWallMarkers are phrases that, in SHORT extracted content, indicate a bot/JS
// interstitial rather than the page itself. Covers Cloudflare, CAPTCHA shells, and
// Anubis/PoW proof-of-work gates (github.com/TecharoHQ/anubis) which return HTTP 200.
var botWallMarkers = []string{
	"checking your browser",
	"enable javascript and cookies to continue",
	"verify you are human",
	"verifying you are human",
	"please verify you are a human",
	"verify that you're not a robot", // CourtListener / Free Law Project interstitial
	"verify that you are not a robot",
	"are you a robot",
	"complete the security check",
	"ddos protection by",
	"attention required",
	"just a moment",       // Cloudflare interstitial title
	"prove your humanity", // Reddit challenge page, served with HTTP 200
	"cf-browser-verification",
	"please turn javascript on",
	"javascript is disabled", // bot/JS-wall shell that renders no real content
	"please enable javascript to view",
	// Anubis / PoW anti-AI-scraping gates (github.com/TecharoHQ/anubis).
	// These return HTTP 200 with a ~1075-byte interstitial — not detectable by
	// status code alone. All three phrases appear in the Anubis default template.
	"making sure you're not a bot",
	"protect the server against the scourge of",
	"anubis uses a proof-of-work scheme",
	"this is a placeholder solution",
	// Akamai/DataDome-style access block pages, served for a flagged device or
	// network (the wall WSJ shows an automated browser). Same short-content gate.
	"access is temporarily restricted",
	"unusual activity from your device or network",
	// Imperva/Incapsula bot-wall stub (a tiny HTML page with a script from
	// /_Incapsula_Resource, often served at a .pdf URL, #828).
	"_incapsula_resource",
	"incapsula incident",
}

// softGateMarkers are login, signup and brand-portal gates. They suit a scrape,
// where such a page is not the content. They do not suit a login window, where a
// login page is exactly what the user is meant to see.
var softGateMarkers = []string{
	// Frontify brand-portal login wall — returned with HTTP 200 when the portal
	// requires viewer credentials. go-rod's headless fingerprint triggers this
	// even on public portals; the pipeline must reject it and not cache it.
	"please enter your viewer credentials",
	"request access to the brand owner",
	// Soft signup/login/subscribe gates — a well-formed HTTP 200 page whose real
	// content is short and ends in a CTA instead of a hard CAPTCHA/JS challenge.
	// This is the anti-bot pattern Crunchbase/SimilarWeb-style sites use for
	// anonymous traffic (issue #661). These phrases are generic enough to appear
	// legitimately gate-like, but — like every marker above — only fire when
	// paired with the short-content gate (BotWallMaxBytes) below, never when a
	// phrase merely appears somewhere inside a long real article.
	"sign up to continue",
	"log in to continue",
	"subscribe to continue",
	"create a free account",
	"view full profile",
	"unlock full access",
	"sign up for free to view",
}

// frameChallengeMarkers match what a login window reports for a challenge page
// with no text of its own: the URL of its one frame. DataDome (WSJ) is a shell
// around a frame from this host. Scrapes never see frame URLs, so they skip it.
var frameChallengeMarkers = []string{
	"captcha-delivery.com",
}

// LooksLikeBotWall reports whether short extracted content is a bot/JS-wall
// interstitial that was returned with a 200 status. Such pages must be treated as
// blocked (ErrBlocked), not as a successful low-quality scrape — otherwise the
// placeholder text masquerades as the page's content. Only short content is
// considered, so a legitimate long article is never flagged.
func LooksLikeBotWall(text string) bool {
	return shortContains(text, botWallMarkers, softGateMarkers)
}

// LooksLikeAccessBlock reports whether the text of a page a person is looking at
// is a bot-block page. It leaves out login and signup gates, so an ordinary
// sign-in page is never taken for a block.
func LooksLikeAccessBlock(text string) bool {
	return shortContains(text, botWallMarkers, frameChallengeMarkers)
}

func shortContains(text string, lists ...[]string) bool {
	if len(text) > BotWallMaxBytes {
		return false
	}
	lower := strings.ToLower(text)
	for _, list := range lists {
		for _, m := range list {
			if strings.Contains(lower, m) {
				return true
			}
		}
	}
	return false
}
