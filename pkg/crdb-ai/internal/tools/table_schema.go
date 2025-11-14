package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TableSchemaTool provides detailed schema information for a table
type TableSchemaTool struct {
	db *pgxpool.Pool
}

// NewTableSchemaTool creates a new table schema tool
func NewTableSchemaTool(db *pgxpool.Pool) *TableSchemaTool {
	return &TableSchemaTool{db: db}
}

// IndexInfo represents information about a table index
type IndexInfo struct {
	IndexName   string `json:"index_name"`
	IndexType   string `json:"index_type"` // primary, unique, secondary
	IsUnique    bool   `json:"is_unique"`
	Columns     string `json:"columns"` // Will contain the index definition
}

// TableSchemaResult contains schema information for a table
type TableSchemaResult struct {
	TableName        string      `json:"table_name"`
	CreateStatement  string      `json:"create_statement"`
	Indexes          []IndexInfo `json:"indexes"`
}

func (t *TableSchemaTool) Name() string {
	return "get_table_schema"
}

func (t *TableSchemaTool) Description() string {
	return "Get detailed schema information for a specific table, including columns, indexes, and constraints. IMPORTANT: You must specify the database name - use the database where the table exists (e.g., 'tpcc', 'defaultdb'). Table lookups are database-scoped and will fail if you specify the wrong database."
}

func (t *TableSchemaTool) ActiveDescription() string {
	return "I'm pulling up the detailed schema for this table"
}

func (t *TableSchemaTool) Parameters() map[string]interface{} {
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

func (t *TableSchemaTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	tableName, ok := args["table"].(string)
	if !ok || tableName == "" {
		return nil, fmt.Errorf("table name is required")
	}

	database, ok := args["database"].(string)
	if !ok || database == "" {
		return nil, fmt.Errorf("database name is required")
	}

	var result TableSchemaResult
	result.TableName = tableName

	// Get CREATE TABLE statement using fully-qualified table name
	fullyQualifiedTable := fmt.Sprintf("%s.public.%s", database, tableName)
	createQuery := fmt.Sprintf("SHOW CREATE TABLE %s", fullyQualifiedTable)
	var tname, createStmt string
	if err := t.db.QueryRow(ctx, createQuery).Scan(&tname, &createStmt); err != nil {
		return nil, fmt.Errorf("failed to get create statement for table %s in database %s: %w", tableName, database, err)
	}
	result.CreateStatement = createStmt

	// Get index information
	// Note: We need to query from the correct database context
	// Use the fully qualified table name to look up indexes
	indexQuery := fmt.Sprintf(`
		SELECT
			index_name,
			index_type,
			is_unique,
			create_statement
		FROM %s.crdb_internal.table_indexes
		WHERE descriptor_name = $1
		ORDER BY index_id
	`, database)

	rows, err := t.db.Query(ctx, indexQuery, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to query indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var idx IndexInfo
		if err := rows.Scan(&idx.IndexName, &idx.IndexType, &idx.IsUnique, &idx.Columns); err != nil {
			return nil, fmt.Errorf("failed to scan index row: %w", err)
		}
		result.Indexes = append(result.Indexes, idx)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating index rows: %w", err)
	}

	return result, nil
}
