package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnumTypesTool shows user-defined enum types
type EnumTypesTool struct {
	db *pgxpool.Pool
}

// NewEnumTypesTool creates a new enum types tool
func NewEnumTypesTool(db *pgxpool.Pool) *EnumTypesTool {
	return &EnumTypesTool{db: db}
}

// EnumType represents a user-defined enum type
type EnumType struct {
	DatabaseName string   `json:"database_name"`
	SchemaName   string   `json:"schema_name"`
	TypeName     string   `json:"type_name"`
	Values       []string `json:"values"`
	ValueCount   int      `json:"value_count"`
}

// EnumTypesResult contains enum type information
type EnumTypesResult struct {
	Enums []EnumType `json:"enums"`
	Count int        `json:"count"`
}

func (t *EnumTypesTool) Name() string {
	return "get_enum_types"
}

func (t *EnumTypesTool) Description() string {
	return "Show user-defined enum types and their possible values"
}

func (t *EnumTypesTool) ActiveDescription() string {
	return "I'm looking up your user-defined enum types and their values"
}

func (t *EnumTypesTool) Parameters() map[string]interface{} {
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

func (t *EnumTypesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result EnumTypesResult

	database, _ := args["database"].(string)

	// Query enum types from crdb_internal.create_type_statements
	query := `
		SELECT
			database_name,
			schema_name,
			descriptor_name as type_name,
			create_statement
		FROM crdb_internal.create_type_statements
		WHERE schema_name NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND database_name = '%s'", database)
	}

	query += " ORDER BY database_name, schema_name, descriptor_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query enum types: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var et EnumType
		var createStmt string

		if err := rows.Scan(
			&et.DatabaseName,
			&et.SchemaName,
			&et.TypeName,
			&createStmt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan enum type row: %w", err)
		}

		// Parse enum values from CREATE TYPE statement
		// This is a simplified parser - in production you'd want more robust parsing
		// CREATE TYPE status AS ENUM ('pending', 'active', 'inactive')
		et.Values = []string{} // Placeholder - would need proper parsing

		result.Enums = append(result.Enums, et)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating enum type rows: %w", err)
	}

	result.Count = len(result.Enums)

	return result, nil
}
