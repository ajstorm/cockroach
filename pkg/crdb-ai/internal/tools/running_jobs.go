package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/ts"
	"github.com/cockroachdb/cockroach/pkg/ts/tspb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunningJobsTool provides information about currently running background jobs
type RunningJobsTool struct {
	db       *pgxpool.Pool
	tsServer *ts.Server
}

// NewRunningJobsTool creates a new running jobs tool
func NewRunningJobsTool(db *pgxpool.Pool, tsServer *ts.Server) *RunningJobsTool {
	return &RunningJobsTool{
		db:       db,
		tsServer: tsServer,
	}
}

// RunningJobInfo represents a running job type with stats
type RunningJobInfo struct {
	JobType    string  `json:"job_type"`
	MaxCount   int     `json:"max_count"`
	AvgCount   float64 `json:"avg_count"`
	WasRunning bool    `json:"was_running"`
}

// RunningJobsResult contains currently running jobs summary
type RunningJobsResult struct {
	RunningJobs []RunningJobInfo `json:"running_jobs"`
	TimeRange   string           `json:"time_range"`
	Note        string           `json:"note"`
}

func (t *RunningJobsTool) Name() string {
	return "get_running_jobs"
}

func (t *RunningJobsTool) Description() string {
	return `Get information about running background jobs during a time range by querying jobs.*.currently_running metrics.

This tool dynamically discovers all jobs.*.currently_running metrics from the time-series database and reports which ones had non-zero values during the specified time range.

This tool is useful for:
- Understanding what background work was happening during a performance issue
- Investigating if heavy background jobs (like auto-stats) were running during an incident
- Checking if schema changes, backups, or other operations were active at a specific time
- Correlating background job activity with cluster performance problems

The tool reports all job types that were running during the time range, including:
- Auto statistics collection (auto_create_stats, auto_create_partial_stats)
- Manual statistics collection (create_stats)
- Schema changes (schema_change, new_schema_change)
- Backups and restores
- Changefeeds
- SQL stats compaction
- Row-level TTL
- And any other background job types

For each job type, it returns:
- Maximum concurrent count during the time range
- Average count over the time range
- Whether it was running at all

Default time range is the last 5 minutes if not specified.`
}

func (t *RunningJobsTool) ActiveDescription() string {
	return "I'm checking which background jobs were running during the specified time period"
}

func (t *RunningJobsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start time for the query. Can be relative (e.g., '5m ago', '1h ago') or absolute RFC3339 timestamp. Defaults to 5 minutes ago.",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End time for the query. Can be relative (e.g., 'now', '30m ago') or absolute RFC3339 timestamp. Defaults to now.",
			},
		},
		"required": []string{},
	}
}

func (t *RunningJobsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result RunningJobsResult

	if t.tsServer == nil {
		return nil, fmt.Errorf("time-series server not available")
	}

	// Parse time range
	now := time.Now()
	startTime, err := ParseTimeArgument(args["start_time"], now.Add(-5*time.Minute))
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

	// First, get list of all available job metrics by querying a short recent window
	// This is a discovery query to find all job types
	jobTypes, err := t.discoverJobTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover job types: %w", err)
	}

	// Now query each job type's currently_running metric for the requested time range
	var queries []tspb.Query
	downsampler := tspb.TimeSeriesQueryAggregator_AVG
	sourceAggregator := tspb.TimeSeriesQueryAggregator_SUM
	derivative := tspb.TimeSeriesQueryDerivative_NONE
	for _, jobType := range jobTypes {
		metricName := fmt.Sprintf("cr.node.jobs.%s.currently_running", jobType)
		queries = append(queries, tspb.Query{
			Name: metricName,
			// Average downsampler for gauge metrics
			Downsampler: &downsampler,
			// SUM across all nodes to get cluster-wide count
			SourceAggregator: &sourceAggregator,
			Derivative:       &derivative,
		})
	}

	if len(queries) == 0 {
		result.TimeRange = fmt.Sprintf("%s to %s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))
		result.Note = "No job metrics found in the cluster"
		return result, nil
	}

	request := &tspb.TimeSeriesQueryRequest{
		StartNanos:  startTime.UnixNano(),
		EndNanos:    endTime.UnixNano(),
		SampleNanos: (10 * time.Second).Nanoseconds(),
		Queries:     queries,
	}

	response, err := t.tsServer.Query(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to query timeseries: %w", err)
	}

	// Process results
	for _, queryResult := range response.Results {
		if len(queryResult.Datapoints) == 0 {
			continue
		}

		// Extract job type from the metric name in the result
		// Metric name format: "cr.node.jobs.<job_type>.currently_running"
		metricName := queryResult.Name
		if !strings.HasPrefix(metricName, "cr.node.jobs.") || !strings.HasSuffix(metricName, ".currently_running") {
			continue // Skip if not a jobs metric
		}
		// Extract job type: remove "cr.node.jobs." prefix (13 chars) and ".currently_running" suffix (18 chars)
		jobType := metricName[13 : len(metricName)-18]

		// Calculate max and average
		var maxCount float64
		var totalCount float64
		var hasNonZero bool

		for _, dp := range queryResult.Datapoints {
			if dp.Value > maxCount {
				maxCount = dp.Value
			}
			totalCount += dp.Value
			if dp.Value > 0 {
				hasNonZero = true
			}
		}

		if hasNonZero {
			avgCount := totalCount / float64(len(queryResult.Datapoints))
			result.RunningJobs = append(result.RunningJobs, RunningJobInfo{
				JobType:    jobType,
				MaxCount:   int(maxCount),
				AvgCount:   avgCount,
				WasRunning: true,
			})
		}
	}

	result.TimeRange = fmt.Sprintf("%s to %s (%s)",
		startTime.Format(time.RFC3339),
		endTime.Format(time.RFC3339),
		endTime.Sub(startTime))

	if len(result.RunningJobs) == 0 {
		result.Note = "No background jobs were running during this time period"
	} else {
		result.Note = fmt.Sprintf("Found %d type(s) of background jobs that were running during this time period", len(result.RunningJobs))
	}

	return result, nil
}

// discoverJobTypes discovers all available job types by querying crdb_internal.node_metrics
// and looking for jobs.*.currently_running metrics
func (t *RunningJobsTool) discoverJobTypes(ctx context.Context) ([]string, error) {
	query := `
		SELECT DISTINCT name
		FROM crdb_internal.node_metrics
		WHERE name LIKE 'jobs.%.currently_running'
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query node_metrics: %w", err)
	}
	defer rows.Close()

	// Extract all job types from metric names
	jobTypes := make(map[string]bool)
	for rows.Next() {
		var metricName string
		if err := rows.Scan(&metricName); err != nil {
			return nil, fmt.Errorf("failed to scan metric name: %w", err)
		}

		// Check if this is a jobs.*.currently_running metric
		if !strings.HasPrefix(metricName, "jobs.") {
			continue
		}
		if !strings.HasSuffix(metricName, ".currently_running") {
			continue
		}

		// Extract job type from metric name (e.g., "jobs.backup.currently_running" -> "backup")
		jobType := metricName[5 : len(metricName)-18] // Strip "jobs." prefix (5 chars) and ".currently_running" suffix (18 chars)
		jobTypes[jobType] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating metric rows: %w", err)
	}

	// Convert map to slice
	var result []string
	for jobType := range jobTypes {
		result = append(result, jobType)
	}

	return result, nil
}

