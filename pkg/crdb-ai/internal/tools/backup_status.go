package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BackupStatusTool shows status of backups and restores
type BackupStatusTool struct {
	db *pgxpool.Pool
}

// NewBackupStatusTool creates a new backup status tool
func NewBackupStatusTool(db *pgxpool.Pool) *BackupStatusTool {
	return &BackupStatusTool{db: db}
}

// BackupInfo represents information about a backup or restore job
type BackupInfo struct {
	JobID             int64      `json:"job_id"`
	JobType           string     `json:"job_type"`
	Description       string     `json:"description"`
	Status            string     `json:"status"`
	RunningStatus     string     `json:"running_status,omitempty"`
	Created           time.Time  `json:"created"`
	Finished          *time.Time `json:"finished,omitempty"`
	Modified          time.Time  `json:"modified"`
	FractionCompleted float64    `json:"fraction_completed"`
	Error             string     `json:"error,omitempty"`
}

// BackupStatusResult contains backup/restore status information
type BackupStatusResult struct {
	Backups        []BackupInfo `json:"backups"`
	Restores       []BackupInfo `json:"restores"`
	RunningBackups int          `json:"running_backups"`
	FailedBackups  int          `json:"failed_backups"`
	RunningRestores int         `json:"running_restores"`
	FailedRestores int          `json:"failed_restores"`
	Note           string       `json:"note,omitempty"`
}

func (t *BackupStatusTool) Name() string {
	return "get_backup_status"
}

func (t *BackupStatusTool) Description() string {
	return "Show status of backup and restore jobs including progress and any errors"
}

func (t *BackupStatusTool) ActiveDescription() string {
	return "I'm reviewing the status of your backup and restore jobs"
}

func (t *BackupStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_finished": map[string]interface{}{
				"type":        "boolean",
				"description": "Include recently finished backups/restores (default: false)",
			},
			"hours": map[string]interface{}{
				"type":        "integer",
				"description": "How many hours back to look for finished jobs (default: 24)",
			},
		},
		"required": []string{},
	}
}

func (t *BackupStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result BackupStatusResult

	includeFinished := false
	if v, ok := args["include_finished"].(bool); ok {
		includeFinished = v
	}

	hours := 24
	if h, ok := args["hours"].(float64); ok {
		hours = int(h)
	}

	// Query backup and restore jobs
	// Note: crdb_internal.jobs does not have a 'started' column, using 'modified' instead
	query := fmt.Sprintf(`
		SELECT
			job_id,
			job_type,
			description,
			status,
			running_status,
			created,
			finished,
			modified,
			fraction_completed,
			error
		FROM crdb_internal.jobs
		WHERE job_type IN ('BACKUP', 'RESTORE')
	`)

	if includeFinished {
		query += fmt.Sprintf(" AND (status NOT IN ('succeeded', 'canceled') OR finished > NOW() - INTERVAL '%d hours')", hours)
	} else {
		query += " AND status NOT IN ('succeeded', 'canceled')"
	}

	query += " ORDER BY created DESC LIMIT 50"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query backup status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var bi BackupInfo
		var errorMsg *string
		var runningStatus *string

		if err := rows.Scan(
			&bi.JobID,
			&bi.JobType,
			&bi.Description,
			&bi.Status,
			&runningStatus,
			&bi.Created,
			&bi.Finished,
			&bi.Modified,
			&bi.FractionCompleted,
			&errorMsg,
		); err != nil {
			return nil, fmt.Errorf("failed to scan backup info row: %w", err)
		}

		if errorMsg != nil {
			bi.Error = *errorMsg
		}
		if runningStatus != nil {
			bi.RunningStatus = *runningStatus
		}

		// Categorize by type
		if bi.JobType == "BACKUP" {
			result.Backups = append(result.Backups, bi)
			if bi.Status == "running" || bi.Status == "pending" {
				result.RunningBackups++
			} else if bi.Status == "failed" {
				result.FailedBackups++
			}
		} else if bi.JobType == "RESTORE" {
			result.Restores = append(result.Restores, bi)
			if bi.Status == "running" || bi.Status == "pending" {
				result.RunningRestores++
			} else if bi.Status == "failed" {
				result.FailedRestores++
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating backup info rows: %w", err)
	}

	// Generate note
	if len(result.Backups) == 0 && len(result.Restores) == 0 {
		result.Note = "No backup or restore jobs found"
	} else {
		notes := []string{}
		if result.FailedBackups > 0 {
			notes = append(notes, fmt.Sprintf("%d failed backups", result.FailedBackups))
		}
		if result.FailedRestores > 0 {
			notes = append(notes, fmt.Sprintf("%d failed restores", result.FailedRestores))
		}
		if result.RunningBackups > 0 {
			notes = append(notes, fmt.Sprintf("%d backups in progress", result.RunningBackups))
		}
		if result.RunningRestores > 0 {
			notes = append(notes, fmt.Sprintf("%d restores in progress", result.RunningRestores))
		}

		if len(notes) > 0 {
			result.Note = fmt.Sprintf("Status: %s", notes[0])
			for i := 1; i < len(notes); i++ {
				result.Note += fmt.Sprintf(", %s", notes[i])
			}
		} else {
			result.Note = "All backup/restore jobs completed successfully"
		}
	}

	return result, nil
}
