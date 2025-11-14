package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NetworkLatencyTool measures network latency between nodes
type NetworkLatencyTool struct {
	db *pgxpool.Pool
}

// NewNetworkLatencyTool creates a new network latency tool
func NewNetworkLatencyTool(db *pgxpool.Pool) *NetworkLatencyTool {
	return &NetworkLatencyTool{db: db}
}

// LatencyInfo represents latency between two nodes
type LatencyInfo struct {
	SourceNodeID int     `json:"source_node_id"`
	TargetNodeID int     `json:"target_node_id"`
	LatencyMS    float64 `json:"latency_ms"`
}

// NetworkLatencyResult contains network latency information
type NetworkLatencyResult struct {
	Latencies    []LatencyInfo `json:"latencies"`
	AvgLatencyMS float64       `json:"avg_latency_ms"`
	MaxLatencyMS float64       `json:"max_latency_ms"`
	MinLatencyMS float64       `json:"min_latency_ms"`
	Status       string        `json:"status"`
	Note         string        `json:"note"`
}

func (t *NetworkLatencyTool) Name() string {
	return "get_network_latency"
}

func (t *NetworkLatencyTool) Description() string {
	return "Measure network latency between cluster nodes to identify network issues"
}

func (t *NetworkLatencyTool) ActiveDescription() string {
	return "I'm measuring network latency between your cluster nodes"
}

func (t *NetworkLatencyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *NetworkLatencyTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result NetworkLatencyResult

	// Note: crdb_internal.gossip_network is deprecated and doesn't contain latency data.
	// Network latency between nodes is not directly exposed via SQL.
	// Users should monitor network latency using time-series metrics.

	// Get basic node connectivity info to provide useful context
	query := `
		SELECT node_id, address
		FROM crdb_internal.gossip_nodes
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		result.Note = "Unable to query node information. Network latency metrics are available through time-series metrics (cr.node.round-trip-latency) rather than SQL tables."
		result.Status = "info"
		return result, nil
	}
	defer rows.Close()

	var nodeCount int
	for rows.Next() {
		nodeCount++
		// Just count nodes, we don't have per-connection latency data via SQL
		var nodeID int
		var address *string
		rows.Scan(&nodeID, &address)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	result.Status = "info"
	if nodeCount <= 1 {
		result.Note = fmt.Sprintf("Single-node cluster detected (%d node). Network latency between nodes is not applicable.", nodeCount)
	} else {
		result.Note = fmt.Sprintf("Cluster has %d nodes. Per-connection network latency is not directly available via SQL. "+
			"To monitor network latency, use query_timeseries_metrics with metric 'cr.node.round-trip-latency' "+
			"or check the DB Console's Network Latency page.", nodeCount)
	}

	return result, nil
}
