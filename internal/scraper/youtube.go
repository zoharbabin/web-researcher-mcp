package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	videoIDRegex    = regexp.MustCompile(`(?:v=|youtu\.be/|embed/|shorts/|live/|/v/)([a-zA-Z0-9_-]{11})`)
	playerRespRegex = regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*(\{.+?\})\s*;`)
	playerRespAlt   = regexp.MustCompile(`var\s+ytInitialPlayerResponse\s*=\s*(\{.+?\})\s*;`)
	descriptionRe   = regexp.MustCompile(`"shortDescription"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	// vttTagRe strips every WebVTT cue-text tag: inline timing (<00:00:00.000>),
	// voice (<v Speaker>), and styling (<c>, <c.className>, <b>, <i>, ...). YouTube's
	// auto-generated captions interleave <c>...</c> word-role tags with timing tags,
	// so both must be stripped or literal tag markup leaks into the transcript text.
	vttTagRe = regexp.MustCompile(`</?[a-zA-Z][^>]*>|<\d{2}:\d{2}:\d{2}\.\d+>`)
)

const (
	maxHighlights        = 5
	minHighlightSegments = 5
)

// TranscriptHighlight is a top-scored transcript segment returned by
// scoreTranscriptHighlights. Score is in [0,1]; StartTime uses "[M:SS]" format.
type TranscriptHighlight struct {
	Text      string  `json:"text"`
	Score     float64 `json:"score"`
	StartTime string  `json:"startTime,omitempty"`
}

