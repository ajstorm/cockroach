package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionInfoTool shows active sessions and connections
type SessionInfoTool struct {
	db *pgxpool.Pool
}

// NewSessionInfoTool creates a new session info tool
func NewSessionInfoTool(db *pgxpool.Pool) *SessionInfoTool {
	return &SessionInfoTool{db: db}
}

// SessionInfo represents information about a session
type SessionInfo struct {
	SessionID       string     `json:"session_id"`
	NodeID          int        `json:"node_id"`
	UserName        string     `json:"user_name"`
	ClientAddress   string     `json:"client_address"`
	ApplicationName string     `json:"application_name"`
	ActiveQuery     string     `json:"active_query,omitempty"`
	SessionStart    time.Time  `json:"session_start"`
	ActiveQueryStart *time.Time `json:"active_query_start,omitempty"`
	Status          string     `json:"status"`
}

// SessionInfoResult contains session information
type SessionInfoResult struct {
	Sessions      []SessionInfo `json:"sessions"`
	TotalSessions int           `json:"total_sessions"`
	ActiveSessions int          `json:"active_sessions"`
	IdleSessions  int           `json:"idle_sessions"`
}

func (t *SessionInfoTool) Name() string {
	return "get_session_info"
}

func (t *SessionInfoTool) Description() string {
	return "Show information about active database sessions and connections"
}

func (t *SessionInfoTool) ActiveDescription() string {
	return "I'm checking active database sessions and connections"
}

func (t *SessionInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user_name": map[string]interface{}{
				"type":        "string",
				"description": "Filter by username (optional)",
			},
			"active_only": map[string]interface{}{
				"type":        "boolean",
				"description": "Show only sessions with active queries (default: false)",
			},
		},
		"required": []string{},
	}
}

func (t *SessionInfoTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result SessionInfoResult

	userName, _ := args["user_name"].(string)
	activeOnly := false
	if v, ok := args["active_only"].(bool); ok {
		activeOnly = v
	}

	query := `
		SELECT
			session_id,
			node_id,
			user_name,
			client_address,
			application_name,
			active_queries,
			session_start,
			active_query_start,
			status
		FROM crdb_internal.cluster_sessions
		WHERE 1=1
	`

	if userName != "" {
		query += fmt.Sprintf(" AND user_name = '%s'", userName)
	}

	if activeOnly {
		query += " AND status = 'active'"
	}

	query += " ORDER BY session_start DESC LIMIT 100"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query session info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var si SessionInfo
		var activeQuery *string
		var activeQueryStart *time.Time

		if err := rows.Scan(
			&si.SessionID,
			&si.NodeID,
			&si.UserName,
			&si.ClientAddress,
			&si.ApplicationName,
			&activeQuery,
			&si.SessionStart,
			&activeQueryStart,
			&si.Status,
		); err != nil {
			return nil, fmt.Errorf("failed to scan session info row: %w", err)
		}

		if activeQuery != nil {
			si.ActiveQuery = *activeQuery
		}
		si.ActiveQueryStart = activeQueryStart

		result.Sessions = append(result.Sessions, si)
		result.TotalSessions++

		if si.Status == "active" {
			result.ActiveSessions++
		} else {
			result.IdleSessions++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating session info rows: %w", err)
	}

	return result, nil
}
