package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthenticationMethodsTool shows authentication methods and configuration
type AuthenticationMethodsTool struct {
	db *pgxpool.Pool
}

// NewAuthenticationMethodsTool creates a new authentication methods tool
func NewAuthenticationMethodsTool(db *pgxpool.Pool) *AuthenticationMethodsTool {
	return &AuthenticationMethodsTool{db: db}
}

// AuthMethod represents an authentication method configuration
type AuthMethod struct {
	Username     string `json:"username"`
	Options      string `json:"options"`
	Method       string `json:"method"`
}

// AuthenticationMethodsResult contains authentication method information
type AuthenticationMethodsResult struct {
	Methods   []AuthMethod `json:"methods"`
	Count     int          `json:"count"`
	Note      string       `json:"note"`
}

func (t *AuthenticationMethodsTool) Name() string {
	return "get_authentication_methods"
}

func (t *AuthenticationMethodsTool) Description() string {
	return "Show authentication methods and configuration for database users"
}

func (t *AuthenticationMethodsTool) ActiveDescription() string {
	return "I'm checking your authentication methods and user configurations"
}

func (t *AuthenticationMethodsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *AuthenticationMethodsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result AuthenticationMethodsResult

	// Get authentication info from pg_hba_file_rules (if available)
	// Or from cluster settings for authentication configuration
	query := `
		SELECT
			variable,
			value
		FROM crdb_internal.cluster_settings
		WHERE variable LIKE '%auth%' OR variable LIKE '%security%'
		ORDER BY variable
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query authentication methods: %w", err)
	}
	defer rows.Close()

	authSettings := make(map[string]string)

	for rows.Next() {
		var variable, value string
		if err := rows.Scan(&variable, &value); err != nil {
			return nil, fmt.Errorf("failed to scan authentication setting row: %w", err)
		}
		authSettings[variable] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating authentication setting rows: %w", err)
	}

	// Get user information from system.users joined with role_options
	// Note: system.users doesn't have an 'options' column - role options are in system.role_options
	userQuery := `
		SELECT
			u.username,
			COALESCE(string_agg(ro.option || '=' || COALESCE(ro.value, ''), ','), '') as options,
			CASE
				WHEN EXISTS (SELECT 1 FROM system.role_options r WHERE r.username = u.username AND r.option = 'PASSWORD') THEN 'password'
				ELSE 'default'
			END as method
		FROM system.users u
		LEFT JOIN system.role_options ro ON u.username = ro.username
		WHERE u.username NOT IN ('node', 'root')
		GROUP BY u.username
		ORDER BY u.username
	`

	userRows, err := t.db.Query(ctx, userQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer userRows.Close()

	for userRows.Next() {
		var am AuthMethod

		if err := userRows.Scan(&am.Username, &am.Options, &am.Method); err != nil {
			return nil, fmt.Errorf("failed to scan user row: %w", err)
		}

		result.Methods = append(result.Methods, am)
	}

	if err := userRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user rows: %w", err)
	}

	result.Count = len(result.Methods)

	// Generate note based on settings
	if len(authSettings) > 0 {
		result.Note = fmt.Sprintf("Found %d users with authentication configured. Check cluster settings for detailed auth configuration.", result.Count)
	} else {
		result.Note = fmt.Sprintf("Found %d users. Authentication settings may require admin privileges to view.", result.Count)
	}

	return result, nil
}
