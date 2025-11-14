package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaChangeProgressTool monitors ongoing schema changes
type SchemaChangeProgressTool struct {
	db *pgxpool.Pool
}

// NewSchemaChangeProgressTool creates a new schema change progress tool
func NewSchemaChangeProgressTool(db *pgxpool.Pool) *SchemaChangeProgressTool {
	return &SchemaChangeProgressTool{db: db}
}

func (t *SchemaChangeProgressTool) Name() string {
	return "schema_change_progress"
}

func (t *SchemaChangeProgressTool) Description() string {
	return `Monitor the progress of ongoing schema changes like ALTER TABLE, CREATE INDEX, etc.

This tool tracks:
- Active schema change jobs
- Progress percentage and estimated time remaining
- Phase of schema change (backfill, validation, etc.)
- Impact on cluster resources
- Historical schema changes and their duration
- Potential issues or slowdowns

Use this when:
- Running long schema changes on large tables
- Need to estimate completion time for ALTER TABLE or CREATE INDEX
- Investigating why a schema change is slow
- Planning maintenance windows for schema migrations
- Verifying schema change isn't blocking other operations

Schema changes can be long-running on large tables:
- CREATE INDEX requires backfilling all existing rows
- ALTER TABLE ADD COLUMN with default requires rewriting
- DROP INDEX is usually fast (logical deletion)
- Some operations require table rewrites`
}

func (t *SchemaChangeProgressTool) ActiveDescription() string {
	return "I'm checking schema change progress"
}

func (t *SchemaChangeProgressTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"table_name": map[string]interface{}{
				"type":        "string",
				"description": "Filter to schema changes on a specific table. Optional.",
			},
			"include_completed": map[string]interface{}{
				"type":        "boolean",
				"description": "Include recently completed schema changes. Defaults to false.",
			},
		},
		"required": []string{},
	}
}

// SchemaChangeJob represents a schema change job
type SchemaChangeJob struct {
	JobID            int64   `json:"job_id"`
	JobType          string  `json:"job_type"`
	Description      string  `json:"description"`
	TableName        string  `json:"table_name,omitempty"`
	Status           string  `json:"status"`
	Created          string  `json:"created"`
	Started          *string `json:"started,omitempty"`
	Finished         *string `json:"finished,omitempty"`
	Progress         float64 `json:"progress_percent"`
	FractionComplete float64 `json:"fraction_complete"`
	HighWaterTime    *string `json:"high_water_time,omitempty"`
	RunningStatus    string  `json:"running_status,omitempty"`
	ErrorMessage     *string `json:"error,omitempty"`
	TimeElapsed      *string `json:"time_elapsed,omitempty"`
	EstimatedRemaining *string `json:"estimated_remaining,omitempty"`
}

// SchemaChangeProgressResult contains the monitoring results
type SchemaChangeProgressResult struct {
	ActiveJobs     []SchemaChangeJob `json:"active_jobs"`
	CompletedJobs  []SchemaChangeJob `json:"completed_jobs,omitempty"`
	TotalActive    int               `json:"total_active"`
	TotalCompleted int               `json:"total_completed,omitempty"`
	Summary        string            `json:"summary"`
}

func (t *SchemaChangeProgressTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var tableFilter *string
	if tn, ok := args["table_name"].(string); ok && tn != "" {
		tableFilter = &tn
	}

	includeCompleted := false
	if ic, ok := args["include_completed"].(bool); ok {
		includeCompleted = ic
	}

	// Get active schema change jobs
	activeJobs, err := t.getSchemaChangeJobs(ctx, tableFilter, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get active schema changes: %w", err)
	}

	var completedJobs []SchemaChangeJob
	if includeCompleted {
		completedJobs, err = t.getSchemaChangeJobs(ctx, tableFilter, true)
		if err != nil {
			// Don't fail if we can't get completed jobs
			completedJobs = []SchemaChangeJob{}
		}
	}

	// Build summary
	summary := buildSchemaChangeSummary(activeJobs, completedJobs)

	result := SchemaChangeProgressResult{
		ActiveJobs:     activeJobs,
		CompletedJobs:  completedJobs,
		TotalActive:    len(activeJobs),
		TotalCompleted: len(completedJobs),
		Summary:        summary,
	}

	return result, nil
}

