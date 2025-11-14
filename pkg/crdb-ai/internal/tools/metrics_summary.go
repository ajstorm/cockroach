package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/ts"
	"github.com/cockroachdb/cockroach/pkg/ts/tspb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetricsSummaryTool provides summary of key cluster metrics
type MetricsSummaryTool struct {
	db       *pgxpool.Pool
	tsServer *ts.Server
}

// NewMetricsSummaryTool creates a new metrics summary tool
func NewMetricsSummaryTool(db *pgxpool.Pool, tsServer *ts.Server) *MetricsSummaryTool {
	return &MetricsSummaryTool{
		db:       db,
		tsServer: tsServer,
	}
}

// NodeMetrics represents key metrics for a node
type NodeMetrics struct {
	NodeID               int     `json:"node_id"`
	CPUPercent           float64 `json:"cpu_percent"`
	MemoryUsedBytes      int64   `json:"memory_used_bytes"`
	MemoryAvailableBytes int64   `json:"memory_available_bytes"`
	DiskReadBytesPerSec  int64   `json:"disk_read_bytes_per_sec"`
	DiskWriteBytesPerSec int64   `json:"disk_write_bytes_per_sec"`
	NetworkBytesIn       int64   `json:"network_bytes_in"`
	NetworkBytesOut      int64   `json:"network_bytes_out"`
}

// MetricsSummaryResult contains cluster metrics summary
type MetricsSummaryResult struct {
	NodeMetrics    []NodeMetrics `json:"node_metrics"`
	ClusterQPS     float64       `json:"cluster_qps"`
	ClusterTPM     float64       `json:"cluster_tpm"`
	ActiveSessions int           `json:"active_sessions"`
	Note           string        `json:"note"`
}

func (t *MetricsSummaryTool) Name() string {
	return "get_metrics_summary"
}

func (t *MetricsSummaryTool) Description() string {
	return "Get summary of key cluster metrics including CPU, memory, disk I/O, and network usage"
}

func (t *MetricsSummaryTool) ActiveDescription() string {
	return "I'm gathering key metrics about your cluster's CPU, memory, disk, and network usage"
}

