package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// JobsHistoryTool allows querying jobs by time range for historical analysis
type JobsHistoryTool struct {
	db *pgxpool.Pool
}

// NewJobsHistoryTool creates a new jobs history tool
func NewJobsHistoryTool(db *pgxpool.Pool) *JobsHistoryTool {
	return &JobsHistoryTool{db: db}
}

// JobHistoryInfo represents historical job information
type JobHistoryInfo struct {
	JobID       int64      `json:"job_id"`
	JobType     string     `json:"job_type"`
	Description string     `json:"description"`
	Owner       string     `json:"owner"`
	Status      string     `json:"status"`
	Created     time.Time  `json:"created"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	Finished    *time.Time `json:"finished,omitempty"`
	NumRuns     *int64     `json:"num_runs,omitempty"`
	ErrorMsg    string     `json:"error_msg,omitempty"`
}

// JobsHistoryResult contains historical job query results
type JobsHistoryResult struct {
	Jobs        []JobHistoryInfo      `json:"jobs"`
	Count       int                   `json:"count"`
	TimeRange   string                `json:"time_range"`
	Summary     map[string]int        `json:"summary"`
	TypeSummary map[string]int        `json:"type_summary"`
	Note        string                `json:"note,omitempty"`
}

func (t *JobsHistoryTool) Name() string {
	return "query_jobs_history"
}

func (t *JobsHistoryTool) Description() string {
	return `Query jobs table by time range for historical analysis and investigation.

This tool allows flexible time-bounded queries of the jobs table, useful for:
- Investigating job failures during a specific time period
- Analyzing job patterns and trends over time
- Correlating jobs with cluster events or performance issues
- Understanding what jobs were running during an incident
- Tracking long-running or frequently retried jobs

The tool queries system.jobs which contains all job history including:
- Backups and restores
- Schema changes (ALTER TABLE, CREATE INDEX, etc.)
- Imports and exports
- Changefeeds
- SQL stats compaction
- Auto stats collection
- And all other background jobs

You can filter by:
- Time range (created or finished time)
- Job status (succeeded, failed, canceled, running, etc.)
- Job type (BACKUP, RESTORE, SCHEMA CHANGE, IMPORT, etc.)

Note: This queries the persistent system.jobs table, not the in-memory view.
Jobs are retained according to cluster settings (default: 14 days for finished jobs).`
}

func (t *JobsHistoryTool) ActiveDescription() string {
	return "I'm querying the jobs history table"
}

func (t *JobsHistoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start time in RFC3339 format (default: 24 hours ago). Example: '2024-01-15T10:00:00Z'",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End time in RFC3339 format (default: now). Example: '2024-01-15T12:00:00Z'",
			},
			"time_field": map[string]interface{}{
				"type":        "string",
				"description": "Which timestamp to filter on: 'created' (when job was created) or 'finished' (when job completed). Default: 'created'",
			},
			"status": map[string]interface{}{
				"type":        "string",
				"description": "Filter by job status: succeeded, failed, canceled, running, pending, etc. Optional.",
			},
			"job_type": map[string]interface{}{
				"type":        "string",
				"description": "Filter by job type: BACKUP, RESTORE, SCHEMA CHANGE, IMPORT, CHANGEFEED, etc. Optional.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of jobs to return (default: 100, max: 1000)",
			},
		},
		"required": []string{},
	}
}

func (t *JobsHistoryTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result JobsHistoryResult

	// Parse time range using the shared parser (supports relative times like "7d ago")
	now := time.Now()
	startTime, err := ParseTimeArgument(args["start_time"], now.Add(-24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("invalid start_time: %w", err)
	}

	endTime, err := ParseTimeArgument(args["end_time"], now)
	if err != nil {
		return nil, fmt.Errorf("invalid end_time: %w", err)
	}

	if startTime.After(endTime) {
		return nil, fmt.Errorf("start_time must be before end_time")
	}

	// Parse time field to filter on
	timeField := "created"
	if tf, ok := args["time_field"].(string); ok && tf != "" {
		timeField = strings.ToLower(tf)
		if timeField != "created" && timeField != "finished" {
			return nil, fmt.Errorf("time_field must be 'created' or 'finished', got: %s", timeField)
		}
	}

	// Parse limit
	limit := 100
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
		if limit > 1000 {
			limit = 1000
		} else if limit < 1 {
			limit = 1
		}
	}

	// Build query
	query := `
		SELECT
			id,
			job_type,
			description,
			owner,
			status,
			created,
			last_run,
			finished,
			num_runs,
			error_msg
		FROM system.jobs
		WHERE `

	var conditions []string
	var queryArgs []interface{}
	argNum := 1

	// Add time range condition
	if timeField == "created" {
		conditions = append(conditions, fmt.Sprintf("created >= $%d AND created <= $%d", argNum, argNum+1))
		queryArgs = append(queryArgs, startTime, endTime)
		argNum += 2
	} else {
		// For finished time, need to handle NULLs (jobs that haven't finished yet)
		conditions = append(conditions, fmt.Sprintf("finished >= $%d AND finished <= $%d", argNum, argNum+1))
		queryArgs = append(queryArgs, startTime, endTime)
		argNum += 2
	}

	// Add status filter
	if status, ok := args["status"].(string); ok && status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argNum))
		queryArgs = append(queryArgs, status)
		argNum++
	}

	// Add job type filter
	if jobType, ok := args["job_type"].(string); ok && jobType != "" {
		conditions = append(conditions, fmt.Sprintf("job_type = $%d", argNum))
		queryArgs = append(queryArgs, strings.ToUpper(jobType))
		argNum++
	}

	query += strings.Join(conditions, " AND ")
	query += fmt.Sprintf(" ORDER BY created DESC LIMIT %d", limit)

	// Execute query
	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer rows.Close()

	// Track summaries
	statusCounts := make(map[string]int)
	typeCounts := make(map[string]int)

	for rows.Next() {
		var job JobHistoryInfo
		var errorMsg *string

		if err := rows.Scan(
			&job.JobID,
			&job.JobType,
			&job.Description,
			&job.Owner,
			&job.Status,
			&job.Created,
			&job.LastRun,
			&job.Finished,
			&job.NumRuns,
			&errorMsg,
		); err != nil {
			return nil, fmt.Errorf("failed to scan job row: %w", err)
		}

		if errorMsg != nil {
			job.ErrorMsg = *errorMsg
		}

		result.Jobs = append(result.Jobs, job)
		statusCounts[job.Status]++
		typeCounts[job.JobType]++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating job rows: %w", err)
	}

	result.Count = len(result.Jobs)
	result.TimeRange = fmt.Sprintf("%s to %s (filtering on %s)",
		startTime.Format(time.RFC3339),
		endTime.Format(time.RFC3339),
		timeField)
	result.Summary = statusCounts
	result.TypeSummary = typeCounts

	// Add notes
	if result.Count == 0 {
		result.Note = "No jobs found in the specified time range"
	} else if result.Count >= limit {
		result.Note = fmt.Sprintf("Returned maximum of %d jobs. There may be more jobs in this time range. Consider narrowing your filters.", limit)
	}

	return result, nil
}
