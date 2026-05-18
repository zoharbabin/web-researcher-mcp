package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	videoIDRegex      = regexp.MustCompile(`(?:v=|youtu\.be/|embed/)([a-zA-Z0-9_-]{11})`)
	playerRespRegex  = regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*(\{.+?\})\s*;`)
)

func (p *Pipeline) scrapeYouTube(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	videoID := extractVideoID(url)
	if videoID == "" {
		return nil, fmt.Errorf("cannot extract video ID from %s", url)
	}

	watchURL := "https://www.youtube.com/watch?v=" + videoID
	req, err := http.NewRequestWithContext(ctx, "GET", watchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

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

	// Extract title
	title := extractYouTubeTitle(pageHTML)

	// Try to get transcript
	transcript, err := extractTranscript(ctx, p.client, pageHTML)
	if err != nil {
		return nil, fmt.Errorf("no transcript available for %s: %w", videoID, err)
	}

	if len(transcript) > maxLength {
		transcript = transcript[:maxLength]
	}

	return &ScrapeResult{
		URL:         url,
		Content:     transcript,
		ContentType: "youtube",
		Title:       title,
	}, nil
}

func extractVideoID(url string) string {
	matches := videoIDRegex.FindStringSubmatch(url)
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
	matches := playerRespRegex.FindStringSubmatch(pageHTML)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", err
	}

	return parseTranscriptXML(string(body)), nil
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
		if langCode == "en" {
			url, _ := track["baseUrl"].(string)
			return url
		}
	}

	// Fall back to first track
	track, ok := tracks[0].(map[string]any)
	if !ok {
		return ""
	}
	url, _ := track["baseUrl"].(string)
	return url
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
