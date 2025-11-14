package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExplainQueryTool analyzes query execution plans
type ExplainQueryTool struct {
	db *pgxpool.Pool
}

// NewExplainQueryTool creates a new explain query tool
func NewExplainQueryTool(db *pgxpool.Pool) *ExplainQueryTool {
	return &ExplainQueryTool{db: db}
}

// ExplainQueryResult contains query plan analysis
type ExplainQueryResult struct {
	Query string `json:"query"`
	Plan  string `json:"plan"`
}

func (t *ExplainQueryTool) Name() string {
	return "explain_query"
}

func (t *ExplainQueryTool) Description() string {
	return "Analyze a SQL query's execution plan to understand performance characteristics and identify optimization opportunities"
}

func (t *ExplainQueryTool) ActiveDescription() string {
	return "I'm analyzing this query's execution plan to find optimization opportunities"
}

func (t *ExplainQueryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "SQL query to analyze (required)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *ExplainQueryTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required")
	}

	var result ExplainQueryResult
	result.Query = query

	// Build EXPLAIN query
	explainQuery := fmt.Sprintf("EXPLAIN (VERBOSE) %s", query)

	// Execute and collect all rows into a single plan string
	rows, err := t.db.Query(ctx, explainQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to explain query: %w", err)
	}
	defer rows.Close()

	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("failed to scan plan row: %w", err)
		}
		planLines = append(planLines, line)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating plan rows: %w", err)
	}

	result.Plan = strings.Join(planLines, "\n")

	return result, nil
}
