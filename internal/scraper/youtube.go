package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	videoIDRegex    = regexp.MustCompile(`(?:v=|youtu\.be/|embed/)([a-zA-Z0-9_-]{11})`)
	playerRespRegex = regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*(\{.+?\})\s*;`)
	playerRespAlt   = regexp.MustCompile(`var\s+ytInitialPlayerResponse\s*=\s*(\{.+?\})\s*;`)
	descriptionRe   = regexp.MustCompile(`"shortDescription"\s*:\s*"((?:[^"\\]|\\.)*)"`)
)

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
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("video not found")
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
		if len(transcript) > maxLength {
			transcript = transcript[:maxLength]
		}
		return &ScrapeResult{
			URL:         rawURL,
			Content:     transcript,
			ContentType: "youtube",
			Title:       title,
		}, nil
	}

	// Strategy 2: Direct timedtext API
	transcript, err = fetchTimedTextAPI(ctx, p.client, videoID)
	if err == nil && len(transcript) > 100 {
		if len(transcript) > maxLength {
			transcript = transcript[:maxLength]
		}
		return &ScrapeResult{
			URL:         rawURL,
			Content:     transcript,
			ContentType: "youtube",
			Title:       title,
		}, nil
	}

	// Strategy 3: Fall back to video description
	description := extractDescription(pageHTML)
	if description != "" {
		content := fmt.Sprintf("[Video: %s]\n\n%s", title, description)
		if len(content) > maxLength {
			content = content[:maxLength]
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

	return parseTranscriptXML(string(body)), nil
}

func fetchTimedTextAPI(ctx context.Context, client *http.Client, videoID string) (string, error) {
	languages := []string{"en", "en-US", "en-GB"}

	for _, lang := range languages {
		apiURL := fmt.Sprintf("https://www.youtube.com/api/timedtext?v=%s&lang=%s&fmt=srv3",
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
		resp.Body.Close()
		if err != nil {
			continue
		}

		if resp.StatusCode == 200 && len(body) > 50 {
			text := parseTranscriptXML(string(body))
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

func parseTranscriptXML(xml string) string {
	textRegex := regexp.MustCompile(`<text[^>]*start="([^"]*)"[^>]*>([^<]*)</text>`)
	matches := textRegex.FindAllStringSubmatch(xml, -1)

	var parts []string
	for _, m := range matches {
		if len(m) >= 3 {
			text := m[2]
			text = strings.ReplaceAll(text, "&amp;", "&")
			text = strings.ReplaceAll(text, "&lt;", "<")
			text = strings.ReplaceAll(text, "&gt;", ">")
			text = strings.ReplaceAll(text, "&#39;", "'")
			text = strings.ReplaceAll(text, "&quot;", "\"")
			text = strings.ReplaceAll(text, "&#10;", "\n")
			text = strings.TrimSpace(text)
			if text != "" {
				startSec := parseSeconds(m[1])
				timestamp := formatTimestamp(startSec)
				parts = append(parts, fmt.Sprintf("[%s] %s", timestamp, text))
			}
		}
	}

	return strings.Join(parts, "\n")
}

func parseSeconds(s string) float64 {
	var sec float64
	_, _ = fmt.Sscanf(s, "%f", &sec)
	return sec
}

func formatTimestamp(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}
