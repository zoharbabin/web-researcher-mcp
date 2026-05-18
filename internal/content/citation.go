package content

import (
	"fmt"
	"strings"
	"time"
)

type Citation struct {
	URL          string           `json:"url"`
	AccessedDate string           `json:"accessedDate"`
	Metadata     CitationMetadata `json:"metadata"`
	Formatted    CitationFormats  `json:"formatted"`
}

type CitationMetadata struct {
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Site   string `json:"site,omitempty"`
	Date   string `json:"date,omitempty"`
}

type CitationFormats struct {
	APA string `json:"apa"`
	MLA string `json:"mla"`
}

func ExtractCitation(url, title, author, siteName, pubDate string) Citation {
	accessed := time.Now().Format("2006-01-02")

	c := Citation{
		URL:          url,
		AccessedDate: accessed,
		Metadata: CitationMetadata{
			Title:  title,
			Author: author,
			Site:   siteName,
			Date:   pubDate,
		},
	}

	c.Formatted = CitationFormats{
		APA: formatAPA(title, author, siteName, pubDate, url, accessed),
		MLA: formatMLA(title, author, siteName, pubDate, url, accessed),
	}

	return c
}

func formatAPA(title, author, site, date, url, accessed string) string {
	parts := []string{}

	if author != "" {
		parts = append(parts, author+".")
	}

	if date != "" {
		parts = append(parts, fmt.Sprintf("(%s).", date))
	} else {
		parts = append(parts, "(n.d.).")
	}

	if title != "" {
		parts = append(parts, title+".")
	}

	if site != "" {
		parts = append(parts, site+".")
	}

	parts = append(parts, fmt.Sprintf("Retrieved %s, from %s", accessed, url))

	return strings.Join(parts, " ")
}

func formatMLA(title, author, site, date, url, accessed string) string {
	parts := []string{}

	if author != "" {
		parts = append(parts, author+".")
	}

	if title != "" {
		parts = append(parts, fmt.Sprintf("\"%s.\"", title))
	}

	if site != "" {
		parts = append(parts, site+",")
	}

	if date != "" {
		parts = append(parts, date+",")
	}

	parts = append(parts, url+".")
	parts = append(parts, fmt.Sprintf("Accessed %s.", accessed))

	return strings.Join(parts, " ")
}
