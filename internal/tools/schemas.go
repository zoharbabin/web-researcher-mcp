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
		"hints":        map[string]any{"type": "object"},
		"papers": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":         map[string]any{"type": "string"},
					"url":           map[string]any{"type": "string"},
					"doi":           map[string]any{"type": "string"},
					"authors":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"journal":       map[string]any{"type": "string"},
					"year":          map[string]any{"type": "integer"},
					"abstract":      map[string]any{"type": "string"},
					"citationCount": map[string]any{"type": "integer"},
					"source":        map[string]any{"type": "string"},
					"openAccess":    map[string]any{"type": "boolean"},
					"pdfUrl":        map[string]any{"type": "string"},
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
		"source":      map[string]any{"type": "string"},
		"searchUrl":   map[string]any{"type": "string"},
		"hints":       map[string]any{"type": "object"},
		"patents": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":    map[string]any{"type": "string"},
					"url":      map[string]any{"type": "string"},
					"number":   map[string]any{"type": "string"},
					"abstract": map[string]any{"type": "string"},
					"assignee": map[string]any{"type": "string"},
					"inventor": map[string]any{"type": "string"},
					"filed":    map[string]any{"type": "string"},
					"granted":  map[string]any{"type": "string"},
					"pdf":      map[string]any{"type": "string"},
					"status":   map[string]any{"type": "string"},
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
		"raw":             map[string]any{"type": "boolean"},
		"citation": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":          map[string]any{"type": "string"},
				"accessedDate": map[string]any{"type": "string"},
				"metadata": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":  map[string]any{"type": "string"},
						"author": map[string]any{"type": "string"},
						"site":   map[string]any{"type": "string"},
						"date":   map[string]any{"type": "string"},
					},
				},
				"formatted": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"apa": map[string]any{"type": "string"},
						"mla": map[string]any{"type": "string"},
					},
				},
			},
		},
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
		"status":          map[string]any{"type": "string"},
		"combinedContent": map[string]any{"type": "string"},
		"note":            map[string]any{"type": "string"},
		"scrapeFailures":  map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
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
				"urlsSearched":     map[string]any{"type": "integer"},
				"urlsScraped":      map[string]any{"type": "integer"},
				"urlsFailed":       map[string]any{"type": "integer"},
				"processingTimeMs": map[string]any{"type": "integer"},
			},
		},
		"sizeMetadata": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"totalLength":     map[string]any{"type": "integer"},
				"estimatedTokens": map[string]any{"type": "integer"},
				"sizeCategory":    map[string]any{"type": "string"},
			},
		},
		// Additive, content-only enrichments (#95, #90). Both are omitted unless
		// enabled and non-empty; neither alters `sources` ordering.
		"recommendations": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":    map[string]any{"type": "string"},
					"title":  map[string]any{"type": "string"},
					"score":  map[string]any{"type": "number"},
					"reason": map[string]any{"type": "string"},
				},
			},
		},
		"components": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":          map[string]any{"type": "string"},
					"autoFormatted": map[string]any{"type": "boolean"},
					"label":         map[string]any{"type": "string"},
					"title":         map[string]any{"type": "string"},
					"sourceRefs":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"card":          map[string]any{"type": "object"},
					"table":         map[string]any{"type": "object"},
				},
			},
		},
	},
}

var getMyAnalyticsOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status": map[string]any{"type": "string"}, // ok | empty | no_consent | unavailable
		"reason": map[string]any{"type": "string"},
		"analytics": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tenantId":    map[string]any{"type": "string"},
				"userId":      map[string]any{"type": "string"},
				"totalCalls":  map[string]any{"type": "integer"},
				"toolCounts":  map[string]any{"type": "object"},
				"firstSeen":   map[string]any{"type": "string"},
				"lastSeen":    map[string]any{"type": "string"},
				"recentTools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
	},
}

var memorySaveOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status":    map[string]any{"type": "string"}, // ok | no_consent | unavailable
		"reason":    map[string]any{"type": "string"},
		"id":        map[string]any{"type": "string"},
		"createdAt": map[string]any{"type": "string"},
	},
}

var memoryRecallOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status": map[string]any{"type": "string"},
		"reason": map[string]any{"type": "string"},
		"count":  map[string]any{"type": "integer"},
		"memories": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":        map[string]any{"type": "string"},
					"tenantId":  map[string]any{"type": "string"},
					"userId":    map[string]any{"type": "string"},
					"topic":     map[string]any{"type": "string"},
					"note":      map[string]any{"type": "string"},
					"url":       map[string]any{"type": "string"},
					"tags":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"createdAt": map[string]any{"type": "string"},
				},
			},
		},
	},
}

var sequentialSearchOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"sessionId":          map[string]any{"type": "string"},
		"responseMode":       map[string]any{"type": "string"},
		"researchGoal":       map[string]any{"type": "string"},
		"currentStep":        map[string]any{"type": "integer"},
		"totalStepsEstimate": map[string]any{"type": "integer"},
		"isComplete":         map[string]any{"type": "boolean"},
		"startedAt":          map[string]any{"type": "string"},
		"completedAt":        map[string]any{"type": "string"},
		"warning":            map[string]any{"type": "string"},
		"summary":            map[string]any{"type": "string"},
		"steps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stepNumber": map[string]any{"type": "integer"},
					"oneLiner":   map[string]any{"type": "string"},
					"branchId":   map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "string"},
				},
			},
		},
		"stepIndex": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stepNumber": map[string]any{"type": "integer"},
					"oneLiner":   map[string]any{"type": "string"},
					"branchId":   map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "string"},
				},
			},
		},
		"lastSteps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stepNumber":         map[string]any{"type": "integer"},
					"description":        map[string]any{"type": "string"},
					"reasoning":          map[string]any{"type": "string"},
					"confidence":         map[string]any{"type": "string"},
					"rejectedApproaches": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"isRevision":         map[string]any{"type": "boolean"},
					"revisesStep":        map[string]any{"type": "integer"},
					"branchId":           map[string]any{"type": "string"},
					"timestamp":          map[string]any{"type": "string"},
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
		"sources": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":         map[string]any{"type": "string"},
					"title":       map[string]any{"type": "string"},
					"relevance":   map[string]any{"type": "string"},
					"foundInStep": map[string]any{"type": "integer"},
				},
			},
		},
	},
}

var getSessionOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"sessionId":    map[string]any{"type": "string"},
		"responseMode": map[string]any{"type": "string"},
		"researchGoal": map[string]any{"type": "string"},
		"stepCount":    map[string]any{"type": "integer"},
		"summary":      map[string]any{"type": "string"},
		"startedAt":    map[string]any{"type": "string"},
		"stepIndex": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stepNumber": map[string]any{"type": "integer"},
					"oneLiner":   map[string]any{"type": "string"},
					"branchId":   map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "string"},
				},
			},
		},
		"lastSteps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stepNumber":         map[string]any{"type": "integer"},
					"description":        map[string]any{"type": "string"},
					"reasoning":          map[string]any{"type": "string"},
					"confidence":         map[string]any{"type": "string"},
					"rejectedApproaches": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"isRevision":         map[string]any{"type": "boolean"},
					"revisesStep":        map[string]any{"type": "integer"},
					"branchId":           map[string]any{"type": "string"},
					"timestamp":          map[string]any{"type": "string"},
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
		"sources": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":         map[string]any{"type": "string"},
					"title":       map[string]any{"type": "string"},
					"relevance":   map[string]any{"type": "string"},
					"foundInStep": map[string]any{"type": "integer"},
				},
			},
		},
		"step": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"stepNumber":         map[string]any{"type": "integer"},
				"description":        map[string]any{"type": "string"},
				"reasoning":          map[string]any{"type": "string"},
				"confidence":         map[string]any{"type": "string"},
				"rejectedApproaches": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"isRevision":         map[string]any{"type": "boolean"},
				"revisesStep":        map[string]any{"type": "integer"},
				"branchId":           map[string]any{"type": "string"},
				"timestamp":          map[string]any{"type": "string"},
			},
		},
	},
}
