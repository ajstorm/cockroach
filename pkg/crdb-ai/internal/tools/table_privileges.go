package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TablePrivilegesTool checks table-level permissions
type TablePrivilegesTool struct {
	db *pgxpool.Pool
}

// NewTablePrivilegesTool creates a new table privileges tool
func NewTablePrivilegesTool(db *pgxpool.Pool) *TablePrivilegesTool {
	return &TablePrivilegesTool{db: db}
}

// TablePrivilege represents privileges on a specific table
type TablePrivilege struct {
	DatabaseName  string   `json:"database_name"`
	SchemaName    string   `json:"schema_name"`
	TableName     string   `json:"table_name"`
	Grantees      []string `json:"grantees"`
	Privileges    []string `json:"privileges"`
}

// TablePrivilegesResult contains table privilege information
type TablePrivilegesResult struct {
	Tables []TablePrivilege `json:"tables"`
	Count  int              `json:"count"`
}

func (t *TablePrivilegesTool) Name() string {
	return "check_table_privileges"
}

func (t *TablePrivilegesTool) Description() string {
	return "Check table-level permissions showing which users have access to which tables"
}

func (t *TablePrivilegesTool) ActiveDescription() string {
	return "I'm reviewing table-level permissions and access rights"
}

func (t *TablePrivilegesTool) Parameters() map[string]interface{} {
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

func (t *TablePrivilegesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result TablePrivilegesResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	query := `
		SELECT
			table_catalog,
			table_schema,
			table_name,
			array_agg(DISTINCT grantee) as grantees,
			array_agg(DISTINCT privilege_type) as privileges
		FROM information_schema.table_privileges
		WHERE table_schema NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND table_catalog = '%s'", database)
	}

	if table != "" {
		query += fmt.Sprintf(" AND table_name = '%s'", table)
	}

	query += " GROUP BY table_catalog, table_schema, table_name ORDER BY table_catalog, table_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query table privileges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tp TablePrivilege
		if err := rows.Scan(
			&tp.DatabaseName,
			&tp.SchemaName,
			&tp.TableName,
			&tp.Grantees,
			&tp.Privileges,
		); err != nil {
			return nil, fmt.Errorf("failed to scan table privilege row: %w", err)
		}

		result.Tables = append(result.Tables, tp)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating table privilege rows: %w", err)
	}

	result.Count = len(result.Tables)

	return result, nil
}
