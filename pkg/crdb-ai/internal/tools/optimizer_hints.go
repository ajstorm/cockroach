package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OptimizerHintsTool shows query optimizer statistics and hints
type OptimizerHintsTool struct {
	db *pgxpool.Pool
}

// NewOptimizerHintsTool creates a new optimizer hints tool
func NewOptimizerHintsTool(db *pgxpool.Pool) *OptimizerHintsTool {
	return &OptimizerHintsTool{db: db}
}

// OptimizerHint represents an optimizer hint or statistic
type OptimizerHint struct {
	TableName       string  `json:"table_name"`
	ColumnName      string  `json:"column_name,omitempty"`
	Statistic       string  `json:"statistic"`
	Value           string  `json:"value"`
	LastUpdated     string  `json:"last_updated,omitempty"`
	Recommendation  string  `json:"recommendation,omitempty"`
}

// OptimizerHintsResult contains optimizer hints
type OptimizerHintsResult struct {
	Hints []OptimizerHint `json:"hints"`
	Count int             `json:"count"`
	Note  string          `json:"note"`
}

func (t *OptimizerHintsTool) Name() string {
	return "get_optimizer_hints"
}

func (t *OptimizerHintsTool) Description() string {
	return "Show query optimizer statistics and hints that can help improve query performance"
}

func (t *OptimizerHintsTool) ActiveDescription() string {
	return "I'm pulling query optimizer statistics that might help improve performance"
}

func (t *OptimizerHintsTool) Parameters() map[string]interface{} {
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

func (t *OptimizerHintsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result OptimizerHintsResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	// Get table statistics that optimizer uses
	// Note: crdb_internal.table_row_statistics only has table_id, table_name, estimated_row_count
	// No database_name column available - this is per-database scoped table
	query := `
		SELECT
			table_name,
			'' as column_name,
			'row_count' as statistic,
			COALESCE(estimated_row_count, 0)::TEXT as value,
			'' as last_updated
		FROM crdb_internal.table_row_statistics
	`

	// Note: table_row_statistics doesn't have database_name, but is database-scoped
	// We can only filter by table name
	if table != "" {
		query += fmt.Sprintf(" WHERE table_name = '%s'", table)
	}

	// Ignore database filter since table_row_statistics doesn't have this column
	_ = database

	query += " ORDER BY table_name LIMIT 50"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		result.Note = "Optimizer statistics not available. Statistics may not be collected yet or require admin privileges."
		return result, nil
	}
	defer rows.Close()

	for rows.Next() {
		var hint OptimizerHint
		if err := rows.Scan(
			&hint.TableName,
			&hint.ColumnName,
			&hint.Statistic,
			&hint.Value,
			&hint.LastUpdated,
		); err != nil {
			return nil, fmt.Errorf("failed to scan optimizer hint row: %w", err)
		}

		// Generate recommendations based on statistics
		if hint.Statistic == "row_count" {
			if hint.Value == "0" {
				hint.Recommendation = "Table is empty - consider populating with data for accurate statistics"
			} else {
				hint.Recommendation = "Statistics are available for query optimization"
			}
		}

		result.Hints = append(result.Hints, hint)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating optimizer hint rows: %w", err)
	}

	result.Count = len(result.Hints)

	if result.Count == 0 {
		result.Note = "No optimizer statistics found. Run ANALYZE on tables to collect statistics for better query optimization."
	} else {
		result.Note = fmt.Sprintf("Found %d optimizer statistics. Keep statistics up-to-date with periodic ANALYZE for optimal query plans.", result.Count)
	}

	return result, nil
}
