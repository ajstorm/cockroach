package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ActiveQueriesTool shows currently executing queries
type ActiveQueriesTool struct {
	db *pgxpool.Pool
}

// NewActiveQueriesTool creates a new active queries tool
func NewActiveQueriesTool(db *pgxpool.Pool) *ActiveQueriesTool {
	return &ActiveQueriesTool{db: db}
}

// ActiveQueryInfo represents a currently executing query
type ActiveQueryInfo struct {
	QueryID         string    `json:"query_id"`
	NodeID          int       `json:"node_id"`
	UserName        string    `json:"user_name"`
	Query           string    `json:"query"`
	Database        string    `json:"database"`
	ApplicationName string    `json:"application_name"`
	Phase           string    `json:"phase"`
	DurationSec     float64   `json:"duration_sec"`
	IsFullScan      bool      `json:"is_full_scan"`
	IsDistributed   bool      `json:"is_distributed"`
}

// ActiveQueriesResult contains list of active queries
type ActiveQueriesResult struct {
	Queries []ActiveQueryInfo `json:"queries"`
	Count   int               `json:"count"`
}

func (t *ActiveQueriesTool) Name() string {
	return "get_active_queries"
}

func (t *ActiveQueriesTool) Description() string {
	return "Show currently executing queries on the cluster, useful for identifying long-running or stuck queries"
}

func (t *ActiveQueriesTool) ActiveDescription() string {
	return "I'm checking what queries are currently running on your cluster"
}

func (t *ActiveQueriesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"min_duration_sec": map[string]interface{}{
				"type":        "number",
				"description": "Only show queries running longer than this many seconds (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *ActiveQueriesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ActiveQueriesResult

	query := `
		SELECT
			query_id,
			node_id,
			user_name,
			query,
			database,
			application_name,
			phase,
			start,
			full_scan,
			distributed
		FROM crdb_internal.cluster_queries
		ORDER BY start ASC
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query active queries: %w", err)
	}
	defer rows.Close()

	minDuration := 0.0
	if d, ok := args["min_duration_sec"].(float64); ok {
		minDuration = d
	}

	now := time.Now()
	for rows.Next() {
		var q ActiveQueryInfo
		var startTime time.Time
		var fullScan, distributed *bool
		if err := rows.Scan(
			&q.QueryID,
			&q.NodeID,
			&q.UserName,
			&q.Query,
			&q.Database,
			&q.ApplicationName,
			&q.Phase,
			&startTime,
			&fullScan,
			&distributed,
		); err != nil {
			return nil, fmt.Errorf("failed to scan query row: %w", err)
		}

		// Handle NULL booleans
		if fullScan != nil {
			q.IsFullScan = *fullScan
		}
		if distributed != nil {
			q.IsDistributed = *distributed
		}

		// Calculate duration
		q.DurationSec = now.Sub(startTime).Seconds()

		// Apply duration filter if specified
		if q.DurationSec >= minDuration {
			result.Queries = append(result.Queries, q)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating query rows: %w", err)
	}

	result.Count = len(result.Queries)

	return result, nil
}
