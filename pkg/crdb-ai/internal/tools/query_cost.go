package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryCostTool estimates query execution cost
type QueryCostTool struct {
	db *pgxpool.Pool
}

// NewQueryCostTool creates a new query cost tool
func NewQueryCostTool(db *pgxpool.Pool) *QueryCostTool {
	return &QueryCostTool{db: db}
}

// QueryCostEstimate represents cost estimation for a query
type QueryCostEstimate struct {
	Query           string  `json:"query"`
	EstimatedCost   float64 `json:"estimated_cost"`
	EstimatedRows   int64   `json:"estimated_rows"`
	FullScan        bool    `json:"full_scan"`
	DistributedPlan bool    `json:"distributed_plan"`
	Warnings        []string `json:"warnings,omitempty"`
	Plan            string  `json:"plan"`
}

// QueryCostResult contains query cost estimation
type QueryCostResult struct {
	Estimate QueryCostEstimate `json:"estimate"`
	Note     string            `json:"note"`
}

func (t *QueryCostTool) Name() string {
	return "estimate_query_cost"
}

func (t *QueryCostTool) Description() string {
	return "Estimate query execution cost and resource usage before running expensive queries"
}

func (t *QueryCostTool) ActiveDescription() string {
	return "I'm estimating the execution cost of this query"
}

func (t *QueryCostTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "SQL query to estimate cost for (required)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *QueryCostTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result QueryCostResult

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query parameter is required")
	}

	result.Estimate.Query = query

	// Get EXPLAIN output for cost estimation
	explainQuery := fmt.Sprintf("EXPLAIN %s", query)

	rows, err := t.db.Query(ctx, explainQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to explain query: %w", err)
	}
	defer rows.Close()

	var planLines []string
	var estimatedRows int64
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("failed to scan explain row: %w", err)
		}
		planLines = append(planLines, line)

		// Parse plan for indicators
		if len(line) > 0 {
			// Check for full scan
			if contains(line, "full scan") || contains(line, "table reader") {
				result.Estimate.FullScan = true
			}
			// Check for distributed
			if contains(line, "distributed") {
				result.Estimate.DistributedPlan = true
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading explain output: %w", err)
	}

	// Combine plan lines
	for _, line := range planLines {
		result.Estimate.Plan += line + "\n"
	}

	// Generate warnings based on plan characteristics
	if result.Estimate.FullScan {
		result.Estimate.Warnings = append(result.Estimate.Warnings, "Query performs full table scan - consider adding an index")
	}

	// Estimate cost (simplified - real implementation would parse EXPLAIN output)
	result.Estimate.EstimatedCost = float64(len(planLines)) * 100 // Placeholder
	result.Estimate.EstimatedRows = estimatedRows

	result.Note = "Cost estimates are approximate. Actual query performance may vary based on data distribution and cluster load."

	return result, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
