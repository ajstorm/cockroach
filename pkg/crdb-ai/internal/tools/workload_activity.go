package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WorkloadActivityTool shows recent workload activity from statement statistics
type WorkloadActivityTool struct {
	db *pgxpool.Pool
}

// NewWorkloadActivityTool creates a new workload activity tool
func NewWorkloadActivityTool(db *pgxpool.Pool) *WorkloadActivityTool {
	return &WorkloadActivityTool{db: db}
}

// WorkloadActivity represents recent query activity
type WorkloadActivity struct {
	QuerySummary    string  `json:"query_summary"`
	Database        string  `json:"database"`
	AppName         string  `json:"app_name"`
	ExecutionCount  int64   `json:"execution_count"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`
	TotalTimeMs     float64 `json:"total_time_ms"`
	RowsRead        int64   `json:"rows_read"`
	RowsWritten     int64   `json:"rows_written"`
	LastExecTime    string  `json:"last_exec_time"`
}

// WorkloadActivityResult contains recent workload activity
type WorkloadActivityResult struct {
	Activities      []WorkloadActivity `json:"activities"`
	TotalQueries    int64              `json:"total_queries"`
	TotalExecutions int64              `json:"total_executions"`
	Note            string             `json:"note"`
}

func (t *WorkloadActivityTool) Name() string {
	return "get_workload_activity"
}

func (t *WorkloadActivityTool) Description() string {
	return "Show recent workload activity and query patterns from statement statistics. Use this to see what queries have been running recently, even if they're too fast to show up in active queries."
}

func (t *WorkloadActivityTool) ActiveDescription() string {
	return "I'm reviewing recent workload activity and query patterns"
}

func (t *WorkloadActivityTool) Parameters() map[string]interface{} {
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
				"type":        "number",
				"description": "Maximum number of query patterns to return (default: 20)",
			},
			"min_executions": map[string]interface{}{
				"type":        "number",
				"description": "Only show queries executed at least this many times (default: 1)",
			},
		},
		"required": []string{},
	}
}

func (t *WorkloadActivityTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result WorkloadActivityResult

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	minExecutions := int64(1)
	if m, ok := args["min_executions"].(float64); ok {
		minExecutions = int64(m)
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

	// Query statement statistics for recent activity
	// Note: 'cnt' (total count) is in statistics, not execution_statistics (which has sampled counts)
	query := `
		SELECT
			metadata->>'querySummary' as query_summary,
			metadata->>'db' as database,
			app_name,
			(statistics->'statistics'->>'cnt')::INT as execution_count,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as avg_latency,
			(statistics->'statistics'->'rowsRead'->>'mean')::FLOAT as rows_read,
			(statistics->'statistics'->'rowsWritten'->>'mean')::FLOAT as rows_written,
			aggregated_ts
		FROM crdb_internal.statement_statistics
		WHERE (statistics->'statistics'->>'cnt')::INT >= $1
	`

	queryArgs := []interface{}{minExecutions}
	argNum := 2

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

	query += fmt.Sprintf(" ORDER BY (statistics->'statistics'->>'cnt')::INT DESC LIMIT %d", limit)

	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query workload activity: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var wa WorkloadActivity
		var execCount *int64
		var avgLatency, rowsRead, rowsWritten *float64
		var database, appName, querySummary *string
		var aggregatedTs time.Time

		if err := rows.Scan(
			&querySummary,
			&database,
			&appName,
			&execCount,
			&avgLatency,
			&rowsRead,
			&rowsWritten,
			&aggregatedTs,
		); err != nil {
			return nil, fmt.Errorf("failed to scan workload activity row: %w", err)
		}

		if querySummary != nil {
			wa.QuerySummary = *querySummary
		}
		if database != nil {
			wa.Database = *database
		}
		if appName != nil {
			wa.AppName = *appName
		}
		if execCount != nil {
			wa.ExecutionCount = *execCount
			result.TotalExecutions += *execCount
		}
		if avgLatency != nil {
			// Convert from seconds to milliseconds
			wa.AvgLatencyMs = *avgLatency * 1000
			wa.TotalTimeMs = wa.AvgLatencyMs * float64(wa.ExecutionCount)
		}
		if rowsRead != nil {
			// rowsRead is now average (mean) rows read per execution
			wa.RowsRead = int64(*rowsRead)
		}
		if rowsWritten != nil {
			// rowsWritten is now average (mean) rows written per execution
			wa.RowsWritten = int64(*rowsWritten)
		}
		wa.LastExecTime = aggregatedTs.UTC().Format(time.RFC3339)

		result.Activities = append(result.Activities, wa)
		result.TotalQueries++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating workload activity rows: %w", err)
	}

	// Build time range note
	var timeRangeNote string
	if startTime != nil {
		timeRangeNote = fmt.Sprintf(" from %s to %s", startTime.UTC().Format(time.RFC3339), endTime.UTC().Format(time.RFC3339))
	}

	if result.TotalQueries == 0 {
		result.Note = fmt.Sprintf("No query activity found in statement statistics%s. The workload may be very new or statistics may have been reset.", timeRangeNote)
	} else {
		result.Note = fmt.Sprintf("Found %d query patterns with %d total executions%s.", result.TotalQueries, result.TotalExecutions, timeRangeNote)
	}

	return result, nil
}
