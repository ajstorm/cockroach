package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ViewDefinitionsTool shows view definitions and dependencies
type ViewDefinitionsTool struct {
	db *pgxpool.Pool
}

// NewViewDefinitionsTool creates a new view definitions tool
func NewViewDefinitionsTool(db *pgxpool.Pool) *ViewDefinitionsTool {
	return &ViewDefinitionsTool{db: db}
}

// ViewDefinition represents a view and its definition
type ViewDefinition struct {
	DatabaseName   string `json:"database_name"`
	SchemaName     string `json:"schema_name"`
	ViewName       string `json:"view_name"`
	Definition     string `json:"definition"`
	IsMaterialized bool   `json:"is_materialized"`
}

// ViewDefinitionsResult contains view definitions
type ViewDefinitionsResult struct {
	Views []ViewDefinition `json:"views"`
	Count int              `json:"count"`
}

func (t *ViewDefinitionsTool) Name() string {
	return "get_view_definitions"
}

func (t *ViewDefinitionsTool) Description() string {
	return "Show view definitions and their SQL including dependencies"
}

func (t *ViewDefinitionsTool) ActiveDescription() string {
	return "I'm looking up view definitions and their SQL"
}

func (t *ViewDefinitionsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
			"view": map[string]interface{}{
				"type":        "string",
				"description": "Filter by view name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *ViewDefinitionsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ViewDefinitionsResult

	database, _ := args["database"].(string)
	view, _ := args["view"].(string)

	query := `
		SELECT
			table_catalog as database_name,
			table_schema as schema_name,
			table_name as view_name,
			view_definition as definition,
			false as is_materialized
		FROM information_schema.views
		WHERE table_schema NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND table_catalog = '%s'", database)
	}

	if view != "" {
		query += fmt.Sprintf(" AND table_name = '%s'", view)
	}

	query += " ORDER BY table_catalog, table_schema, table_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query view definitions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var vd ViewDefinition
		if err := rows.Scan(
			&vd.DatabaseName,
			&vd.SchemaName,
			&vd.ViewName,
			&vd.Definition,
			&vd.IsMaterialized,
		); err != nil {
			return nil, fmt.Errorf("failed to scan view definition row: %w", err)
		}

		result.Views = append(result.Views, vd)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating view definition rows: %w", err)
	}

	result.Count = len(result.Views)

	return result, nil
}
