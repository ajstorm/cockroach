package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TableStatsTool provides statistics about a table
type TableStatsTool struct {
	db *pgxpool.Pool
}

// NewTableStatsTool creates a new table stats tool
func NewTableStatsTool(db *pgxpool.Pool) *TableStatsTool {
	return &TableStatsTool{db: db}
}

// TableStatsResult contains table statistics
type TableStatsResult struct {
	TableName      string `json:"table_name"`
	RowCount       int64  `json:"row_count"`
	TableID        int    `json:"table_id"`
}

func (t *TableStatsTool) Name() string {
	return "get_table_stats"
}

func (t *TableStatsTool) Description() string {
	return "Get statistics for a table including row counts and other metrics. IMPORTANT: You must specify the database name - use the database where the table exists (e.g., 'tpcc', 'defaultdb'). Table statistics are database-scoped and will return no results if you specify the wrong database."
}

func (t *TableStatsTool) ActiveDescription() string {
	return "I'm gathering statistics for this table"
}

func (t *TableStatsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"table": map[string]interface{}{
				"type":        "string",
				"description": "Table name (required)",
			},
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Database name (required) - specify the database where this table exists",
			},
		},
		"required": []string{"table", "database"},
	}
}

func (t *TableStatsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	tableName, ok := args["table"].(string)
	if !ok || tableName == "" {
		return nil, fmt.Errorf("table name is required")
	}

	database, ok := args["database"].(string)
	if !ok || database == "" {
		return nil, fmt.Errorf("database name is required")
	}

	var result TableStatsResult
	result.TableName = tableName

	// Query table statistics from the correct database
	// Note: crdb_internal.table_row_statistics is database-scoped
	query := fmt.Sprintf(`
		SELECT
			table_id,
			table_name,
			estimated_row_count
		FROM %s.crdb_internal.table_row_statistics
		WHERE table_name = $1
	`, database)

	if err := t.db.QueryRow(ctx, query, tableName).Scan(
		&result.TableID,
		&result.TableName,
		&result.RowCount,
	); err != nil {
		return nil, fmt.Errorf("failed to query table statistics for table %s in database %s: %w", tableName, database, err)
	}

	return result, nil
}
