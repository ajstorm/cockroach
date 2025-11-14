package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConstraintsTool lists table constraints (foreign keys, checks, etc.)
type ConstraintsTool struct {
	db *pgxpool.Pool
}

// NewConstraintsTool creates a new constraints tool
func NewConstraintsTool(db *pgxpool.Pool) *ConstraintsTool {
	return &ConstraintsTool{db: db}
}

// ConstraintInfo represents a table constraint
type ConstraintInfo struct {
	DatabaseName   string `json:"database_name"`
	SchemaName     string `json:"schema_name"`
	TableName      string `json:"table_name"`
	ConstraintName string `json:"constraint_name"`
	ConstraintType string `json:"constraint_type"`
	Details        string `json:"details"`
	Validated      bool   `json:"validated"`
}

// ConstraintsResult contains constraint information
type ConstraintsResult struct {
	Constraints []ConstraintInfo `json:"constraints"`
	Count       int              `json:"count"`
	ByType      map[string]int   `json:"by_type"`
}

func (t *ConstraintsTool) Name() string {
	return "get_constraints"
}

func (t *ConstraintsTool) Description() string {
	return "List all constraints (foreign keys, check constraints, unique constraints) on tables"
}

func (t *ConstraintsTool) ActiveDescription() string {
	return "I'm listing all the constraints on your tables"
}

func (t *ConstraintsTool) Parameters() map[string]interface{} {
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
			"constraint_type": map[string]interface{}{
				"type":        "string",
				"description": "Filter by constraint type: 'foreign_key', 'check', 'unique', 'primary_key' (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *ConstraintsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ConstraintsResult
	result.ByType = make(map[string]int)

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)
	constraintType, _ := args["constraint_type"].(string)

	// Note: information_schema.table_constraints does not have check_clause column.
	// To get check constraint details, we'd need to join with check_constraints table.
	// For now, we return constraint_name as details since it's descriptive.
	query := `
		SELECT
			tc.table_catalog as database_name,
			tc.table_schema as schema_name,
			tc.table_name,
			tc.constraint_name,
			tc.constraint_type,
			COALESCE(cc.check_clause, '') as details,
			tc.is_deferrable = 'NO' as validated
		FROM information_schema.table_constraints tc
		LEFT JOIN information_schema.check_constraints cc
			ON tc.constraint_catalog = cc.constraint_catalog
			AND tc.constraint_schema = cc.constraint_schema
			AND tc.constraint_name = cc.constraint_name
		WHERE tc.table_schema NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND tc.table_catalog = '%s'", database)
	}

	if table != "" {
		query += fmt.Sprintf(" AND tc.table_name = '%s'", table)
	}

	if constraintType != "" {
		// Map friendly names to SQL constraint types
		sqlType := constraintType
		switch constraintType {
		case "foreign_key":
			sqlType = "FOREIGN KEY"
		case "check":
			sqlType = "CHECK"
		case "unique":
			sqlType = "UNIQUE"
		case "primary_key":
			sqlType = "PRIMARY KEY"
		}
		query += fmt.Sprintf(" AND tc.constraint_type = '%s'", sqlType)
	}

	query += " ORDER BY tc.table_catalog, tc.table_name, tc.constraint_type, tc.constraint_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query constraints: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ci ConstraintInfo
		if err := rows.Scan(
			&ci.DatabaseName,
			&ci.SchemaName,
			&ci.TableName,
			&ci.ConstraintName,
			&ci.ConstraintType,
			&ci.Details,
			&ci.Validated,
		); err != nil {
			return nil, fmt.Errorf("failed to scan constraint row: %w", err)
		}

		result.Constraints = append(result.Constraints, ci)
		result.ByType[ci.ConstraintType]++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating constraint rows: %w", err)
	}

	result.Count = len(result.Constraints)

	return result, nil
}
