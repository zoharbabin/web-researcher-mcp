package tools

// Output schemas for MCP tool definitions (JSON Schema format).

var webSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"urls":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"query":       map[string]any{"type": "string"},
		"resultCount": map[string]any{"type": "integer"},
		"results": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string"},
					"url":         map[string]any{"type": "string"},
					"snippet":     map[string]any{"type": "string"},
					"displayLink": map[string]any{"type": "string"},
				},
			},
		},
	},
}

var imageSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"query":       map[string]any{"type": "string"},
		"resultCount": map[string]any{"type": "integer"},
		"images": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":         map[string]any{"type": "string"},
					"link":          map[string]any{"type": "string"},
					"thumbnailLink": map[string]any{"type": "string"},
					"displayLink":   map[string]any{"type": "string"},
					"contextLink":   map[string]any{"type": "string"},
					"width":         map[string]any{"type": "integer"},
					"height":        map[string]any{"type": "integer"},
					"fileSize":      map[string]any{"type": "string"},
				},
			},
		},
	},
}

var newsSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"query":       map[string]any{"type": "string"},
		"resultCount": map[string]any{"type": "integer"},
		"articles": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string"},
					"url":         map[string]any{"type": "string"},
					"source":      map[string]any{"type": "string"},
					"publishedAt": map[string]any{"type": "string"},
					"snippet":     map[string]any{"type": "string"},
				},
			},
		},
	},
}

var academicSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"query":        map[string]any{"type": "string"},
		"totalResults": map[string]any{"type": "integer"},
		"resultCount":  map[string]any{"type": "integer"},
		"source":       map[string]any{"type": "string"},
		"papers": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":    map[string]any{"type": "string"},
					"url":      map[string]any{"type": "string"},
					"source":   map[string]any{"type": "string"},
					"abstract": map[string]any{"type": "string"},
				},
			},
		},
	},
}

var patentSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"query":       map[string]any{"type": "string"},
		"searchType":  map[string]any{"type": "string"},
		"resultCount": map[string]any{"type": "integer"},
		"patents": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":    map[string]any{"type": "string"},
					"url":      map[string]any{"type": "string"},
					"number":   map[string]any{"type": "string"},
					"abstract": map[string]any{"type": "string"},
				},
			},
		},
	},
}

var scrapePageOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"url":             map[string]any{"type": "string"},
		"content":         map[string]any{"type": "string"},
		"contentType":     map[string]any{"type": "string"},
		"contentLength":   map[string]any{"type": "integer"},
		"truncated":       map[string]any{"type": "boolean"},
		"estimatedTokens": map[string]any{"type": "integer"},
		"sizeCategory":    map[string]any{"type": "string"},
		"citation":        map[string]any{"type": "string"},
		"metadata": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":  map[string]any{"type": "string"},
				"author": map[string]any{"type": "string"},
			},
		},
	},
}

var searchAndScrapeOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"query":           map[string]any{"type": "string"},
		"combinedContent": map[string]any{"type": "string"},
		"sources": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":         map[string]any{"type": "string"},
					"title":       map[string]any{"type": "string"},
					"content":     map[string]any{"type": "string"},
					"contentType": map[string]any{"type": "string"},
					"scores":      map[string]any{"type": "object"},
				},
			},
		},
		"summary": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"urlsSearched":    map[string]any{"type": "integer"},
				"urlsScraped":     map[string]any{"type": "integer"},
				"processingTimeMs": map[string]any{"type": "integer"},
			},
		},
		"sizeMetadata": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"totalLength":    map[string]any{"type": "integer"},
				"estimatedTokens": map[string]any{"type": "integer"},
				"sizeCategory":   map[string]any{"type": "string"},
			},
		},
	},
}

var sequentialSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"sessionId":          map[string]any{"type": "string"},
		"currentStep":        map[string]any{"type": "integer"},
		"totalStepsEstimate": map[string]any{"type": "integer"},
		"isComplete":         map[string]any{"type": "boolean"},
		"startedAt":          map[string]any{"type": "string"},
		"completedAt":        map[string]any{"type": "string"},
		"sources":            map[string]any{"type": "array"},
		"steps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stepNumber":  map[string]any{"type": "integer"},
					"description": map[string]any{"type": "string"},
					"isRevision":  map[string]any{"type": "boolean"},
					"revisesStep": map[string]any{"type": "integer"},
					"branchId":    map[string]any{"type": "string"},
					"timestamp":   map[string]any{"type": "string"},
				},
			},
		},
		"gaps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"description": map[string]any{"type": "string"},
					"foundInStep": map[string]any{"type": "integer"},
				},
			},
		},
	},
}
