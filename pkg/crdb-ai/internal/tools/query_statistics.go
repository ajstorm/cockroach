package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryStatisticsTool provides detailed statistics for a specific query
type QueryStatisticsTool struct {
	db *pgxpool.Pool
}

// NewQueryStatisticsTool creates a new query statistics tool
func NewQueryStatisticsTool(db *pgxpool.Pool) *QueryStatisticsTool {
	return &QueryStatisticsTool{db: db}
}

// QueryStatistics represents detailed statistics for a query
type QueryStatistics struct {
	Query            string  `json:"query"`
	QuerySummary     string  `json:"query_summary"`
	Database         string  `json:"database"`
	AppName          string  `json:"app_name"`
	ExecutionCount   int     `json:"execution_count"`
	AvgLatencySec    float64 `json:"avg_latency_sec"`
	P50LatencySec    float64 `json:"p50_latency_sec"`
	P90LatencySec    float64 `json:"p90_latency_sec"`
	P99LatencySec    float64 `json:"p99_latency_sec"`
	MaxLatencySec    float64 `json:"max_latency_sec"`
	AvgRowsRead      float64 `json:"avg_rows_read"`
	AvgRowsWritten   float64 `json:"avg_rows_written"`
	TotalExecTime    float64 `json:"total_exec_time_sec"`
	AvgRetries       float64 `json:"avg_retries"`
	MaxRetries       int     `json:"max_retries"`
}

// QueryStatisticsResult contains query statistics
type QueryStatisticsResult struct {
	Statistics *QueryStatistics `json:"statistics,omitempty"`
	Note       string           `json:"note"`
}

func (t *QueryStatisticsTool) Name() string {
	return "get_query_statistics"
}

func (t *QueryStatisticsTool) Description() string {
	return "Get detailed execution statistics for a specific query pattern or query summary. Use this to analyze performance characteristics of a particular query."
}

func (t *QueryStatisticsTool) ActiveDescription() string {
	return "I'm analyzing detailed execution statistics for this query pattern"
}

func (t *QueryStatisticsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query_pattern": map[string]interface{}{
				"type":        "string",
				"description": "Part of the query text to search for (e.g., 'SELECT * FROM users')",
			},
		},
		"required": []string{"query_pattern"},
	}
}

func (t *QueryStatisticsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	queryPattern, ok := args["query_pattern"].(string)
	if !ok || queryPattern == "" {
		return nil, fmt.Errorf("query_pattern is required")
	}

	var result QueryStatisticsResult

	// Query for statistics matching the pattern
	// Note: 'cnt' (total count) is in statistics, not execution_statistics (which has sampled counts)
	// Note: latencyInfo only has 'min' and 'max' fields - p50/p90/p99 are not populated in the JSON
	// Note: maxRetries is a simple integer, not an object with mean/max
	query := `
		SELECT
			metadata->>'query' as query,
			metadata->>'querySummary' as query_summary,
			metadata->>'db' as database,
			app_name,
			(statistics->'statistics'->>'cnt')::INT as exec_count,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as avg_latency,
			(statistics->'statistics'->'latencyInfo'->>'min')::FLOAT as p50_latency,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as p90_latency,
			(statistics->'statistics'->'latencyInfo'->>'max')::FLOAT as p99_latency,
			(statistics->'statistics'->'latencyInfo'->>'max')::FLOAT as max_latency,
			(statistics->'statistics'->'rowsRead'->>'mean')::FLOAT as avg_rows_read,
			(statistics->'statistics'->'rowsWritten'->>'mean')::FLOAT as avg_rows_written,
			(statistics->'statistics'->'runLat'->>'mean')::FLOAT * (statistics->'statistics'->>'cnt')::INT as total_exec_time,
			(statistics->'statistics'->>'maxRetries')::FLOAT as avg_retries,
			(statistics->'statistics'->>'maxRetries')::INT as max_retries
		FROM crdb_internal.statement_statistics
		WHERE metadata->>'query' ILIKE '%' || $1 || '%'
		ORDER BY (statistics->'statistics'->>'cnt')::INT DESC
		LIMIT 1
	`

	row := t.db.QueryRow(ctx, query, queryPattern)

	var stats QueryStatistics
	var p50, p90, p99, maxLat, avgRowsRead, avgRowsWritten, avgRetries *float64
	var maxRetries *int

	err := row.Scan(
		&stats.Query,
		&stats.QuerySummary,
		&stats.Database,
		&stats.AppName,
		&stats.ExecutionCount,
		&stats.AvgLatencySec,
		&p50,
		&p90,
		&p99,
		&maxLat,
		&avgRowsRead,
		&avgRowsWritten,
		&stats.TotalExecTime,
		&avgRetries,
		&maxRetries,
	)

	if err != nil {
		if err.Error() == "no rows in result set" {
			result.Note = fmt.Sprintf("No statistics found for query pattern: %s", queryPattern)
			return result, nil
		}
		return nil, fmt.Errorf("failed to query statistics: %w", err)
	}

	// Handle nullable fields
	if p50 != nil {
		stats.P50LatencySec = *p50
	}
	if p90 != nil {
		stats.P90LatencySec = *p90
	}
	if p99 != nil {
		stats.P99LatencySec = *p99
	}
	if maxLat != nil {
		stats.MaxLatencySec = *maxLat
	}
	if avgRowsRead != nil {
		stats.AvgRowsRead = *avgRowsRead
	}
	if avgRowsWritten != nil {
		stats.AvgRowsWritten = *avgRowsWritten
	}
	if avgRetries != nil {
		stats.AvgRetries = *avgRetries
	}
	if maxRetries != nil {
		stats.MaxRetries = *maxRetries
	}

	result.Statistics = &stats
	result.Note = "Statistics for most frequently executed query matching the pattern"

	return result, nil
}
