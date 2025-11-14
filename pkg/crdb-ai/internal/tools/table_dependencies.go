package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TableDependenciesTool shows foreign key relationships graph
type TableDependenciesTool struct {
	db *pgxpool.Pool
}

// NewTableDependenciesTool creates a new table dependencies tool
func NewTableDependenciesTool(db *pgxpool.Pool) *TableDependenciesTool {
	return &TableDependenciesTool{db: db}
}

// TableDependency represents a foreign key relationship
type TableDependency struct {
	SourceDatabase       string `json:"source_database"`
	SourceSchema         string `json:"source_schema"`
	SourceTable          string `json:"source_table"`
	SourceColumns        string `json:"source_columns"`
	TargetDatabase       string `json:"target_database"`
	TargetSchema         string `json:"target_schema"`
	TargetTable          string `json:"target_table"`
	TargetColumns        string `json:"target_columns"`
	ConstraintName       string `json:"constraint_name"`
	OnDelete             string `json:"on_delete"`
	OnUpdate             string `json:"on_update"`
}

// TableDependenciesResult contains table dependency information
type TableDependenciesResult struct {
	Dependencies []TableDependency `json:"dependencies"`
	Count        int               `json:"count"`
}

func (t *TableDependenciesTool) Name() string {
	return "get_table_dependencies"
}

func (t *TableDependenciesTool) Description() string {
	return "Show foreign key relationships and table dependencies to understand data relationships"
}

func (t *TableDependenciesTool) ActiveDescription() string {
	return "I'm digging up your foreign key relationships and table dependencies to better understand the data relationships"
}

func (t *TableDependenciesTool) Parameters() map[string]interface{} {
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

func (t *TableDependenciesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result TableDependenciesResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	// Query foreign key relationships
	query := `
		SELECT
			kcu.table_catalog as source_database,
			kcu.table_schema as source_schema,
			kcu.table_name as source_table,
			kcu.column_name as source_columns,
			ccu.table_catalog as target_database,
			ccu.table_schema as target_schema,
			ccu.table_name as target_table,
			ccu.column_name as target_columns,
			tc.constraint_name,
			rc.delete_rule as on_delete,
			rc.update_rule as on_update
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_catalog = kcu.constraint_catalog
			AND tc.constraint_schema = kcu.constraint_schema
			AND tc.constraint_name = kcu.constraint_name
		JOIN information_schema.referential_constraints rc
			ON tc.constraint_catalog = rc.constraint_catalog
			AND tc.constraint_schema = rc.constraint_schema
			AND tc.constraint_name = rc.constraint_name
		JOIN information_schema.constraint_column_usage ccu
			ON rc.unique_constraint_catalog = ccu.constraint_catalog
			AND rc.unique_constraint_schema = ccu.constraint_schema
			AND rc.unique_constraint_name = ccu.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
			AND kcu.table_schema NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND kcu.table_catalog = '%s'", database)
	}

	if table != "" {
		query += fmt.Sprintf(" AND (kcu.table_name = '%s' OR ccu.table_name = '%s')", table, table)
	}

	query += " ORDER BY kcu.table_catalog, kcu.table_name, tc.constraint_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query table dependencies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var td TableDependency
		if err := rows.Scan(
			&td.SourceDatabase,
			&td.SourceSchema,
			&td.SourceTable,
			&td.SourceColumns,
			&td.TargetDatabase,
			&td.TargetSchema,
			&td.TargetTable,
			&td.TargetColumns,
			&td.ConstraintName,
			&td.OnDelete,
			&td.OnUpdate,
		); err != nil {
			return nil, fmt.Errorf("failed to scan table dependency row: %w", err)
		}

		result.Dependencies = append(result.Dependencies, td)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating table dependency rows: %w", err)
	}

	result.Count = len(result.Dependencies)

	return result, nil
}
