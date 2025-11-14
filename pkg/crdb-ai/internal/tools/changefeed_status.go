package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ChangefeedStatusTool shows status of changefeeds
type ChangefeedStatusTool struct {
	db *pgxpool.Pool
}

// NewChangefeedStatusTool creates a new changefeed status tool
func NewChangefeedStatusTool(db *pgxpool.Pool) *ChangefeedStatusTool {
	return &ChangefeedStatusTool{db: db}
}

// ChangefeedInfo represents information about a changefeed
type ChangefeedInfo struct {
	JobID            int64     `json:"job_id"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	HighWaterTime    *time.Time `json:"high_water_time,omitempty"`
	Created          time.Time `json:"created"`
	Modified         time.Time `json:"modified"`
	Error            string    `json:"error,omitempty"`
}

// ChangefeedStatusResult contains changefeed status information
type ChangefeedStatusResult struct {
	Changefeeds    []ChangefeedInfo `json:"changefeeds"`
	RunningCount   int              `json:"running_count"`
	PausedCount    int              `json:"paused_count"`
	FailedCount    int              `json:"failed_count"`
	Note           string           `json:"note,omitempty"`
}

func (t *ChangefeedStatusTool) Name() string {
	return "get_changefeed_status"
}

func (t *ChangefeedStatusTool) Description() string {
	return "Show status of changefeeds including high-water marks and any errors"
}

func (t *ChangefeedStatusTool) ActiveDescription() string {
	return "I'm examining your changefeeds to check their status and health"
}

func (t *ChangefeedStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_finished": map[string]interface{}{
				"type":        "boolean",
				"description": "Include recently finished changefeeds (default: false)",
			},
		},
		"required": []string{},
	}
}

func (t *ChangefeedStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ChangefeedStatusResult

	includeFinished := false
	if v, ok := args["include_finished"].(bool); ok {
		includeFinished = v
	}

	// Query changefeeds from jobs table
	query := `
		SELECT
			job_id,
			description,
			status,
			high_water_timestamp as high_water_time,
			created,
			modified,
			error
		FROM crdb_internal.jobs
		WHERE job_type = 'CHANGEFEED'
	`

	if !includeFinished {
		query += " AND status NOT IN ('succeeded', 'canceled')"
	}

	query += " ORDER BY created DESC LIMIT 50"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query changefeed status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cf ChangefeedInfo
		var highWater *time.Time
		var errorMsg *string

		if err := rows.Scan(
			&cf.JobID,
			&cf.Description,
			&cf.Status,
			&highWater,
			&cf.Created,
			&cf.Modified,
			&errorMsg,
		); err != nil {
			return nil, fmt.Errorf("failed to scan changefeed row: %w", err)
		}

		cf.HighWaterTime = highWater
		if errorMsg != nil {
			cf.Error = *errorMsg
		}

		result.Changefeeds = append(result.Changefeeds, cf)

		// Count by status
		switch cf.Status {
		case "running":
			result.RunningCount++
		case "paused", "pause-requested":
			result.PausedCount++
		case "failed", "reverting":
			result.FailedCount++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating changefeed rows: %w", err)
	}

	if len(result.Changefeeds) == 0 {
		result.Note = "No changefeeds found"
	} else if result.FailedCount > 0 {
		result.Note = fmt.Sprintf("%d changefeeds have failed - check error messages", result.FailedCount)
	} else if result.PausedCount > 0 {
		result.Note = fmt.Sprintf("%d changefeeds are paused", result.PausedCount)
	} else {
		result.Note = "All changefeeds are running normally"
	}

	return result, nil
}
