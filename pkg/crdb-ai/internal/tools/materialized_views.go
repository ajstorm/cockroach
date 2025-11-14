package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MaterializedViewsTool shows materialized view status
type MaterializedViewsTool struct {
	db *pgxpool.Pool
}

// NewMaterializedViewsTool creates a new materialized views tool
func NewMaterializedViewsTool(db *pgxpool.Pool) *MaterializedViewsTool {
	return &MaterializedViewsTool{db: db}
}

// MaterializedViewInfo represents a materialized view
type MaterializedViewInfo struct {
	DatabaseName   string     `json:"database_name"`
	SchemaName     string     `json:"schema_name"`
	ViewName       string     `json:"view_name"`
	Definition     string     `json:"definition"`
	LastRefreshed  *time.Time `json:"last_refreshed,omitempty"`
	SizeBytes      int64      `json:"size_bytes"`
}

// MaterializedViewsResult contains materialized view information
type MaterializedViewsResult struct {
	Views []MaterializedViewInfo `json:"views"`
	Count int                    `json:"count"`
	Note  string                 `json:"note,omitempty"`
}

func (t *MaterializedViewsTool) Name() string {
	return "get_materialized_views"
}

func (t *MaterializedViewsTool) Description() string {
	return "Show materialized view status including refresh times and sizes"
}

func (t *MaterializedViewsTool) ActiveDescription() string {
	return "I'm reviewing your materialized views and their refresh status"
}

func (t *MaterializedViewsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *MaterializedViewsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result MaterializedViewsResult

	database, _ := args["database"].(string)

	// CockroachDB doesn't have native materialized views yet, so check tables that might act as such
	// We can look for tables created from CREATE TABLE AS SELECT patterns
	// Note: crdb_internal.tables doesn't have create_statement column - use crdb_internal.create_statements instead
	query := `
		SELECT
			database_name,
			schema_name,
			descriptor_name as table_name,
			create_statement as definition,
			0 as size_bytes
		FROM crdb_internal.create_statements
		WHERE schema_name NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
			AND descriptor_type = 'table'
			AND create_statement LIKE '%AS SELECT%'
	`

	if database != "" {
		query += fmt.Sprintf(" AND database_name = '%s'", database)
	}

	query += " ORDER BY database_name, schema_name, descriptor_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		result.Note = "Materialized views not available or not supported in this CockroachDB version"
		return result, nil
	}
	defer rows.Close()

	for rows.Next() {
		var mvi MaterializedViewInfo
		if err := rows.Scan(
			&mvi.DatabaseName,
			&mvi.SchemaName,
			&mvi.ViewName,
			&mvi.Definition,
			&mvi.SizeBytes,
		); err != nil {
			return nil, fmt.Errorf("failed to scan materialized view row: %w", err)
		}

		result.Views = append(result.Views, mvi)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating materialized view rows: %w", err)
	}

	result.Count = len(result.Views)

	if result.Count == 0 {
		result.Note = "No materialized views found. CockroachDB does not yet support native materialized views. Consider using scheduled refresh jobs with regular tables."
	}

	return result, nil
}
