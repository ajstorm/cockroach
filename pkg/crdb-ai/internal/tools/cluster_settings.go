package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClusterSettingsTool shows cluster settings
type ClusterSettingsTool struct {
	db *pgxpool.Pool
}

// NewClusterSettingsTool creates a new cluster settings tool
func NewClusterSettingsTool(db *pgxpool.Pool) *ClusterSettingsTool {
	return &ClusterSettingsTool{db: db}
}

// ClusterSetting represents a cluster setting
type ClusterSetting struct {
	Variable    string `json:"variable"`
	Value       string `json:"value"`
	SettingType string `json:"setting_type"`
	Description string `json:"description"`
	IsPublic    bool   `json:"is_public"`
}

// ClusterSettingsResult contains cluster settings
type ClusterSettingsResult struct {
	Settings []ClusterSetting `json:"settings"`
	Count    int              `json:"count"`
	Note     string           `json:"note,omitempty"`
}

func (t *ClusterSettingsTool) Name() string {
	return "get_cluster_settings"
}

func (t *ClusterSettingsTool) Description() string {
	return "Show cluster settings and their current values. Useful for understanding cluster configuration."
}

func (t *ClusterSettingsTool) ActiveDescription() string {
	return "I'm reviewing your cluster settings and configurations"
}

func (t *ClusterSettingsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Filter settings by name pattern (optional, e.g., 'sql', 'kv', 'server')",
			},
			"public_only": map[string]interface{}{
				"type":        "boolean",
				"description": "Show only public settings (default: true)",
			},
		},
		"required": []string{},
	}
}

func (t *ClusterSettingsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ClusterSettingsResult

	pattern, _ := args["pattern"].(string)
	publicOnly := true
	if v, ok := args["public_only"].(bool); ok {
		publicOnly = v
	}

	query := `
		SELECT
			variable,
			value,
			type as setting_type,
			description,
			public
		FROM crdb_internal.cluster_settings
		WHERE 1=1
	`

	if pattern != "" {
		query += fmt.Sprintf(" AND variable ILIKE '%%%s%%'", pattern)
	}

	if publicOnly {
		query += " AND public = true"
	}

	query += " ORDER BY variable LIMIT 100"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query cluster settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cs ClusterSetting
		if err := rows.Scan(
			&cs.Variable,
			&cs.Value,
			&cs.SettingType,
			&cs.Description,
			&cs.IsPublic,
		); err != nil {
			return nil, fmt.Errorf("failed to scan cluster setting row: %w", err)
		}

		result.Settings = append(result.Settings, cs)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating cluster setting rows: %w", err)
	}

	result.Count = len(result.Settings)

	if pattern != "" {
		result.Note = fmt.Sprintf("Showing settings matching pattern '%s'", pattern)
	}

	return result, nil
}