func (p *Pipeline) scrapeYouTube(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	videoID := extractVideoID(rawURL)
	if videoID == "" {
		return nil, fmt.Errorf("cannot extract video ID from %s", rawURL)
	}

	watchURL := "https://www.youtube.com/watch?v=" + videoID
	req, err := http.NewRequestWithContext(ctx, "GET", watchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(rawURL, "youtube", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, classifyHTTPStatus(resp.StatusCode, rawURL, "youtube")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, err
	}

	pageHTML := string(body)
	title := extractYouTubeTitle(pageHTML)

	// Strategy 1: Extract transcript from player response captions
	transcript, err := extractTranscript(ctx, p.client, pageHTML)
	if err == nil && len(transcript) > 100 {
		highlights := scoreTranscriptHighlights(transcript, "")
		content := transcript
		if len(content) > maxLength {
			content = truncateBytes(content, maxLength)
		}
		return &ScrapeResult{
			URL:         rawURL,
			Content:     content,
			ContentType: "youtube",
			Title:       title,
			Highlights:  highlights,
		}, nil
	}

	// Strategy 2: Direct timedtext API
	transcript, err = fetchTimedTextAPI(ctx, p.client, videoID)
	if err == nil && len(transcript) > 100 {
		highlights := scoreTranscriptHighlights(transcript, "")
		content := transcript
		if len(content) > maxLength {
			content = truncateBytes(content, maxLength)
		}
		return &ScrapeResult{
			URL:         rawURL,
			Content:     content,
			ContentType: "youtube",
			Title:       title,
			Highlights:  highlights,
		}, nil
	}

	// Strategy 3: Fall back to video description
	description := extractDescription(pageHTML)
	if description != "" {
		content := fmt.Sprintf("[Video: %s]\n\n%s", title, description)
		if len(content) > maxLength {
			content = truncateBytes(content, maxLength)
		}
		return &ScrapeResult{
			URL:         rawURL,
			Content:     content,
			ContentType: "youtube",
			Title:       title,
		}, nil
	}

	return nil, fmt.Errorf("no transcript or description available for %s", videoID)
}

func extractVideoID(rawURL string) string {
	matches := videoIDRegex.FindStringSubmatch(rawURL)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func extractYouTubeTitle(html string) string {
	titleRegex := regexp.MustCompile(`<title>(.+?)</title>`)
	matches := titleRegex.FindStringSubmatch(html)
	if len(matches) >= 2 {
		title := matches[1]
		title = strings.TrimSuffix(title, " - YouTube")
		return strings.TrimSpace(title)
	}
	return ""
}

func extractTranscript(ctx context.Context, client *http.Client, pageHTML string) (string, error) {
	// Try primary regex
	matches := playerRespRegex.FindStringSubmatch(pageHTML)
	if len(matches) < 2 {
		// Try alternate regex pattern
		matches = playerRespAlt.FindStringSubmatch(pageHTML)
	}
	if len(matches) < 2 {
		return "", fmt.Errorf("player response not found")
	}

	var playerResp map[string]any
	if err := json.Unmarshal([]byte(matches[1]), &playerResp); err != nil {
		return "", fmt.Errorf("failed to parse player response: %w", err)
	}

	captionURL := findCaptionURL(playerResp)
	if captionURL == "" {
		return "", fmt.Errorf("no captions found")
	}
	if strings.Contains(captionURL, "?") {
		captionURL += "&fmt=vtt"
	} else {
		captionURL += "?fmt=vtt"
	}

	req, err := http.NewRequestWithContext(ctx, "GET", captionURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("caption fetch returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", err
	}

	return parseTranscriptVTT(string(body)), nil
}

func fetchTimedTextAPI(ctx context.Context, client *http.Client, videoID string) (string, error) {
	languages := []string{"en", "en-US", "en-GB"}

	for _, lang := range languages {
		apiURL := fmt.Sprintf("https://www.youtube.com/api/timedtext?v=%s&lang=%s&fmt=vtt",
			url.QueryEscape(videoID), url.QueryEscape(lang))

		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		_ = resp.Body.Close()
		if err != nil {
			continue
		}

		if resp.StatusCode == 200 && len(body) > 50 {
			text := parseTranscriptVTT(string(body))
			if len(text) > 100 {
				return text, nil
			}
		}
	}

	return "", fmt.Errorf("timedtext API returned no transcript")
}

func extractDescription(pageHTML string) string {
	matches := descriptionRe.FindStringSubmatch(pageHTML)
	if len(matches) < 2 {
		return ""
	}

	desc := matches[1]
	desc = strings.ReplaceAll(desc, "\\n", "\n")
	desc = strings.ReplaceAll(desc, "\\\"", "\"")
	desc = strings.ReplaceAll(desc, "\\/", "/")
	desc = strings.ReplaceAll(desc, "\\\\", "\\")
	desc = strings.TrimSpace(desc)

	if len(desc) < 20 {
		return ""
	}
	return desc
}

func findCaptionURL(playerResp map[string]any) string {
	captions, ok := playerResp["captions"].(map[string]any)
	if !ok {
		return ""
	}

	renderer, ok := captions["playerCaptionsTracklistRenderer"].(map[string]any)
	if !ok {
		return ""
	}

	tracks, ok := renderer["captionTracks"].([]any)
	if !ok || len(tracks) == 0 {
		return ""
	}

	// Prefer English
	for _, t := range tracks {
		track, ok := t.(map[string]any)
		if !ok {
			continue
		}
		langCode, _ := track["languageCode"].(string)
		if langCode == "en" || strings.HasPrefix(langCode, "en-") {
			u, _ := track["baseUrl"].(string)
			return u
		}
	}

	// Fall back to first track
	track, ok := tracks[0].(map[string]any)
	if !ok {
		return ""
	}
	u, _ := track["baseUrl"].(string)
	return u
}

// parseTranscriptVTT extracts clean plain text from a WebVTT transcript.
// Skips the WEBVTT header, blank lines, and timestamp lines ("HH:MM:SS.mmm --> ...").
// Inline cue tags (timing, <c>, <v>, etc.) are stripped from text lines. Each cue is
// prefixed with its start timestamp in "[M:SS]" format.
func parseTranscriptVTT(vtt string) string {
	lines := strings.Split(vtt, "\n")

	var parts []string
	var textLines []string
	var currentTimestamp string

	flush := func() {
		if len(textLines) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(textLines, " "))
		if text != "" {
			if currentTimestamp != "" {
				parts = append(parts, fmt.Sprintf("[%s] %s", currentTimestamp, text))
			} else {
				parts = append(parts, text)
			}
		}
		textLines = nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))

		if line == "" {
			flush()
			currentTimestamp = ""
			continue
		}
		if strings.HasPrefix(line, "WEBVTT") || strings.HasPrefix(line, "Kind:") || strings.HasPrefix(line, "Language:") || strings.HasPrefix(line, "NOTE") {
			continue
		}
		if isVTTIdentifier(line) {
			continue
		}
		if strings.Contains(line, "-->") {
			flush()
			start := strings.TrimSpace(strings.SplitN(line, "-->", 2)[0])
			currentTimestamp = formatTimestampVTT(start)
			continue
		}

		text := vttTagRe.ReplaceAllString(line, "")
		text = strings.TrimSpace(text)
		if text != "" {
			textLines = append(textLines, text)
		}
	}
	flush()

	return strings.Join(parts, "\n")
}

