package cmd

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/agent"
	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/db"
	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/tools"
	"github.com/spf13/cobra"
)

var (
	askDBURL     string
	askOpenAIKey string
)

var askCmd = &cobra.Command{
	Use:   "ask [question]",
	Short: "Ask the AI assistant a question about your CockroachDB cluster",
	Long: `Ask the AI assistant a question and get an immediate answer.
This command connects directly to your cluster and uses AI to analyze it.

Examples:
  crdb-ai ask "What is the state of my cluster?"
  crdb-ai ask "How many nodes are running?"
  crdb-ai ask "Are there any unavailable ranges?"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAsk,
}

func init() {
	askCmd.Flags().StringVar(&askDBURL, "db-url", "", "CockroachDB connection URL (required)")
	askCmd.Flags().StringVar(&askOpenAIKey, "openai-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")

	askCmd.MarkFlagRequired("db-url")
}

var thinkingEmojis = []string{"🤔", "🔍", "🧠", "💭", "⚡", "🎯", "🤖", "💡"}
var thinkingMessages = []string{
	"Hmm, let me think about that...",
	"Stalking your database...",
	"Bribing the cluster for secrets...",
	"Consulting my crystal ball...",
	"Waking up the nodes...",
	"Hunting for clues...",
	"Beep boop analyzing...",
	"Aha! Wait, no... still thinking...",
	"Poking around your ranges...",
	"Sweet-talking the statistics...",
	"Interrogating system tables...",
	"Doing some detective work...",
}

// startThinkingAnimation starts an animated thinking indicator
// Returns a channel that should be closed to stop the animation
func startThinkingAnimation() chan struct{} {
	stopChan := make(chan struct{})

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		fmt.Print("\n")
		for {
			select {
			case <-stopChan:
				// Clear the line and return
				fmt.Print("\r\033[K")
				return
			case <-ticker.C:
				// Pick random emoji and message
				emoji := thinkingEmojis[rand.Intn(len(thinkingEmojis))]
				message := thinkingMessages[rand.Intn(len(thinkingMessages))]

				// Clear the line first, then print new message
				fmt.Printf("\r\033[K%s %s", emoji, message)
			}
		}
	}()

	return stopChan
}

func runAsk(cmd *cobra.Command, args []string) error {
	// Validate OpenAI key
	if askOpenAIKey == "" {
		return fmt.Errorf("OpenAI API key is required (--openai-key or OPENAI_API_KEY env var)")
	}

	// Join all args into a single question
	question := strings.Join(args, " ")

	ctx := context.Background()

	// Create database connection pool
	pool, err := db.NewPool(ctx, askDBURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer pool.Close()

	// Create tool registry (insecure=false, no tsServer for CLI)
	toolRegistry := tools.NewRegistry(pool, false, nil)

	// Create agent (no settings for CLI, maxIterations=30)
	aiAgent := agent.NewAgent(askOpenAIKey, toolRegistry, nil, 30)

	// Start thinking animation
	stopAnimation := startThinkingAnimation()

	// Ask the question
	resp, err := aiAgent.Ask(ctx, &agent.AskRequest{
		Prompt: question,
	})

	// Stop the animation
	close(stopAnimation)
	time.Sleep(50 * time.Millisecond) // Give goroutine time to clean up

	if err != nil {
		return fmt.Errorf("failed to get answer: %w", err)
	}

	// Print the response
	printResponse(resp)

	return nil
}

func printResponse(resp *agent.AskResponse) {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📊 Answer:")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println(resp.Answer)
	fmt.Println()

	if len(resp.Recommendations) > 0 {
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("💡 Recommendations:")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		for i, rec := range resp.Recommendations {
			fmt.Printf("  %d. %s\n", i+1, rec)
		}
		fmt.Println()
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("🔧 Tools used: %s\n", strings.Join(resp.ToolsUsed, ", "))
	fmt.Printf("📈 Confidence: %s | 🎫 Tokens: %d\n", resp.Confidence, resp.TokensUsed)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}