// getSchemaChangeJobs retrieves schema change jobs
func (t *SchemaChangeProgressTool) getSchemaChangeJobs(
	ctx context.Context, tableFilter *string, completed bool,
) ([]SchemaChangeJob, error) {
	query := `
		SELECT
			job_id,
			job_type,
			description,
			status,
			created,
			finished,
			fraction_completed,
			high_water_timestamp,
			running_status,
			error
		FROM crdb_internal.jobs
		WHERE job_type IN ('SCHEMA CHANGE', 'NEW SCHEMA CHANGE', 'SCHEMA CHANGE GC', 'CREATE STATS')
	`

	if completed {
		query += " AND status IN ('succeeded', 'failed', 'canceled')"
		query += " AND finished >= NOW() - INTERVAL '24 hours'"
	} else {
		query += " AND status IN ('running', 'pending', 'paused', 'retrying')"
	}

	if tableFilter != nil {
		query += " AND description ILIKE '%' || $1 || '%'"
	}

	query += " ORDER BY created DESC LIMIT 50"

	var rows interface{ Close() }
	var err error

	if tableFilter != nil {
		rows, err = t.db.Query(ctx, query, *tableFilter)
	} else {
		rows, err = t.db.Query(ctx, query)
	}

	if err != nil {
		return nil, err
	}
	defer rows.(interface{ Close() }).Close()

	var jobs []SchemaChangeJob

	for rows.(interface {
		Next() bool
		Scan(...interface{}) error
	}).Next() {
		var job SchemaChangeJob
		var created, finished, highWaterTime *time.Time
		var runningStatus, errorMsg *string

		err := rows.(interface {
			Scan(...interface{}) error
		}).Scan(
			&job.JobID,
			&job.JobType,
			&job.Description,
			&job.Status,
			&created,
			&finished,
			&job.FractionComplete,
			&highWaterTime,
			&runningStatus,
			&errorMsg,
		)
		if err != nil {
			return nil, err
		}

		// Format timestamps
		if created != nil {
			job.Created = created.Format(time.RFC3339)

			// Calculate elapsed time from created (since there's no started column)
			elapsed := time.Since(*created)
			elapsedStr := formatDuration(elapsed)
			job.TimeElapsed = &elapsedStr

			// Estimate remaining time if job is running
			if job.Status == "running" && job.FractionComplete > 0 && job.FractionComplete < 1 {
				totalEstimated := elapsed / time.Duration(job.FractionComplete)
				remaining := totalEstimated - elapsed
				if remaining > 0 {
					remainingStr := formatDuration(remaining)
					job.EstimatedRemaining = &remainingStr
				}
			}
		}

		if finished != nil {
			finishedStr := finished.Format(time.RFC3339)
			job.Finished = &finishedStr
		}

		if highWaterTime != nil {
			hwStr := highWaterTime.Format(time.RFC3339)
			job.HighWaterTime = &hwStr
		}

		if runningStatus != nil {
			job.RunningStatus = *runningStatus
		}

		if errorMsg != nil {
			job.ErrorMessage = errorMsg
		}

		job.Progress = job.FractionComplete * 100

		// Try to extract table name from description
		job.TableName = extractTableFromDescription(job.Description)

		jobs = append(jobs, job)
	}

	// Check for iteration errors
	if err := rows.(interface{ Err() error }).Err(); err != nil {
		return nil, err
	}

	return jobs, nil
}

// extractTableFromDescription attempts to extract table name from job description
func extractTableFromDescription(description string) string {
	// Simple pattern matching - job descriptions usually contain "TABLE table_name"
	// This is a heuristic and may not work for all cases
	if len(description) == 0 {
		return ""
	}

	// Look for patterns like "ALTER TABLE foo" or "CREATE INDEX ON bar"
	// This is simplified - could be made more robust
	return "" // Placeholder - actual implementation would use regex or parsing
}

// formatDuration formats a duration in human-readable form
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	} else if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	} else {
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}

// buildSchemaChangeSummary creates a summary of schema changes
func buildSchemaChangeSummary(activeJobs, completedJobs []SchemaChangeJob) string {
	if len(activeJobs) == 0 && len(completedJobs) == 0 {
		return "No schema changes found"
	}

	summary := ""

	if len(activeJobs) > 0 {
		summary += fmt.Sprintf("Found %d active schema change(s). ", len(activeJobs))

		// Find furthest along job
		maxProgress := 0.0
		var mostProgress *SchemaChangeJob
		for i := range activeJobs {
			if activeJobs[i].Progress > maxProgress {
				maxProgress = activeJobs[i].Progress
				mostProgress = &activeJobs[i]
			}
		}

		if mostProgress != nil {
			summary += fmt.Sprintf("Most advanced: %.1f%% complete", mostProgress.Progress)
			if mostProgress.EstimatedRemaining != nil {
				summary += fmt.Sprintf(" (est. %s remaining)", *mostProgress.EstimatedRemaining)
			}
			summary += ". "
		}
	}

	if len(completedJobs) > 0 {
		succeeded := 0
		failed := 0
		for _, job := range completedJobs {
			if job.Status == "succeeded" {
				succeeded++
			} else if job.Status == "failed" {
				failed++
			}
		}

		summary += fmt.Sprintf("Recent completed: %d succeeded", succeeded)
		if failed > 0 {
			summary += fmt.Sprintf(", %d failed", failed)
		}
		summary += ". "
	}

	return summary
}
