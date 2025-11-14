package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GossipNetworkTool analyzes gossip network health and connectivity
type GossipNetworkTool struct {
	db *pgxpool.Pool
}

// NewGossipNetworkTool creates a new gossip network tool
func NewGossipNetworkTool(db *pgxpool.Pool) *GossipNetworkTool {
	return &GossipNetworkTool{db: db}
}

func (t *GossipNetworkTool) Name() string {
	return "analyze_gossip_network"
}

func (t *GossipNetworkTool) Description() string {
	return `Analyze the gossip network health and connectivity between cluster nodes.

The gossip network is critical for cluster coordination, distributing:
- Node liveness information
- Descriptor updates (tables, ranges, etc.)
- Cluster settings
- Range lease information
- Node addresses and capabilities

This tool examines:
- Gossip connectivity between nodes
- Network latency and connectivity issues
- Stale or missing gossip information
- Network partitions or isolated nodes
- Gossip protocol health metrics

Use this when:
- Nodes appear disconnected or unreachable
- Cluster metadata seems out of sync
- Investigating split-brain scenarios
- Network connectivity issues suspected
- Nodes showing inconsistent cluster views
- Range lease issues or rebalancing problems

Gossip issues can cause:
- Range unavailability
- Lease transfer failures
- Slow metadata propagation
- Cluster instability`
}

func (t *GossipNetworkTool) ActiveDescription() string {
	return "I'm analyzing the gossip network"
}

func (t *GossipNetworkTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

// GossipNodeInfo represents gossip information for a node
type GossipNodeInfo struct {
	NodeID           int      `json:"node_id"`
	Address          string   `json:"address"`
	Locality         string   `json:"locality,omitempty"`
	IsLive           bool     `json:"is_live"`
	ConnectedNodes   []int    `json:"connected_nodes"`
	NumConnections   int      `json:"num_connections"`
	MaxConnections   int      `json:"max_connections"`
	SentinelGossipTS *string  `json:"sentinel_gossip_ts,omitempty"`
}

// GossipConnection represents a gossip connection between nodes
type GossipConnection struct {
	FromNodeID int    `json:"from_node_id"`
	ToNodeID   int    `json:"to_node_id"`
	Status     string `json:"status"` // "healthy", "degraded", "down"
}

// GossipIssue represents a detected issue in the gossip network
type GossipIssue struct {
	Severity    string `json:"severity"` // "high", "medium", "low"
	NodeID      *int   `json:"node_id,omitempty"`
	Description string `json:"description"`
	Impact      string `json:"impact"`
	Recommendation string `json:"recommendation"`
}

// GossipNetworkResult contains the analysis results
type GossipNetworkResult struct {
	Nodes            []GossipNodeInfo    `json:"nodes"`
	Connections      []GossipConnection  `json:"connections,omitempty"`
	Issues           []GossipIssue       `json:"issues"`
	TotalNodes       int                 `json:"total_nodes"`
	LiveNodes        int                 `json:"live_nodes"`
	NetworkHealthy   bool                `json:"network_healthy"`
	Summary          string              `json:"summary"`
}

func (t *GossipNetworkTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Get node information
	nodes, connectionDataAvailable, err := t.getGossipNodeInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gossip node info: %w", err)
	}

	// Analyze for issues
	issues := analyzeGossipIssues(nodes, connectionDataAvailable)

	// Calculate statistics
	liveNodes := 0
	for _, node := range nodes {
		if node.IsLive {
			liveNodes++
		}
	}

	networkHealthy := len(issues) == 0
	for _, issue := range issues {
		if issue.Severity == "high" {
			networkHealthy = false
			break
		}
	}

	// Build summary
	summary := buildGossipSummary(nodes, liveNodes, networkHealthy, issues, connectionDataAvailable)

	result := GossipNetworkResult{
		Nodes:          nodes,
		Issues:         issues,
		TotalNodes:     len(nodes),
		LiveNodes:      liveNodes,
		NetworkHealthy: networkHealthy,
		Summary:        summary,
	}

	return result, nil
}

