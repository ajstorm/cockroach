package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NodeStatusTool provides detailed status for individual nodes
type NodeStatusTool struct {
	db *pgxpool.Pool
}

// NewNodeStatusTool creates a new node status tool
func NewNodeStatusTool(db *pgxpool.Pool) *NodeStatusTool {
	return &NodeStatusTool{db: db}
}

// NodeStatus represents detailed status of a node
type NodeStatus struct {
	NodeID        int        `json:"node_id"`
	Address       string     `json:"address"`
	IsLive        bool       `json:"is_live"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	IsAvailable   bool       `json:"is_available"`
	Locality      string     `json:"locality,omitempty"`
	RangeCount    int        `json:"range_count"`
	LeaseCount    int        `json:"lease_count"`
	ReplicaCount  int        `json:"replica_count"`
	LivenessEpoch *int       `json:"liveness_epoch,omitempty"`
}

// NodeStatusResult contains node status information
type NodeStatusResult struct {
	Nodes      []NodeStatus `json:"nodes"`
	TotalNodes int          `json:"total_nodes"`
	LiveNodes  int          `json:"live_nodes"`
	DeadNodes  int          `json:"dead_nodes"`
}

func (t *NodeStatusTool) Name() string {
	return "get_node_status"
}

func (t *NodeStatusTool) Description() string {
	return "Get detailed status information for all nodes in the cluster including liveness, range distribution, and lease counts"
}

func (t *NodeStatusTool) ActiveDescription() string {
	return "I'm getting detailed status information for all nodes in your cluster"
}

func (t *NodeStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"node_id": map[string]interface{}{
				"type":        "integer",
				"description": "Specific node ID to get status for (optional, shows all nodes if not specified)",
			},
		},
		"required": []string{},
	}
}

func (t *NodeStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result NodeStatusResult

	nodeFilter := ""
	if nodeID, ok := args["node_id"].(float64); ok {
		nodeFilter = fmt.Sprintf(" WHERE gn.node_id = %d", int(nodeID))
	}

	// Note: unnest() is a set-returning function and cannot be used directly in GROUP BY.
	// We first expand replicas into rows in a CTE, then aggregate.
	query := fmt.Sprintf(`
		WITH replica_nodes AS (
			SELECT unnest(replicas) as node_id
			FROM crdb_internal.ranges_no_leases
		)
		SELECT
			gn.node_id,
			gn.address,
			gn.is_live,
			gl.updated_at,
			gn.is_live as is_available,
			gn.locality,
			gl.epoch as liveness_epoch,
			COALESCE(range_counts.range_count, 0) as range_count,
			COALESCE(lease_counts.lease_count, 0) as lease_count,
			COALESCE(replica_counts.replica_count, 0) as replica_count
		FROM crdb_internal.gossip_nodes gn
		LEFT JOIN crdb_internal.gossip_liveness gl ON gn.node_id = gl.node_id
		LEFT JOIN (
			SELECT node_id, COUNT(*) as range_count
			FROM replica_nodes
			GROUP BY node_id
		) range_counts ON gn.node_id = range_counts.node_id
		LEFT JOIN (
			SELECT lease_holder, COUNT(*) as lease_count
			FROM crdb_internal.ranges
			GROUP BY lease_holder
		) lease_counts ON gn.node_id = lease_counts.lease_holder
		LEFT JOIN (
			SELECT node_id, COUNT(*) as replica_count
			FROM replica_nodes
			GROUP BY node_id
		) replica_counts ON gn.node_id = replica_counts.node_id
		%s
		ORDER BY gn.node_id
	`, nodeFilter)

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query node status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ns NodeStatus
		var locality *string
		var updatedAt *time.Time
		var livenessEpoch *int
		if err := rows.Scan(
			&ns.NodeID,
			&ns.Address,
			&ns.IsLive,
			&updatedAt,
			&ns.IsAvailable,
			&locality,
			&livenessEpoch,
			&ns.RangeCount,
			&ns.LeaseCount,
			&ns.ReplicaCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan node status row: %w", err)
		}

		ns.UpdatedAt = updatedAt
		ns.LivenessEpoch = livenessEpoch
		if locality != nil {
			ns.Locality = *locality
		}

		result.Nodes = append(result.Nodes, ns)
		result.TotalNodes++
		if ns.IsLive {
			result.LiveNodes++
		} else {
			result.DeadNodes++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node status rows: %w", err)
	}

	return result, nil
}
