package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListTablesTool lists databases and tables in the cluster
type ListTablesTool struct {
	db *pgxpool.Pool
}

// NewListTablesTool creates a new list tables tool
func NewListTablesTool(db *pgxpool.Pool) *ListTablesTool {
	return &ListTablesTool{db: db}
}

// DatabaseInfo represents information about a database
type DatabaseInfo struct {
	DatabaseName string   `json:"database_name"`
	Tables       []string `json:"tables"`
}

// ListTablesResult contains list of databases and tables
type ListTablesResult struct {
	Databases []DatabaseInfo `json:"databases"`
}

func (t *ListTablesTool) Name() string {
	return "list_tables"
}

func (t *ListTablesTool) Description() string {
	return "List all databases and their tables in the cluster"
}

func (t *ListTablesTool) ActiveDescription() string {
	return "I'm pulling up all databases and tables in your cluster"
}

func (t *ListTablesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *ListTablesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ListTablesResult

	// First get all databases (excluding system databases)
	dbQuery := `
		SELECT database_name
		FROM [SHOW DATABASES]
		WHERE database_name NOT IN ('system', 'information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
		ORDER BY database_name
	`

	dbRows, err := t.db.Query(ctx, dbQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query databases: %w", err)
	}
	defer dbRows.Close()

	var databases []string
	for dbRows.Next() {
		var dbName string
		if err := dbRows.Scan(&dbName); err != nil {
			return nil, fmt.Errorf("failed to scan database row: %w", err)
		}
		databases = append(databases, dbName)
	}

	if err := dbRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating database rows: %w", err)
	}

	// For each database, get its tables
	for _, dbName := range databases {
		var dbInfo DatabaseInfo
		dbInfo.DatabaseName = dbName

		// Query tables in this database
		tableQuery := fmt.Sprintf("SELECT table_name FROM [SHOW TABLES FROM %s] ORDER BY table_name", dbName)

		tableRows, err := t.db.Query(ctx, tableQuery)
		if err != nil {
			// If we can't access the database, skip it
			continue
		}

		for tableRows.Next() {
			var tableName string
			if err := tableRows.Scan(&tableName); err != nil {
				tableRows.Close()
				return nil, fmt.Errorf("failed to scan table row: %w", err)
			}
			dbInfo.Tables = append(dbInfo.Tables, tableName)
		}
		tableRows.Close()

		if err := tableRows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating table rows: %w", err)
		}

		result.Databases = append(result.Databases, dbInfo)
	}

	return result, nil
}