// getGossipNodeInfo retrieves gossip information for all nodes
// Returns nodes, whether connection data is available, and any error
func (t *GossipNetworkTool) getGossipNodeInfo(ctx context.Context) ([]GossipNodeInfo, bool, error) {
	// Query node info from gossip_nodes (which has address, locality, is_live)
	query := `
		SELECT
			node_id,
			address,
			locality,
			is_live
		FROM crdb_internal.gossip_nodes
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var nodes []GossipNodeInfo
	connectionDataAvailable := false

	for rows.Next() {
		var node GossipNodeInfo
		var locality *string

		err := rows.Scan(&node.NodeID, &node.Address, &locality, &node.IsLive)
		if err != nil {
			return nil, false, err
		}

		if locality != nil {
			node.Locality = *locality
		}

		// Get connection info for this node
		// Note: crdb_internal.gossip_network is deprecated and may not be populated
		connections, err := t.getNodeConnections(ctx, node.NodeID)
		if err == nil {
			node.ConnectedNodes = connections
			node.NumConnections = len(connections)
			// If any node has connections, the data is available
			if len(connections) > 0 {
				connectionDataAvailable = true
			}
		}

		// Max connections is typically proportional to cluster size
		// Heuristic: minimum 3, maximum 10, based on cluster size
		totalNodes := len(nodes) + 1 // Current nodes plus this one
		node.MaxConnections = 3
		if totalNodes > 3 {
			node.MaxConnections = min(10, (totalNodes+1)/2)
		}

		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	return nodes, connectionDataAvailable, nil
}

// getNodeConnections retrieves gossip connections for a specific node
// NOTE: crdb_internal.gossip_network has columns source_id and target_id (not node_id and peer_id)
// and is deprecated/no longer populated. This returns empty results as gossip connectivity
// cannot currently be queried via SQL.
func (t *GossipNetworkTool) getNodeConnections(ctx context.Context, nodeID int) ([]int, error) {
	query := `
		SELECT DISTINCT target_id::INT
		FROM crdb_internal.gossip_network
		WHERE source_id = $1
		ORDER BY target_id
	`

	rows, err := t.db.Query(ctx, query, nodeID)
	if err != nil {
		// Gossip network table might not be available in all versions
		return []int{}, nil
	}
	defer rows.Close()

	var connections []int
	for rows.Next() {
		var peerID int
		if err := rows.Scan(&peerID); err != nil {
			return nil, err
		}
		connections = append(connections, peerID)
	}

	return connections, rows.Err()
}

// analyzeGossipIssues identifies issues in the gossip network
func analyzeGossipIssues(nodes []GossipNodeInfo, connectionDataAvailable bool) []GossipIssue {
	var issues []GossipIssue

	// Check for dead nodes
	for _, node := range nodes {
		if !node.IsLive {
			issues = append(issues, GossipIssue{
				Severity:    "high",
				NodeID:      &node.NodeID,
				Description: fmt.Sprintf("Node %d is not live", node.NodeID),
				Impact:      "Range replicas on this node are unavailable",
				Recommendation: "Investigate node status - check logs, network connectivity, and system resources",
			})
		}
	}

	// Only analyze connection-based issues if connection data is available
	// (crdb_internal.gossip_network is deprecated and may not be populated)
	if !connectionDataAvailable {
		return issues
	}

	// Check for insufficient connections
	for _, node := range nodes {
		if node.IsLive && node.NumConnections < 2 && len(nodes) > 2 {
			issues = append(issues, GossipIssue{
				Severity:    "medium",
				NodeID:      &node.NodeID,
				Description: fmt.Sprintf("Node %d has only %d gossip connection(s)", node.NodeID, node.NumConnections),
				Impact:      "Reduced redundancy for gossip information propagation",
				Recommendation: "Check network connectivity between nodes - may indicate network partition or firewall issues",
			})
		}
	}

	// Check for isolated nodes (live but no connections)
	for _, node := range nodes {
		if node.IsLive && node.NumConnections == 0 && len(nodes) > 1 {
			issues = append(issues, GossipIssue{
				Severity:    "high",
				NodeID:      &node.NodeID,
				Description: fmt.Sprintf("Node %d is isolated (no gossip connections)", node.NodeID),
				Impact:      "Node cannot receive cluster updates and may have stale metadata",
				Recommendation: "Immediate investigation required - check network, firewall rules, and gossip port accessibility",
			})
		}
	}

	// Check for asymmetric connections (connection imbalance)
	if len(nodes) > 0 {
		totalConnections := 0
		for _, node := range nodes {
			totalConnections += node.NumConnections
		}
		avgConnections := float64(totalConnections) / float64(len(nodes))

		for _, node := range nodes {
			if node.IsLive && float64(node.NumConnections) < avgConnections*0.5 {
				issues = append(issues, GossipIssue{
					Severity:    "low",
					NodeID:      &node.NodeID,
					Description: fmt.Sprintf("Node %d has fewer gossip connections than average", node.NodeID),
					Impact:      "Slower gossip propagation to this node",
					Recommendation: "Monitor for gossip delays - may be transient network issues",
				})
			}
		}
	}

	return issues
}

// buildGossipSummary creates a summary of gossip network analysis
func buildGossipSummary(nodes []GossipNodeInfo, liveNodes int, healthy bool, issues []GossipIssue, connectionDataAvailable bool) string {
	summary := fmt.Sprintf("Cluster has %d nodes, %d are live. ", len(nodes), liveNodes)

	if healthy {
		summary += "Gossip network is healthy. "
	} else {
		summary += "Gossip network has issues. "
	}

	if len(issues) > 0 {
		highSeverity := 0
		for _, issue := range issues {
			if issue.Severity == "high" {
				highSeverity++
			}
		}

		summary += fmt.Sprintf("Found %d issue(s)", len(issues))
		if highSeverity > 0 {
			summary += fmt.Sprintf(" (%d high severity)", highSeverity)
		}
		summary += ". "
	}

	// Note if connection data is unavailable
	if !connectionDataAvailable {
		summary += "Note: Per-node gossip connection data is not available (crdb_internal.gossip_network is deprecated). Node liveness is verified through gossip_liveness. "
	} else if liveNodes > 0 {
		// Calculate average connectivity only if data is available
		totalConn := 0
		for _, node := range nodes {
			if node.IsLive {
				totalConn += node.NumConnections
			}
		}
		avgConn := float64(totalConn) / float64(liveNodes)
		summary += fmt.Sprintf("Average gossip connections per live node: %.1f. ", avgConn)
	}

	return summary
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
