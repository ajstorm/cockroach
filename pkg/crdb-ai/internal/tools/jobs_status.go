package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// JobsStatusTool shows running and recent jobs
type JobsStatusTool struct {
	db *pgxpool.Pool
}

// NewJobsStatusTool creates a new jobs status tool
func NewJobsStatusTool(db *pgxpool.Pool) *JobsStatusTool {
	return &JobsStatusTool{db: db}
}

// JobInfo represents information about a job
type JobInfo struct {
	JobID             int64      `json:"job_id"`
	JobType           string     `json:"job_type"`
	Description       string     `json:"description"`
	UserName          string     `json:"user_name"`
	Status            string     `json:"status"`
	Created           time.Time  `json:"created"`
	Modified          time.Time  `json:"modified"`
	Finished          *time.Time `json:"finished,omitempty"`
	FractionCompleted float64    `json:"fraction_completed"`
	Error             string     `json:"error,omitempty"`
}

// JobsStatusResult contains job status information
type JobsStatusResult struct {
	RunningJobs   []JobInfo `json:"running_jobs"`
	RecentJobs    []JobInfo `json:"recent_jobs,omitempty"`
	FailedJobs    []JobInfo `json:"failed_jobs,omitempty"`
	RunningCount  int       `json:"running_count"`
	FailedCount   int       `json:"failed_count"`
}

func (t *JobsStatusTool) Name() string {
	return "get_jobs_status"
}

func (t *JobsStatusTool) Description() string {
	return "Show status of running, recent, and failed jobs including backups, schema changes, and imports"
}

func (t *JobsStatusTool) ActiveDescription() string {
	return "I'm checking the status of your running and recent jobs"
}

func (t *JobsStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_finished": map[string]interface{}{
				"type":        "boolean",
				"description": "Include recently finished jobs (default: false)",
			},
			"include_failed": map[string]interface{}{
				"type":        "boolean",
				"description": "Include failed jobs (default: true)",
			},
		},
		"required": []string{},
	}
}

func (t *JobsStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result JobsStatusResult

	includeFinished := false
	if v, ok := args["include_finished"].(bool); ok {
		includeFinished = v
	}

	includeFailed := true
	if v, ok := args["include_failed"].(bool); ok {
		includeFailed = v
	}

	// Get running jobs
	runningQuery := `
		SELECT
			job_id,
			job_type,
			description,
			user_name,
			status,
			created,
			modified,
			finished,
			fraction_completed,
			error
		FROM crdb_internal.jobs
		WHERE status IN ('running', 'pending', 'pause-requested', 'reverting')
		ORDER BY created DESC
		LIMIT 50
	`

	rows, err := t.db.Query(ctx, runningQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query running jobs: %w", err)
	}

	for rows.Next() {
		var job JobInfo
		var errorMsg *string
		if err := rows.Scan(
			&job.JobID,
			&job.JobType,
			&job.Description,
			&job.UserName,
			&job.Status,
			&job.Created,
			&job.Modified,
			&job.Finished,
			&job.FractionCompleted,
			&errorMsg,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan job row: %w", err)
		}
		if errorMsg != nil {
			job.Error = *errorMsg
		}
		result.RunningJobs = append(result.RunningJobs, job)
	}
	rows.Close()

	result.RunningCount = len(result.RunningJobs)

	// Get failed jobs if requested
	if includeFailed {
		failedQuery := `
			SELECT
				job_id,
				job_type,
				description,
				user_name,
				status,
				created,
				modified,
				finished,
				fraction_completed,
				error
			FROM crdb_internal.jobs
			WHERE status IN ('failed', 'canceled')
				AND finished > NOW() - INTERVAL '24 hours'
			ORDER BY finished DESC
			LIMIT 20
		`

		rows, err := t.db.Query(ctx, failedQuery)
		if err != nil {
			return nil, fmt.Errorf("failed to query failed jobs: %w", err)
		}

		for rows.Next() {
			var job JobInfo
			var errorMsg *string
			if err := rows.Scan(
				&job.JobID,
				&job.JobType,
				&job.Description,
				&job.UserName,
				&job.Status,
				&job.Created,
				&job.Modified,
				&job.Finished,
				&job.FractionCompleted,
				&errorMsg,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan failed job row: %w", err)
			}
			if errorMsg != nil {
				job.Error = *errorMsg
			}
			result.FailedJobs = append(result.FailedJobs, job)
		}
		rows.Close()

		result.FailedCount = len(result.FailedJobs)
	}

	// Get recent finished jobs if requested
	if includeFinished {
		finishedQuery := `
			SELECT
				job_id,
				job_type,
				description,
				user_name,
				status,
				created,
				modified,
				finished,
				fraction_completed,
				error
			FROM crdb_internal.jobs
			WHERE status = 'succeeded'
				AND finished > NOW() - INTERVAL '1 hour'
			ORDER BY finished DESC
			LIMIT 10
		`

		rows, err := t.db.Query(ctx, finishedQuery)
		if err != nil {
			return nil, fmt.Errorf("failed to query finished jobs: %w", err)
		}

		for rows.Next() {
			var job JobInfo
			var errorMsg *string
			if err := rows.Scan(
				&job.JobID,
				&job.JobType,
				&job.Description,
				&job.UserName,
				&job.Status,
				&job.Created,
				&job.Modified,
				&job.Finished,
				&job.FractionCompleted,
				&errorMsg,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan finished job row: %w", err)
			}
			if errorMsg != nil {
				job.Error = *errorMsg
			}
			result.RecentJobs = append(result.RecentJobs, job)
		}
		rows.Close()
	}

	return result, nil
}
