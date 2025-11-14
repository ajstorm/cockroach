package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RangeStatusTool provides detailed range information
type RangeStatusTool struct {
	db *pgxpool.Pool
}

// NewRangeStatusTool creates a new range status tool
func NewRangeStatusTool(db *pgxpool.Pool) *RangeStatusTool {
	return &RangeStatusTool{db: db}
}

// RangeDetail represents detailed information about a range
type RangeDetail struct {
	RangeID       int    `json:"range_id"`
	StartKey      string `json:"start_key"`
	EndKey        string `json:"end_key"`
	DatabaseName  string `json:"database_name,omitempty"`
	TableName     string `json:"table_name,omitempty"`
	IndexName     string `json:"index_name,omitempty"`
	Replicas      []int  `json:"replicas"`
	LeaseHolder   int    `json:"lease_holder"`
	RangeSize     int64  `json:"range_size_bytes"`
	ReplicaCount  int    `json:"replica_count"`
	IsUnavailable bool   `json:"is_unavailable"`
}

// RangeStatusResult contains range status information
type RangeStatusResult struct {
	Ranges     []RangeDetail `json:"ranges"`
	TotalCount int         `json:"total_count"`
	Note       string      `json:"note,omitempty"`
}

func (t *RangeStatusTool) Name() string {
	return "get_range_status"
}

func (t *RangeStatusTool) Description() string {
	return "Get detailed status information for ranges, optionally filtered by table or range ID. Useful for investigating specific range issues."
}

func (t *RangeStatusTool) ActiveDescription() string {
	return "I'm getting detailed status information for your ranges"
}

func (t *RangeStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"table_name": map[string]interface{}{
				"type":        "string",
				"description": "Filter by table name (optional)",
			},
			"range_id": map[string]interface{}{
				"type":        "integer",
				"description": "Specific range ID to get status for (optional)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of ranges to return (default: 20)",
			},
		},
		"required": []string{},
	}
}

func (t *RangeStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result RangeStatusResult

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Build query with optional filters
	query := `
		SELECT
			r.range_id,
			r.start_pretty,
			r.end_pretty,
			t.database_name,
			t.name as table_name,
			NULL as index_name,
			rnl.replicas,
			r.lease_holder,
			r.range_size,
			array_length(rnl.replicas, 1) as replica_count
		FROM crdb_internal.ranges r
		LEFT JOIN crdb_internal.table_spans ts ON r.start_key >= ts.start_key AND r.start_key < ts.end_key AND ts.dropped = false
		LEFT JOIN crdb_internal.tables t ON ts.descriptor_id = t.table_id
		JOIN crdb_internal.ranges_no_leases rnl ON r.range_id = rnl.range_id
		WHERE 1=1
	`

	if tableName, ok := args["table_name"].(string); ok && tableName != "" {
		query += fmt.Sprintf(" AND t.name = '%s'", tableName)
	}

	if rangeID, ok := args["range_id"].(float64); ok {
		query += fmt.Sprintf(" AND r.range_id = %d", int(rangeID))
	}

	query += fmt.Sprintf(" ORDER BY r.range_id LIMIT %d", limit)

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query range status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ri RangeDetail
		var dbName, tableName, indexName *string

		if err := rows.Scan(
			&ri.RangeID,
			&ri.StartKey,
			&ri.EndKey,
			&dbName,
			&tableName,
			&indexName,
			&ri.Replicas,
			&ri.LeaseHolder,
			&ri.RangeSize,
			&ri.ReplicaCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan range row: %w", err)
		}

		if dbName != nil {
			ri.DatabaseName = *dbName
		}
		if tableName != nil {
			ri.TableName = *tableName
		}
		if indexName != nil {
			ri.IndexName = *indexName
		}

		ri.IsUnavailable = ri.ReplicaCount == 0

		result.Ranges = append(result.Ranges, ri)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating range rows: %w", err)
	}

	result.TotalCount = len(result.Ranges)

	if result.TotalCount == 0 {
		result.Note = "No ranges found matching the criteria"
	}

	return result, nil
}