func (t *MetricsSummaryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *MetricsSummaryTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result MetricsSummaryResult

	// Get metrics from kv_store_status
	query := `
		SELECT
			node_id,
			COALESCE((metrics->>'sys.cpu.combined.percent-normalized')::FLOAT, 0) as cpu_percent,
			COALESCE((metrics->>'sys.rss')::BIGINT, 0) as memory_used_bytes,
			COALESCE((metrics->>'sys.go.allocbytes')::BIGINT, 0) as go_allocated_bytes,
			COALESCE((metrics->>'rocksdb.read.bytes')::BIGINT, 0) as disk_read_bytes,
			COALESCE((metrics->>'rocksdb.write.bytes')::BIGINT, 0) as disk_write_bytes,
			COALESCE((metrics->>'sys.host.net.recv.bytes')::BIGINT, 0) as network_bytes_in,
			COALESCE((metrics->>'sys.host.net.send.bytes')::BIGINT, 0) as network_bytes_out
		FROM crdb_internal.kv_store_status
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var nm NodeMetrics
		var goAlloc int64

		if err := rows.Scan(
			&nm.NodeID,
			&nm.CPUPercent,
			&nm.MemoryUsedBytes,
			&goAlloc,
			&nm.DiskReadBytesPerSec,
			&nm.DiskWriteBytesPerSec,
			&nm.NetworkBytesIn,
			&nm.NetworkBytesOut,
		); err != nil {
			return nil, fmt.Errorf("failed to scan metrics row: %w", err)
		}

		// Use the max of RSS and Go allocated as available memory indicator
		if goAlloc > nm.MemoryUsedBytes {
			nm.MemoryAvailableBytes = goAlloc
		} else {
			nm.MemoryAvailableBytes = nm.MemoryUsedBytes
		}

		result.NodeMetrics = append(result.NodeMetrics, nm)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating metrics rows: %w", err)
	}

	// Query time-series metrics for real-time QPS/TPM and active sessions
	if t.tsServer != nil {
		qps, tpm, err := t.calculateRealTimeQPS(ctx)
		if err == nil {
			result.ClusterQPS = qps
			result.ClusterTPM = tpm
		} else {
			// If time-series query fails, log but don't fail the whole request
			result.Note = fmt.Sprintf("Note: Could not calculate real-time QPS/TPM: %v", err)
		}

		activeSessions, err := t.calculateActiveSessions(ctx)
		if err == nil {
			result.ActiveSessions = activeSessions
		}
	}

	if result.Note == "" {
		result.Note = "Metrics represent current cluster state. QPS/TPM and active sessions calculated from last 5 minutes of time-series data."
	}

	return result, nil
}

// calculateRealTimeQPS queries the time-series database for sql.query.count
// and calculates the actual queries per second over the last 5 minutes
func (t *MetricsSummaryTool) calculateRealTimeQPS(ctx context.Context) (qps float64, tpm float64, err error) {
	now := time.Now()
	startTime := now.Add(-5 * time.Minute)

	// Query cr.node.sql.query.count metric with rate derivative
	// This gives us the rate of change (queries per second) rather than cumulative count
	request := &tspb.TimeSeriesQueryRequest{
		StartNanos: startTime.UnixNano(),
		EndNanos:   now.UnixNano(),
		// Use 10-second resolution for accurate rate calculation
		SampleNanos: (10 * time.Second).Nanoseconds(),
		Queries: []tspb.Query{
			{
				Name: "cr.node.sql.query.count",
				// Use DERIVATIVE aggregator to get rate of change
				Downsampler: tspb.TimeSeriesQueryAggregator_AVG.Enum(),
				// SUM across all nodes to get cluster-wide QPS
				SourceAggregator: tspb.TimeSeriesQueryAggregator_SUM.Enum(),
				// Use DERIVATIVE to convert cumulative counter to rate
				Derivative: tspb.TimeSeriesQueryDerivative_DERIVATIVE.Enum(),
			},
		},
	}

	response, err := t.tsServer.Query(ctx, request)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query timeseries: %w", err)
	}

	if len(response.Results) == 0 || len(response.Results[0].Datapoints) == 0 {
		return 0, 0, fmt.Errorf("no datapoints returned for sql.query.count")
	}

	// Calculate average QPS from the last 5 minutes of datapoints
	// Each datapoint represents the rate (queries/sec) at that moment
	var totalQPS float64
	var count int
	for _, dp := range response.Results[0].Datapoints {
		totalQPS += dp.Value
		count++
	}

	if count == 0 {
		return 0, 0, nil
	}

	avgQPS := totalQPS / float64(count)
	avgTPM := avgQPS * 60 // Convert queries/sec to transactions/min

	return avgQPS, avgTPM, nil
}

// calculateActiveSessions queries the time-series database for sql.active_connections
// and returns the current number of active SQL sessions
func (t *MetricsSummaryTool) calculateActiveSessions(ctx context.Context) (int, error) {
	now := time.Now()
	startTime := now.Add(-1 * time.Minute) // Just need the most recent value

	// Query cr.node.sql.active_connections metric
	request := &tspb.TimeSeriesQueryRequest{
		StartNanos:  startTime.UnixNano(),
		EndNanos:    now.UnixNano(),
		SampleNanos: (10 * time.Second).Nanoseconds(),
		Queries: []tspb.Query{
			{
				Name: "cr.node.sql.conns",
				// Average downsampler for gauge metrics
				Downsampler: tspb.TimeSeriesQueryAggregator_AVG.Enum(),
				// SUM across all nodes to get cluster-wide sessions
				SourceAggregator: tspb.TimeSeriesQueryAggregator_SUM.Enum(),
			},
		},
	}

	response, err := t.tsServer.Query(ctx, request)
	if err != nil {
		return 0, fmt.Errorf("failed to query timeseries: %w", err)
	}

	if len(response.Results) == 0 || len(response.Results[0].Datapoints) == 0 {
		return 0, nil
	}

	// Get the most recent datapoint (last value)
	lastDatapoint := response.Results[0].Datapoints[len(response.Results[0].Datapoints)-1]
	activeSessions := int(lastDatapoint.Value)

	return activeSessions, nil
}
