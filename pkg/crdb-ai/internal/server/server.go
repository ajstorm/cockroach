package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/agent"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// Server handles HTTP requests for the AI assistant
type Server struct {
	agent   *agent.Agent
	router  *gin.Engine
	apiKey  string
	limiter *rate.Limiter
}

// Config holds server configuration
type Config struct {
	Port      int
	APIKey    string
	RateLimit float64 // requests per second
	RateBurst int
}

// NewServer creates a new HTTP server
func NewServer(agent *agent.Agent, cfg *Config) *Server {
	gin.SetMode(gin.ReleaseMode)

	s := &Server{
		agent:   agent,
		router:  gin.New(),
		apiKey:  cfg.APIKey,
		limiter: rate.NewLimiter(rate.Limit(cfg.RateLimit), cfg.RateBurst),
	}

	// Middleware
	s.router.Use(gin.Recovery())
	s.router.Use(s.loggingMiddleware())
	s.router.Use(s.authMiddleware())
	s.router.Use(s.rateLimitMiddleware())

	// Routes
	s.router.GET("/health", s.handleHealth)
	s.router.POST("/ask", s.handleAsk)

	return s
}

// Start starts the HTTP server
func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	log.Info().Int("port", port).Msg("Starting server")
	return s.router.Run(addr)
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"time":   time.Now().UTC(),
	})
}

func (s *Server) handleAsk(c *gin.Context) {
	var req agent.AskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	resp, err := s.agent.Ask(c.Request.Context(), &req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to process question")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		duration := time.Since(start)
		log.Info().
			Str("method", c.Request.Method).
			Str("path", path).
			Int("status", c.Writer.Status()).
			Dur("duration", duration).
			Msg("Request completed")
	}
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip auth for health endpoint
		if c.Request.URL.Path == "/health" {
			c.Next()
			return
		}

		apiKey := c.GetHeader("Authorization")
		expectedKey := "Bearer " + s.apiKey

		if apiKey == "" || apiKey != expectedKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

func (s *Server) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.limiter.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
