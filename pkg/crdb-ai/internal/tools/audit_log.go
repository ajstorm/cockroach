package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditLogTool shows security audit log entries
type AuditLogTool struct {
	db *pgxpool.Pool
}

// NewAuditLogTool creates a new audit log tool
func NewAuditLogTool(db *pgxpool.Pool) *AuditLogTool {
	return &AuditLogTool{db: db}
}

// AuditLogEntry represents a security audit log entry
type AuditLogEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	EventType    string    `json:"event_type"`
	User         string    `json:"user"`
	Database     string    `json:"database,omitempty"`
	Statement    string    `json:"statement,omitempty"`
	Tag          string    `json:"tag,omitempty"`
	Application  string    `json:"application,omitempty"`
}

// AuditLogResult contains audit log entries
type AuditLogResult struct {
	Entries []AuditLogEntry `json:"entries"`
	Count   int             `json:"count"`
	Note    string          `json:"note"`
}

func (t *AuditLogTool) Name() string {
	return "get_audit_log"
}

func (t *AuditLogTool) Description() string {
	return "Show security audit log entries for tracking user activities and security events"
}

func (t *AuditLogTool) ActiveDescription() string {
	return "I'm pulling up your security audit log to track user activities and security events"
}

func (t *AuditLogTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user": map[string]interface{}{
				"type":        "string",
				"description": "Filter by username (optional)",
			},
			"hours": map[string]interface{}{
				"type":        "integer",
				"description": "How many hours back to look (default: 24)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of entries to return (default: 100)",
			},
		},
		"required": []string{},
	}
}

func (t *AuditLogTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result AuditLogResult

	user, _ := args["user"].(string)
	hours := 24
	if h, ok := args["hours"].(float64); ok {
		hours = int(h)
	}

	limit := 100
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Try to query audit log from system.eventlog
	// Note: targetID column is deprecated (always 0 as of v22.2)
	// info column is STRING (may contain JSON), payload column is JSONB
	// We extract user from payload->>'User' or info parsed as JSONB
	query := fmt.Sprintf(`
		SELECT
			timestamp,
			"eventType" as event_type,
			COALESCE(payload->>'User', info::JSONB->>'User', '') as user,
			COALESCE(payload->>'DatabaseName', info::JSONB->>'DatabaseName') as database,
			COALESCE(payload->>'Statement', info::JSONB->>'Statement') as statement,
			COALESCE(payload->>'Tag', info::JSONB->>'Tag') as tag,
			COALESCE(payload->>'ApplicationName', info::JSONB->>'ApplicationName') as application
		FROM system.eventlog
		WHERE timestamp > NOW() - INTERVAL '%d hours'
	`, hours)

	if user != "" {
		query += fmt.Sprintf(" AND (payload->>'User' = '%s' OR info::JSONB->>'User' = '%s')", user, user)
	}

	query += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT %d", limit)

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		// Audit logging might not be enabled or accessible
		result.Note = "Audit logging not available or not enabled. To enable audit logging, configure cluster settings."
		return result, nil
	}
	defer rows.Close()

	for rows.Next() {
		var entry AuditLogEntry
		var database, statement, tag, application *string

		if err := rows.Scan(
			&entry.Timestamp,
			&entry.EventType,
			&entry.User,
			&database,
			&statement,
			&tag,
			&application,
		); err != nil {
			return nil, fmt.Errorf("failed to scan audit log row: %w", err)
		}

		if database != nil {
			entry.Database = *database
		}
		if statement != nil {
			entry.Statement = *statement
		}
		if tag != nil {
			entry.Tag = *tag
		}
		if application != nil {
			entry.Application = *application
		}

		result.Entries = append(result.Entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating audit log rows: %w", err)
	}

	result.Count = len(result.Entries)

	if result.Count == 0 {
		result.Note = "No audit log entries found for the specified time period"
	} else {
		result.Note = fmt.Sprintf("Showing %d audit log entries from the last %d hours", result.Count, hours)
	}

	return result, nil
}
