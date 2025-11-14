package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TableRangesTool shows range distribution for tables
type TableRangesTool struct {
	db *pgxpool.Pool
}

// NewTableRangesTool creates a new table ranges tool
func NewTableRangesTool(db *pgxpool.Pool) *TableRangesTool {
	return &TableRangesTool{db: db}
}

// TableRangeDistribution represents range distribution for a table
type TableRangeDistribution struct {
	DatabaseName  string  `json:"database_name"`
	TableName     string  `json:"table_name"`
	RangeCount    int     `json:"range_count"`
	TotalSizeGB   float64 `json:"total_size_gb"`
	AvgRangeSizeMB float64 `json:"avg_range_size_mb"`
	MinReplicas   int     `json:"min_replicas"`
	MaxReplicas   int     `json:"max_replicas"`
}

// TableRangesResult contains table range distribution
type TableRangesResult struct {
	Tables []TableRangeDistribution `json:"tables"`
	Count  int                      `json:"count"`
}

func (t *TableRangesTool) Name() string {
	return "get_table_ranges"
}

func (t *TableRangesTool) Description() string {
	return "Show range distribution across tables including size and replica information"
}

func (t *TableRangesTool) ActiveDescription() string {
	return "I'm analyzing range distribution across your tables"
}

func (t *TableRangesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
			"table": map[string]interface{}{
				"type":        "string",
				"description": "Filter by table name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *TableRangesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result TableRangesResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	query := `
		SELECT
			t.database_name,
			t.name as table_name,
			COUNT(DISTINCT r.range_id) as range_count,
			SUM(r.range_size) as total_size,
			AVG(r.range_size) as avg_range_size,
			MIN(array_length(rnl.replicas, 1)) as min_replicas,
			MAX(array_length(rnl.replicas, 1)) as max_replicas
		FROM crdb_internal.ranges r
		JOIN crdb_internal.table_spans ts ON r.start_key >= ts.start_key AND r.start_key < ts.end_key
		JOIN crdb_internal.tables t ON ts.descriptor_id = t.table_id
		JOIN crdb_internal.ranges_no_leases rnl ON r.range_id = rnl.range_id
		WHERE ts.dropped = false
	`

	if database != "" {
		query += fmt.Sprintf(" AND t.database_name = '%s'", database)
	}

	if table != "" {
		query += fmt.Sprintf(" AND t.name = '%s'", table)
	}

	query += ` GROUP BY t.database_name, t.name ORDER BY total_size DESC`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query table ranges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var trd TableRangeDistribution
		var totalSize, avgSize int64

		if err := rows.Scan(
			&trd.DatabaseName,
			&trd.TableName,
			&trd.RangeCount,
			&totalSize,
			&avgSize,
			&trd.MinReplicas,
			&trd.MaxReplicas,
		); err != nil {
			return nil, fmt.Errorf("failed to scan table range row: %w", err)
		}

		trd.TotalSizeGB = float64(totalSize) / (1024 * 1024 * 1024)
		trd.AvgRangeSizeMB = float64(avgSize) / (1024 * 1024)

		result.Tables = append(result.Tables, trd)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating table range rows: %w", err)
	}

	result.Count = len(result.Tables)

	return result, nil
}
