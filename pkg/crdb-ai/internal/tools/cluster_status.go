package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClusterStatusTool provides information about cluster health and topology
type ClusterStatusTool struct {
	db *pgxpool.Pool
}

// NewClusterStatusTool creates a new cluster status tool
func NewClusterStatusTool(db *pgxpool.Pool) *ClusterStatusTool {
	return &ClusterStatusTool{db: db}
}

// NodeInfo represents information about a single node
type NodeInfo struct {
	NodeID   int    `json:"node_id"`
	Address  string `json:"address"`
	IsLive   bool   `json:"is_live"`
	IsActive bool   `json:"is_active"`
}

// RangeInfo represents summary information about ranges
type RangeInfo struct {
	TotalRanges       int `json:"total_ranges"`
	UnavailableRanges int `json:"unavailable_ranges"`
}

// ClusterStatusResult contains cluster health information
type ClusterStatusResult struct {
	Nodes  []NodeInfo `json:"nodes"`
	Ranges RangeInfo  `json:"ranges"`
}

func (t *ClusterStatusTool) Name() string {
	return "get_cluster_status"
}

func (t *ClusterStatusTool) Description() string {
	return "Get the current health and status of the CockroachDB cluster, including node information and range distribution"
}

func (t *ClusterStatusTool) ActiveDescription() string {
	return "I'm checking the overall health and status of your CockroachDB cluster"
}

func (t *ClusterStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *ClusterStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ClusterStatusResult

	// Query node information from crdb_internal.gossip_nodes
	nodeQuery := `
		SELECT
			node_id,
			address,
			is_live
		FROM crdb_internal.gossip_nodes
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, nodeQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query node information: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var node NodeInfo
		var address *string
		if err := rows.Scan(&node.NodeID, &address, &node.IsLive); err != nil {
			return nil, fmt.Errorf("failed to scan node row: %w", err)
		}
		if address != nil {
			node.Address = *address
		}
		node.IsActive = node.IsLive
		result.Nodes = append(result.Nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	// Query range information
	rangeQuery := `
		SELECT
			COUNT(*) as total_ranges,
			COUNT(*) FILTER (WHERE array_length(replicas, 1) = 0 OR replicas IS NULL) as unavailable_ranges
		FROM crdb_internal.ranges_no_leases
	`

	if err := t.db.QueryRow(ctx, rangeQuery).Scan(
		&result.Ranges.TotalRanges,
		&result.Ranges.UnavailableRanges,
	); err != nil {
		return nil, fmt.Errorf("failed to query range information: %w", err)
	}

	return result, nil
}
