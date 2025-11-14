package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SlowQueriesTool identifies slow-running queries
type SlowQueriesTool struct {
	db *pgxpool.Pool
}

// NewSlowQueriesTool creates a new slow queries tool
func NewSlowQueriesTool(db *pgxpool.Pool) *SlowQueriesTool {
	return &SlowQueriesTool{db: db}
}

// SlowQueryInfo represents information about a slow query
type SlowQueryInfo struct {
	Query           string  `json:"query"`
	QuerySummary    string  `json:"query_summary"`
	Database        string  `json:"database"`
	ExecutionCount  int     `json:"execution_count"`
	AvgLatencySec   float64 `json:"avg_latency_sec"`
	P99LatencySec   float64 `json:"p99_latency_sec"`
	AvgRowsRead     float64 `json:"avg_rows_read"`
}

// SlowQueriesResult contains list of slow queries
type SlowQueriesResult struct {
	Queries []SlowQueryInfo `json:"queries"`
	Note    string          `json:"note"`
}

func (t *SlowQueriesTool) Name() string {
	return "get_slow_queries"
}

func (t *SlowQueriesTool) Description() string {
	return "Identify the slowest queries in the cluster based on average latency and execution statistics"
}

func (t *SlowQueriesTool) ActiveDescription() string {
	return "I'm identifying the slowest queries in your cluster"
}

func (t *SlowQueriesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start of time range. Supports RFC3339 (e.g., '2024-12-10T18:10:00Z'), relative (e.g., '2h ago', '7d ago'), or 'now'. If provided with end_time, defines an explicit time window.",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End of time range. Supports same formats as start_time. Defaults to 'now' if start_time is provided.",
			},
			"time_range": map[string]interface{}{
				"type":        "string",
				"description": "Relative time range from now (e.g., '1h', '24h', '7d'). Ignored if start_time is provided.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of slow queries to return (default: 10)",
			},
		},
		"required": []string{},
	}
}

func (t *SlowQueriesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Parse time range parameters
	var startTime, endTime *time.Time
	now := time.Now()

	if st, ok := args["start_time"].(string); ok && st != "" {
		t, err := ParseTimeArgument(st, now)
		if err != nil {
			return nil, fmt.Errorf("invalid start_time: %w", err)
		}
		startTime = &t
	}

	if et, ok := args["end_time"].(string); ok && et != "" {
		t, err := ParseTimeArgument(et, now)
		if err != nil {
			return nil, fmt.Errorf("invalid end_time: %w", err)
		}
		endTime = &t
	}

	// If time_range is provided and start_time is not, use time_range
	if startTime == nil {
		if tr, ok := args["time_range"].(string); ok && tr != "" {
			duration, err := ParseExtendedDuration(tr)
			if err != nil {
				return nil, fmt.Errorf("invalid time_range: %w", err)
			}
			st := now.Add(-duration)
			startTime = &st
		}
	}

	// Default end_time to now if start_time is provided but end_time is not
	if startTime != nil && endTime == nil {
		endTime = &now
	}

	var result SlowQueriesResult

	// Build time range note
	if startTime != nil {
		result.Note = fmt.Sprintf("Queries from %s to %s, ordered by average service latency (slowest first)",
			startTime.UTC().Format(time.RFC3339), endTime.UTC().Format(time.RFC3339))
	} else {
		result.Note = "Queries ordered by average service latency (slowest first). No time filter applied - showing all available statistics."
	}

	// Query statement statistics, extracting JSON fields
	// We'll get query text and basic stats
	// Note: 'cnt' (total count) is in statistics, not execution_statistics (which has sampled counts)
	query := `
		SELECT
			metadata->>'query' as query,
			metadata->>'querySummary' as query_summary,
			metadata->>'db' as database,
			(statistics->'statistics'->>'cnt')::INT as exec_count,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as avg_latency,
			(statistics->'statistics'->'latencyInfo'->>'max')::FLOAT as max_latency,
			(statistics->'statistics'->'rowsRead'->>'mean')::FLOAT as avg_rows_read
		FROM crdb_internal.statement_statistics
		WHERE metadata->>'query' IS NOT NULL
			AND metadata->>'query' != ''
	`

	var queryArgs []interface{}
	argNum := 1

	if startTime != nil {
		// Subtract 1 hour from start time to account for aggregation bucket alignment.
		// Statement statistics are aggregated into hourly buckets, and aggregated_ts
		// represents the START of each bucket. A query executed at 18:35 would have
		// aggregated_ts of 18:00, so we need to look back one aggregation interval.
		adjustedStart := startTime.Add(-time.Hour)
		query += fmt.Sprintf(" AND aggregated_ts >= $%d", argNum)
		queryArgs = append(queryArgs, adjustedStart)
		argNum++
	}

	if endTime != nil {
		query += fmt.Sprintf(" AND aggregated_ts <= $%d", argNum)
		queryArgs = append(queryArgs, *endTime)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY avg_latency DESC LIMIT %d", limit)

	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query slow queries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q SlowQueryInfo
		var maxLatency *float64
		if err := rows.Scan(
			&q.Query,
			&q.QuerySummary,
			&q.Database,
			&q.ExecutionCount,
			&q.AvgLatencySec,
			&maxLatency,
			&q.AvgRowsRead,
		); err != nil {
			return nil, fmt.Errorf("failed to scan query row: %w", err)
		}
		if maxLatency != nil {
			q.P99LatencySec = *maxLatency
		}
		result.Queries = append(result.Queries, q)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating query rows: %w", err)
	}

	return result, nil
}
