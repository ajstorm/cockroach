package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UserGrantsTool shows user privileges and grants
type UserGrantsTool struct {
	db *pgxpool.Pool
}

// NewUserGrantsTool creates a new user grants tool
func NewUserGrantsTool(db *pgxpool.Pool) *UserGrantsTool {
	return &UserGrantsTool{db: db}
}

// UserGrant represents a user privilege grant
type UserGrant struct {
	Grantee       string `json:"grantee"`
	TableCatalog  string `json:"table_catalog"`
	TableSchema   string `json:"table_schema"`
	TableName     string `json:"table_name"`
	PrivilegeType string `json:"privilege_type"`
	IsGrantable   bool   `json:"is_grantable"`
}

// UserGrantsResult contains user grants information
type UserGrantsResult struct {
	Grants  []UserGrant `json:"grants"`
	Count   int         `json:"count"`
	ByUser  map[string]int `json:"by_user"`
}

func (t *UserGrantsTool) Name() string {
	return "get_user_grants"
}

func (t *UserGrantsTool) Description() string {
	return "Show user privileges and grants across databases and tables"
}

func (t *UserGrantsTool) ActiveDescription() string {
	return "I'm reviewing user privileges and grants"
}

func (t *UserGrantsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user": map[string]interface{}{
				"type":        "string",
				"description": "Filter by username (optional)",
			},
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *UserGrantsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result UserGrantsResult
	result.ByUser = make(map[string]int)

	user, _ := args["user"].(string)
	database, _ := args["database"].(string)

	query := `
		SELECT
			grantee,
			table_catalog,
			table_schema,
			table_name,
			privilege_type,
			is_grantable = 'YES' as is_grantable
		FROM information_schema.table_privileges
		WHERE table_schema NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if user != "" {
		query += fmt.Sprintf(" AND grantee = '%s'", user)
	}

	if database != "" {
		query += fmt.Sprintf(" AND table_catalog = '%s'", database)
	}

	query += " ORDER BY grantee, table_catalog, table_name, privilege_type"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query user grants: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ug UserGrant
		if err := rows.Scan(
			&ug.Grantee,
			&ug.TableCatalog,
			&ug.TableSchema,
			&ug.TableName,
			&ug.PrivilegeType,
			&ug.IsGrantable,
		); err != nil {
			return nil, fmt.Errorf("failed to scan user grant row: %w", err)
		}

		result.Grants = append(result.Grants, ug)
		result.ByUser[ug.Grantee]++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user grant rows: %w", err)
	}

	result.Count = len(result.Grants)

	return result, nil
}
