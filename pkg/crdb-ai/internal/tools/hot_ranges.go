package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HotRangesTool identifies hot ranges and contended tables
type HotRangesTool struct {
	db *pgxpool.Pool
}

// NewHotRangesTool creates a new hot ranges tool
func NewHotRangesTool(db *pgxpool.Pool) *HotRangesTool {
	return &HotRangesTool{db: db}
}

// HotRangeInfo represents a contended table
type HotRangeInfo struct {
	DatabaseName        string `json:"database_name"`
	SchemaName          string `json:"schema_name"`
	TableName           string `json:"table_name"`
	NumContentionEvents int    `json:"num_contention_events"`
}

// HotRangesResult contains list of hot ranges
type HotRangesResult struct {
	HotRanges []HotRangeInfo `json:"hot_ranges"`
	Count     int            `json:"count"`
	Note      string         `json:"note"`
}

func (t *HotRangesTool) Name() string {
	return "get_hot_ranges"
}

func (t *HotRangesTool) Description() string {
	return "Identify hot ranges and contended tables that are experiencing lock contention or high access patterns"
}

func (t *HotRangesTool) ActiveDescription() string {
	return "I'm identifying hot ranges and contended tables in your cluster"
}

func (t *HotRangesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of hot ranges to return (default: 10)",
			},
		},
		"required": []string{},
	}
}

func (t *HotRangesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	var result HotRangesResult
	result.Note = "Tables ordered by number of contention events (highest first)"

	query := fmt.Sprintf(`
		SELECT
			database_name,
			schema_name,
			table_name,
			num_contention_events
		FROM crdb_internal.cluster_contended_tables
		WHERE num_contention_events > 0
		ORDER BY num_contention_events DESC
		LIMIT %d
	`, limit)

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query hot ranges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var hr HotRangeInfo
		if err := rows.Scan(
			&hr.DatabaseName,
			&hr.SchemaName,
			&hr.TableName,
			&hr.NumContentionEvents,
		); err != nil {
			return nil, fmt.Errorf("failed to scan hot range row: %w", err)
		}
		result.HotRanges = append(result.HotRanges, hr)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating hot range rows: %w", err)
	}

	result.Count = len(result.HotRanges)

	if result.Count == 0 {
		result.Note = "No contended tables found - cluster is running smoothly"
	}

	return result, nil
}
