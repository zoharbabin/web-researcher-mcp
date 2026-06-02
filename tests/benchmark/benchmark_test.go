package benchmark

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// --- Content processing pipeline benchmarks ---

func BenchmarkContentProcess(b *testing.B) {
	p := content.NewProcessor()
	raw := strings.Repeat("This is a sample paragraph with some content. ", 100) +
		"\n\n" +
		strings.Repeat("This is a sample paragraph with some content. ", 100) +
		"\n\n" +
		strings.Repeat("Another unique paragraph with different words. ", 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Process(raw, 5000)
	}
}

func BenchmarkSanitizeText(b *testing.B) {
	p := content.NewProcessor()
	raw := "Hello\u200BWorld\u200C with \u200D zero-width\uFEFF chars\u2060 and   multiple   spaces\n\n\n\n\nnewlines"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.SanitizeText(raw)
	}
}

func BenchmarkDedupContent(b *testing.B) {
	// Create content with duplicate paragraphs
	paragraphs := make([]string, 50)
	for i := range paragraphs {
		paragraphs[i] = fmt.Sprintf("Paragraph %d with some content here.", i%10)
	}
	raw := strings.Join(paragraphs, "\n\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		content.DedupContent(raw)
	}
}

func BenchmarkTruncate(b *testing.B) {
	raw := strings.Repeat("This is a sentence. ", 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		content.Truncate(raw, 5000)
	}
}

// --- Cache benchmarks ---

func BenchmarkMemoryCacheSet(b *testing.B) {
	m := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 64})
	ctx := context.Background()
	value := []byte("benchmark-value-with-some-payload-data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Set(ctx, fmt.Sprintf("key-%d", i), value, time.Hour)
	}
}

func BenchmarkMemoryCacheGet(b *testing.B) {
	m := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 64})
	ctx := context.Background()
	value := []byte("benchmark-value-with-some-payload-data")

	// Pre-populate
	for i := 0; i < 1000; i++ {
		m.Set(ctx, fmt.Sprintf("key-%d", i), value, time.Hour)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Get(ctx, fmt.Sprintf("key-%d", i%1000))
	}
}

func BenchmarkMemoryCacheSetGet(b *testing.B) {
	m := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 64})
	ctx := context.Background()
	value := []byte("benchmark-value-with-some-payload-data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		m.Set(ctx, key, value, time.Hour)
		m.Get(ctx, key)
	}
}

// --- Session benchmarks ---

func BenchmarkSessionCreate(b *testing.B) {
	mgr, _ := session.NewManager(session.Config{
		MaxSessions: 10000,
		SessionTTL:  time.Hour,
	})
	defer mgr.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.Create(fmt.Sprintf("tenant-%d", i%100), "u1")
	}
}

func BenchmarkSessionGet(b *testing.B) {
	mgr, _ := session.NewManager(session.Config{
		MaxSessions: 10000,
		SessionTTL:  time.Hour,
	})
	defer mgr.Close()

	// Pre-create sessions
	type entry struct {
		tenantID  string
		sessionID string
	}
	entries := make([]entry, 100)
	for i := range entries {
		tenantID := fmt.Sprintf("tenant-%d", i)
		sess, _ := mgr.Create(tenantID, "u1")
		entries[i] = entry{tenantID: tenantID, sessionID: sess.ID}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := entries[i%len(entries)]
		mgr.GetIndex(e.tenantID, "u1", e.sessionID)
	}
}
