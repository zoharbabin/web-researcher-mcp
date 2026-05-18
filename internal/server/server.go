package server

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type Config struct {
	Name    string
	Version string
}

type HTTPConfig struct {
	Port           int
	Auth           *auth.Middleware
	RateLimiter    *ratelimit.Limiter
	AllowedOrigins []string
	Metrics        *metrics.Collector
	AdminKey       string
	Cache          cache.Cache
	Sessions       *session.Manager
}

type Server struct {
	mcpServer *server.MCPServer
}

func New(cfg Config) *Server {
	mcpServer := server.NewMCPServer(
		cfg.Name,
		cfg.Version,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
	)

	return &Server{mcpServer: mcpServer}
}

func (s *Server) MCP() *server.MCPServer {
	return s.mcpServer
}

func (s *Server) RunSTDIO(ctx context.Context) error {
	stdioServer := server.NewStdioServer(s.mcpServer)
	return stdioServer.Listen(ctx, os.Stdin, os.Stdout)
}

func (s *Server) ServeHTTP(ctx context.Context, cfg HTTPConfig) error {
	mux := http.NewServeMux()

	sseServer := server.NewSSEServer(s.mcpServer)
	mux.Handle("/mcp/sse", cfg.Auth.Wrap(cfg.RateLimiter.Wrap(http.HandlerFunc(sseServer.ServeHTTP))))
	mux.Handle("/mcp/message", cfg.Auth.Wrap(cfg.RateLimiter.Wrap(http.HandlerFunc(sseServer.ServeHTTP))))

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ready")
	})

	mux.Handle("GET /metrics", cfg.Metrics.HTTPHandler())

	if cfg.AdminKey != "" {
		mux.HandleFunc("DELETE /admin/cache", adminAuth(cfg.AdminKey, handleAdminFlushCache(cfg.Cache)))
		mux.HandleFunc("DELETE /admin/sessions", adminAuth(cfg.AdminKey, handleAdminFlushSessions(cfg.Sessions)))
	}

	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":"web-researcher-mcp","token_endpoint":"n/a"}`)
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: securityHeaders(corsMiddleware(cfg.AllowedOrigins, mux)),
	}

	go func() {
		<-ctx.Done()
		httpServer.Close()
	}()

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := len(allowedOrigins) == 0
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Admin-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func adminAuth(key string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Key") != key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}
}

func handleAdminFlushCache(c cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if c != nil {
			c.Flush()
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "cache flushed")
	}
}

func handleAdminFlushSessions(mgr *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if mgr != nil {
			mgr.DeleteAll()
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "sessions flushed")
	}
}
