package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RoleMembershipsTool shows role hierarchy and memberships
type RoleMembershipsTool struct {
	db *pgxpool.Pool
}

// NewRoleMembershipsTool creates a new role memberships tool
func NewRoleMembershipsTool(db *pgxpool.Pool) *RoleMembershipsTool {
	return &RoleMembershipsTool{db: db}
}

// RoleMembership represents a role membership relationship
type RoleMembership struct {
	Role       string `json:"role"`
	Member     string `json:"member"`
	IsAdmin    bool   `json:"is_admin"`
}

// RoleMembershipsResult contains role membership information
type RoleMembershipsResult struct {
	Memberships []RoleMembership `json:"memberships"`
	Roles       []string         `json:"roles"`
	Count       int              `json:"count"`
}

func (t *RoleMembershipsTool) Name() string {
	return "get_role_memberships"
}

func (t *RoleMembershipsTool) Description() string {
	return "Show role hierarchy and membership relationships including admin privileges"
}

func (t *RoleMembershipsTool) ActiveDescription() string {
	return "I'm looking up role hierarchies and membership relationships"
}

func (t *RoleMembershipsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"role": map[string]interface{}{
				"type":        "string",
				"description": "Filter by role name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *RoleMembershipsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result RoleMembershipsResult
	roleFilter, _ := args["role"].(string)

	// Get role memberships from system.role_members
	// Note: Column names "role", "member", and "isAdmin" need quotes since they're reserved keywords
	query := `
		SELECT
			"role",
			"member",
			"isAdmin"
		FROM system.role_members
	`

	if roleFilter != "" {
		query += fmt.Sprintf(` WHERE "role" = '%s' OR "member" = '%s'`, roleFilter, roleFilter)
	}

	query += ` ORDER BY "role", "member"`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query role memberships: %w", err)
	}
	defer rows.Close()

	rolesSet := make(map[string]bool)

	for rows.Next() {
		var rm RoleMembership
		if err := rows.Scan(
			&rm.Role,
			&rm.Member,
			&rm.IsAdmin,
		); err != nil {
			return nil, fmt.Errorf("failed to scan role membership row: %w", err)
		}

		result.Memberships = append(result.Memberships, rm)
		rolesSet[rm.Role] = true
		rolesSet[rm.Member] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating role membership rows: %w", err)
	}

	// Convert set to slice
	for role := range rolesSet {
		result.Roles = append(result.Roles, role)
	}

	result.Count = len(result.Memberships)

	return result, nil
}
