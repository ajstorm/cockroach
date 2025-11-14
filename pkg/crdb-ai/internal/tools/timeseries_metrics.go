package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/ts"
	"github.com/cockroachdb/cockroach/pkg/ts/tspb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TimeSeriesMetricsTool queries time series metrics data over a specified period
type TimeSeriesMetricsTool struct {
	db       *pgxpool.Pool
	tsServer *ts.Server
}

// NewTimeSeriesMetricsTool creates a new time series metrics tool
func NewTimeSeriesMetricsTool(db *pgxpool.Pool, tsServer *ts.Server) *TimeSeriesMetricsTool {
	return &TimeSeriesMetricsTool{
		db:       db,
		tsServer: tsServer,
	}
}

// TimeSeriesDatapoint represents a single data point (for JSON response)
type TimeSeriesDatapoint struct {
	TimestampNanos int64   `json:"timestamp_nanos"`
	Value          float64 `json:"value"`
}

// TimeSeriesResult represents the result for a single query (for JSON response)
type TimeSeriesResult struct {
	MetricName string                 `json:"metric_name"`
	Datapoints []TimeSeriesDatapoint  `json:"datapoints"`
	Sources    []string               `json:"sources,omitempty"`
}

// TimeSeriesMetricsResult is what we return to the AI
type TimeSeriesMetricsResult struct {
	Results       []TimeSeriesResult `json:"results"`
	TimeSpan      string             `json:"time_span"`
	Resolution    string             `json:"resolution"`
	DatapointInfo string             `json:"datapoint_info"`
}

func (t *TimeSeriesMetricsTool) Name() string {
	return "query_timeseries_metrics"
}

func (t *TimeSeriesMetricsTool) Description() string {
	return `Query time series metrics data over a specified time period. This allows you to retrieve historical metrics data for analysis.

CRITICAL: Metric names MUST use CockroachDB internal format with 'cr.node.' or 'cr.store.' prefix:
- Node metrics: Start with 'cr.node.' (e.g., 'cr.node.sql.service.latency')
- Store metrics: Start with 'cr.store.' (e.g., 'cr.store.capacity.used')

IMPORTANT: Converting from Prometheus format is NOT simple underscore-to-dot replacement!
- Some underscores become dots: 'sql_service' → 'sql.service'
- Some underscores become hyphens: 'percent_normalized' → 'percent-normalized'
- Some underscores are preserved: 'auto_create_stats' → 'auto_create_stats'

Most reliable workflow:
1. Query crdb_internal.kv_store_status to see exact internal metric names
2. Add 'cr.node.' or 'cr.store.' prefix
3. Use with this tool

Examples:
- 'cr.node.sql.service.latency' (component separators are dots)
- 'cr.node.sys.cpu.combined.percent-normalized' (note the hyphen!)
- 'cr.node.jobs.auto_create_stats.currently_running' (underscores in job type preserved)

The tool supports flexible time specifications and aggregation options.`
}

func (t *TimeSeriesMetricsTool) ActiveDescription() string {
	return "I'm querying time series metrics data for the specified period"
}

func (t *TimeSeriesMetricsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"metric_names": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "List of metric names to query. Use list_available_metrics first to discover available metrics.",
			},
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start time for the query. Can be relative (e.g., '1h ago', '30m ago', '24h ago') or absolute RFC3339 timestamp. Defaults to 1 hour ago if not specified.",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End time for the query. Can be relative (e.g., 'now', '30m ago') or absolute RFC3339 timestamp. Defaults to now if not specified.",
			},
			"sources": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Optional list of source node IDs to filter (e.g., ['1', '2', '3']). If omitted, all sources are included.",
			},
			"aggregator": map[string]interface{}{
				"type":        "string",
				"description": "Aggregation function: AVG, SUM (default), MAX, MIN. Used to combine data from multiple sources.",
			},
			"sample_period": map[string]interface{}{
				"type":        "string",
				"description": "Sample period for downsampling (e.g., '10s', '1m', '5m'). Defaults to 10s. Must be a multiple of 10 seconds.",
			},
		},
		"required": []string{"metric_names"},
	}
}

func (t *TimeSeriesMetricsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Extract metric names
	metricNamesRaw, ok := args["metric_names"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("metric_names must be an array of strings")
	}

	var metricNames []string
	for _, name := range metricNamesRaw {
		if str, ok := name.(string); ok {
			metricNames = append(metricNames, str)
		}
	}

	if len(metricNames) == 0 {
		return nil, fmt.Errorf("at least one metric name must be provided")
	}

	// Parse time range
	now := time.Now()
	startTime, err := ParseTimeArgument(args["start_time"], now.Add(-1*time.Hour))
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

	// Parse optional sources
	var sources []string
	if sourcesRaw, ok := args["sources"].([]interface{}); ok {
		for _, s := range sourcesRaw {
			if str, ok := s.(string); ok {
				sources = append(sources, str)
			}
		}
	}

	// Parse aggregator
	aggregator := tspb.TimeSeriesQueryAggregator_SUM
	if agg, ok := args["aggregator"].(string); ok {
		switch agg {
		case "AVG":
			aggregator = tspb.TimeSeriesQueryAggregator_AVG
		case "SUM":
			aggregator = tspb.TimeSeriesQueryAggregator_SUM
		case "MAX":
			aggregator = tspb.TimeSeriesQueryAggregator_MAX
		case "MIN":
			aggregator = tspb.TimeSeriesQueryAggregator_MIN
		}
	}

	// Parse sample period
	samplePeriod := 10 * time.Second
	if sp, ok := args["sample_period"].(string); ok {
		parsed, err := time.ParseDuration(sp)
		if err != nil {
			return nil, fmt.Errorf("invalid sample_period: %w", err)
		}
		samplePeriod = parsed
	}

	// Build queries using protobuf types
	var queries []tspb.Query
	downsampler := tspb.TimeSeriesQueryAggregator_AVG
	derivative := tspb.TimeSeriesQueryDerivative_NONE
	for _, metricName := range metricNames {
		queries = append(queries, tspb.Query{
			Name:             metricName,
			Sources:          sources,
			Downsampler:      &downsampler,
			SourceAggregator: &aggregator,
			Derivative:       &derivative,
		})
	}

	// Create request using protobuf types
	request := &tspb.TimeSeriesQueryRequest{
		StartNanos:  startTime.UnixNano(),
		EndNanos:    endTime.UnixNano(),
		SampleNanos: samplePeriod.Nanoseconds(),
		Queries:     queries,
	}

	// Query the time series server directly (no HTTP needed!)
	response, err := t.tsServer.Query(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to query timeseries: %w", err)
	}

	// Convert protobuf response to JSON-friendly format
	var results []TimeSeriesResult
	for _, result := range response.Results {
		datapoints := make([]TimeSeriesDatapoint, len(result.Datapoints))
		for i, dp := range result.Datapoints {
			datapoints[i] = TimeSeriesDatapoint{
				TimestampNanos: dp.TimestampNanos,
				Value:          dp.Value,
			}
		}
		results = append(results, TimeSeriesResult{
			MetricName: result.Query.Name,
			Datapoints: datapoints,
			Sources:    result.Sources,
		})
	}

	// Format result
	duration := endTime.Sub(startTime)
	result := TimeSeriesMetricsResult{
		Results:    results,
		TimeSpan:   fmt.Sprintf("%s to %s (%s)", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), duration),
		Resolution: samplePeriod.String(),
		DatapointInfo: fmt.Sprintf("Returned %d metrics with data points sampled every %s",
			len(results), samplePeriod),
	}

	return result, nil
}

