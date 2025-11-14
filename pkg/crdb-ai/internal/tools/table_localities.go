package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TableLocalit iesTool shows table locality configurations
type TableLocalitiesTool struct {
	db *pgxpool.Pool
}

// NewTableLocalitiesTool creates a new table localities tool
func NewTableLocalitiesTool(db *pgxpool.Pool) *TableLocalitiesTool {
	return &TableLocalitiesTool{db: db}
}

// TableLocality represents locality information for a table
type TableLocality struct {
	DatabaseName string `json:"database_name"`
	SchemaName   string `json:"schema_name"`
	TableName    string `json:"table_name"`
	Locality     string `json:"locality"`
	RegionConfig string `json:"region_config,omitempty"`
}

// TableLocalitiesResult contains table locality information
type TableLocalitiesResult struct {
	Tables []TableLocality `json:"tables"`
	Count  int             `json:"count"`
	Note   string          `json:"note,omitempty"`
}

func (t *TableLocalitiesTool) Name() string {
	return "get_table_localities"
}

func (t *TableLocalitiesTool) Description() string {
	return "Show table locality configurations for multi-region deployments (REGIONAL BY TABLE, GLOBAL, etc.)"
}

func (t *TableLocalitiesTool) ActiveDescription() string {
	return "I'm checking table locality configurations for your multi-region setup"
}

func (t *TableLocalitiesTool) Parameters() map[string]interface{} {
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

func (t *TableLocalitiesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result TableLocalitiesResult

	database, _ := args["database"].(string)

	// Try to get locality information from crdb_internal.tables
	query := `
		SELECT
			database_name,
			schema_name,
			name as table_name,
			locality
		FROM crdb_internal.tables
		WHERE schema_name NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
			AND locality IS NOT NULL
	`

	if database != "" {
		query += fmt.Sprintf(" AND database_name = '%s'", database)
	}

	query += " ORDER BY database_name, schema_name, name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		// Locality might not be available in older versions
		result.Note = "Table locality information not available. This feature requires multi-region configuration."
		return result, nil
	}
	defer rows.Close()

	for rows.Next() {
		var tl TableLocality
		var locality *string

		if err := rows.Scan(
			&tl.DatabaseName,
			&tl.SchemaName,
			&tl.TableName,
			&locality,
		); err != nil {
			return nil, fmt.Errorf("failed to scan table locality row: %w", err)
		}

		if locality != nil {
			tl.Locality = *locality
		}

		result.Tables = append(result.Tables, tl)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating table locality rows: %w", err)
	}

	result.Count = len(result.Tables)

	if result.Count == 0 {
		result.Note = "No multi-region tables found. Cluster may not be configured for multi-region."
	}

	return result, nil
}
