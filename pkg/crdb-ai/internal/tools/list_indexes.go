package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListIndexesTool lists indexes for tables
type ListIndexesTool struct {
	db *pgxpool.Pool
}

// NewListIndexesTool creates a new list indexes tool
func NewListIndexesTool(db *pgxpool.Pool) *ListIndexesTool {
	return &ListIndexesTool{db: db}
}

// IndexDetail represents detailed information about an index
type IndexDetail struct {
	TableName   string `json:"table_name"`
	IndexName   string `json:"index_name"`
	IndexType   string `json:"index_type"`
	IsUnique    bool   `json:"is_unique"`
	Definition  string `json:"definition"`
}

// ListIndexesResult contains list of indexes
type ListIndexesResult struct {
	Indexes []IndexDetail `json:"indexes"`
}

func (t *ListIndexesTool) Name() string {
	return "list_indexes"
}

func (t *ListIndexesTool) Description() string {
	return `List all indexes in a database or for a specific table.

This tool shows:
- Index names and types (primary, secondary, inverted, etc.)
- Whether indexes are unique
- Index definitions (columns and ordering)
- All indexes across tables for analysis

Use this when:
- Verifying expected indexes exist
- Auditing index coverage
- Finding redundant or missing indexes
- Understanding table index structure

Results include index type (primary, secondary, etc.) and full CREATE INDEX statements.`
}

func (t *ListIndexesTool) ActiveDescription() string {
	return "I'm listing all the indexes in your database"
}

func (t *ListIndexesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Database name (optional - if not provided, uses current database)",
			},
			"table": map[string]interface{}{
				"type":        "string",
				"description": "Table name (optional - if not provided, lists all indexes in the database)",
			},
		},
		"required": []string{},
	}
}

func (t *ListIndexesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ListIndexesResult

	// Get database name (optional)
	var dbName string
	if db, ok := args["database"].(string); ok && db != "" {
		dbName = db
	}

	// Get table name (optional)
	tableName, hasTable := args["table"].(string)

	// Build query based on parameters
	// Note: crdb_internal.table_indexes shows indexes from the current database only
	// descriptor_name is the table name
	var query string
	var queryArgs []interface{}
	argNum := 1

	// If a specific database is requested, we need to query it directly
	if dbName != "" {
		// Switch to the specified database context for the query
		if hasTable && tableName != "" {
			query = fmt.Sprintf(`
				SELECT
					descriptor_name,
					index_name,
					index_type,
					is_unique,
					create_statement
				FROM %s.crdb_internal.table_indexes
				WHERE descriptor_name = $%d
				ORDER BY index_id
			`, dbName, argNum)
			queryArgs = []interface{}{tableName}
		} else {
			query = fmt.Sprintf(`
				SELECT
					descriptor_name,
					index_name,
					index_type,
					is_unique,
					create_statement
				FROM %s.crdb_internal.table_indexes
				ORDER BY descriptor_name, index_id
			`, dbName)
		}
	} else {
		// Use current database
		if hasTable && tableName != "" {
			query = fmt.Sprintf(`
				SELECT
					descriptor_name,
					index_name,
					index_type,
					is_unique,
					create_statement
				FROM crdb_internal.table_indexes
				WHERE descriptor_name = $%d
				ORDER BY index_id
			`, argNum)
			queryArgs = []interface{}{tableName}
		} else {
			query = `
				SELECT
					descriptor_name,
					index_name,
					index_type,
					is_unique,
					create_statement
				FROM crdb_internal.table_indexes
				ORDER BY descriptor_name, index_id
			`
		}
	}

	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var idx IndexDetail
		if err := rows.Scan(
			&idx.TableName,
			&idx.IndexName,
			&idx.IndexType,
			&idx.IsUnique,
			&idx.Definition,
		); err != nil {
			return nil, fmt.Errorf("failed to scan index row: %w", err)
		}
		result.Indexes = append(result.Indexes, idx)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating index rows: %w", err)
	}

	return result, nil
}