// isVTTIdentifier returns true for all-digit cue identifier lines (optional in VTT spec).
func isVTTIdentifier(line string) bool {
	if line == "" {
		return false
	}
	for _, r := range line {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// formatTimestampVTT converts a VTT timestamp ("HH:MM:SS.mmm" or "MM:SS.mmm")
// to the "[M:SS]" format used in transcript output (without the brackets).
func formatTimestampVTT(ts string) string {
	parts := strings.Split(ts, ":")
	var h, m, s int
	switch len(parts) {
	case 3:
		h = atoi(parts[0])
		m = atoi(parts[1])
		s = atoi(strings.SplitN(parts[2], ".", 2)[0])
	case 2:
		m = atoi(parts[0])
		s = atoi(strings.SplitN(parts[1], ".", 2)[0])
	default:
		return ""
	}
	return fmt.Sprintf("%d:%02d", h*60+m, s)
}

// atoi converts a clean digit string to int (avoids strconv error return for
// well-formed timestamp components).
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// highlightLineRe splits a "[M:SS] text" transcript line into its timestamp
// and text parts.
var highlightLineRe = regexp.MustCompile(`^\[(\d+:\d{2})\]\s*(.*)$`)

// splitTimestampedLine separates a transcript line's leading "[M:SS]" marker
// from its text. Returns ("", line) when no marker is present.
func splitTimestampedLine(line string) (startTime, text string) {
	if m := highlightLineRe.FindStringSubmatch(line); len(m) == 3 {
		return m[1], m[2]
	}
	return "", line
}

// scoreTranscriptHighlights scores transcript lines (in "[M:SS] text" format from
// parseTranscriptVTT) by keyword density and structural signals, returning the top
// maxHighlights (5) in descending score order. Returns nil when fewer than
// minHighlightSegments (5) lines are available. query="" uses structural signals only.
func scoreTranscriptHighlights(transcript, query string) []TranscriptHighlight {
	var segments []string
	for _, line := range strings.Split(transcript, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			segments = append(segments, line)
		}
	}
	if len(segments) < minHighlightSegments {
		return nil
	}

	keywords := strings.Fields(strings.ToLower(query))

	type scoredIdx struct {
		idx   int
		score float64
	}
	scores := make([]scoredIdx, len(segments))
	maxScore := 0.0
	for i, line := range segments {
		_, text := splitTimestampedLine(line)
		score := structuralHighlightScore(text)
		if len(keywords) > 0 {
			lower := strings.ToLower(text)
			for _, kw := range keywords {
				score += float64(strings.Count(lower, kw)) * 3
			}
		}
		scores[i] = scoredIdx{idx: i, score: score}
		if score > maxScore {
			maxScore = score
		}
	}

	sort.SliceStable(scores, func(a, b int) bool { return scores[a].score > scores[b].score })
	if len(scores) > maxHighlights {
		scores = scores[:maxHighlights]
	}

	highlights := make([]TranscriptHighlight, 0, len(scores))
	for _, s := range scores {
		norm := 0.0
		if maxScore > 0 {
			norm = s.score / maxScore
		}
		startTime, _ := splitTimestampedLine(segments[s.idx])
		highlights = append(highlights, TranscriptHighlight{
			Text:      segments[s.idx],
			Score:     norm,
			StartTime: startTime,
		})
	}
	return highlights
}

// structuralHighlightScore scores a transcript line's text by structural
// signals: a digit anywhere in a word (+2), an all-caps word (+1), and a
// question-ending line (+1). Pure; no external state.
func structuralHighlightScore(text string) float64 {
	score := 0.0
	for _, w := range strings.Fields(text) {
		clean := strings.TrimFunc(w, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		})
		if clean == "" {
			continue
		}
		if wordHasDigit(clean) {
			score += 2
		}
		if isAllCapsWord(clean) {
			score += 1
		}
	}
	if strings.HasSuffix(strings.TrimSpace(text), "?") {
		score += 1
	}
	return score
}

func wordHasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// isAllCapsWord reports whether a word contains at least one letter and no
// lowercase letters (e.g. "NASA", "AI"). Pure digits are not all-caps words.
func isAllCapsWord(s string) bool {
	hasLetter := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
		}
	}
	return hasLetter
}
