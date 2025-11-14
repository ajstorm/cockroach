package cmd

import (
	"context"
	"os"

	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/agent"
	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/db"
	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/server"
	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/tools"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	dbURL      string
	openaiKey  string
	apiKey     string
	port       int
	rateLimit  float64
	rateBurst  int
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the CockroachDB AI Assistant HTTP server",
	Long: `Start the AI Assistant server that provides an HTTP API for
asking questions about your CockroachDB cluster.`,
	RunE: runServer,
}

func init() {
	serverCmd.Flags().StringVar(&dbURL, "db-url", "", "CockroachDB connection URL (required)")
	serverCmd.Flags().StringVar(&openaiKey, "openai-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")
	serverCmd.Flags().StringVar(&apiKey, "api-key", os.Getenv("API_KEY"), "Server API key for authentication")
	serverCmd.Flags().IntVar(&port, "port", 8080, "Server port")
	serverCmd.Flags().Float64Var(&rateLimit, "rate-limit", 10.0, "Requests per second rate limit")
	serverCmd.Flags().IntVar(&rateBurst, "rate-burst", 20, "Rate limit burst size")

	serverCmd.MarkFlagRequired("db-url")
}

func runServer(cmd *cobra.Command, args []string) error {
	// Setup logging
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Validate required flags
	if openaiKey == "" {
		log.Fatal().Msg("OpenAI API key is required (--openai-key or OPENAI_API_KEY env var)")
	}
	if apiKey == "" {
		log.Fatal().Msg("API key is required (--api-key or API_KEY env var)")
	}

	ctx := context.Background()

	// Create database connection pool
	log.Info().Str("db_url", dbURL).Msg("Connecting to CockroachDB")
	pool, err := db.NewPool(ctx, dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer pool.Close()
	log.Info().Msg("Database connection established")

	// Create tool registry (insecure=false, no tsServer for standalone server)
	toolRegistry := tools.NewRegistry(pool, false, nil)
	log.Info().Msg("Tool registry initialized")

	// Create agent (no settings for standalone server, maxIterations=30)
	aiAgent := agent.NewAgent(openaiKey, toolRegistry, nil, 30)
	log.Info().Msg("AI agent initialized")

	// Create and start server
	srv := server.NewServer(aiAgent, &server.Config{
		Port:      port,
		APIKey:    apiKey,
		RateLimit: rateLimit,
		RateBurst: rateBurst,
	})

	log.Info().Msg("Server starting...")
	return srv.Start(port)
}
