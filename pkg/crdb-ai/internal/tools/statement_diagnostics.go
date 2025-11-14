package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StatementDiagnosticsTool shows statement bundle information
type StatementDiagnosticsTool struct {
	db *pgxpool.Pool
}

// NewStatementDiagnosticsTool creates a new statement diagnostics tool
func NewStatementDiagnosticsTool(db *pgxpool.Pool) *StatementDiagnosticsTool {
	return &StatementDiagnosticsTool{db: db}
}

// StatementDiagnostic represents a statement diagnostics bundle
type StatementDiagnostic struct {
	ID                   int64     `json:"id"`
	StatementFingerprint string    `json:"statement_fingerprint"`
	Completed            bool      `json:"completed"`
	DiagnosticsID        *int64    `json:"diagnostics_id,omitempty"`
	RequestedAt          time.Time `json:"requested_at"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
}

// StatementDiagnosticsResult contains statement diagnostics information
type StatementDiagnosticsResult struct {
	Diagnostics []StatementDiagnostic `json:"diagnostics"`
	Pending     int                   `json:"pending"`
	Completed   int                   `json:"completed"`
	Note        string                `json:"note"`
}

func (t *StatementDiagnosticsTool) Name() string {
	return "get_statement_diagnostics"
}

func (t *StatementDiagnosticsTool) Description() string {
	return "Show statement diagnostics bundles collected for query troubleshooting"
}

func (t *StatementDiagnosticsTool) ActiveDescription() string {
	return "I'm reviewing statement diagnostics bundles for troubleshooting"
}

func (t *StatementDiagnosticsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_completed": map[string]interface{}{
				"type":        "boolean",
				"description": "Include completed diagnostics (default: true)",
			},
		},
		"required": []string{},
	}
}

func (t *StatementDiagnosticsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result StatementDiagnosticsResult

	includeCompleted := true
	if v, ok := args["include_completed"].(bool); ok {
		includeCompleted = v
	}

	// Query statement diagnostics requests
	query := `
		SELECT
			id,
			statement_fingerprint,
			completed,
			statement_diagnostics_id,
			requested_at,
			expires_at
		FROM system.statement_diagnostics_requests
		WHERE 1=1
	`

	if !includeCompleted {
		query += " AND completed = false"
	}

	query += " ORDER BY id DESC LIMIT 50"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		result.Note = "Statement diagnostics not available or requires admin privileges"
		return result, nil
	}
	defer rows.Close()

	for rows.Next() {
		var sd StatementDiagnostic
		if err := rows.Scan(
			&sd.ID,
			&sd.StatementFingerprint,
			&sd.Completed,
			&sd.DiagnosticsID,
			&sd.RequestedAt,
			&sd.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan statement diagnostics row: %w", err)
		}

		result.Diagnostics = append(result.Diagnostics, sd)

		if sd.Completed {
			result.Completed++
		} else {
			result.Pending++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating statement diagnostics rows: %w", err)
	}

	if len(result.Diagnostics) == 0 {
		result.Note = "No statement diagnostics bundles found"
	} else {
		result.Note = fmt.Sprintf("Found %d diagnostics bundles (%d pending, %d completed)",
			len(result.Diagnostics), result.Pending, result.Completed)
	}

	return result, nil
}
