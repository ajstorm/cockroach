package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CapacityTool checks cluster capacity and storage usage
type CapacityTool struct {
	db *pgxpool.Pool
}

// NewCapacityTool creates a new capacity tool
func NewCapacityTool(db *pgxpool.Pool) *CapacityTool {
	return &CapacityTool{db: db}
}

// NodeCapacity represents capacity information for a node
type NodeCapacity struct {
	NodeID           int     `json:"node_id"`
	CapacityBytes    int64   `json:"capacity_bytes"`
	AvailableBytes   int64   `json:"available_bytes"`
	UsedBytes        int64   `json:"used_bytes"`
	UsedPercentage   float64 `json:"used_percentage"`
	RangeCount       int     `json:"range_count"`
}

// CapacityResult contains capacity information
type CapacityResult struct {
	Nodes              []NodeCapacity `json:"nodes"`
	TotalCapacityGB    float64        `json:"total_capacity_gb"`
	TotalUsedGB        float64        `json:"total_used_gb"`
	TotalAvailableGB   float64        `json:"total_available_gb"`
	ClusterUsedPercent float64        `json:"cluster_used_percent"`
	Status             string         `json:"status"`
	Note               string         `json:"note"`
}

func (t *CapacityTool) Name() string {
	return "check_capacity"
}

func (t *CapacityTool) Description() string {
	return "Check cluster storage capacity and usage across all nodes"
}

func (t *CapacityTool) ActiveDescription() string {
	return "I'm analyzing your cluster's storage capacity and usage across all nodes"
}

func (t *CapacityTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *CapacityTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result CapacityResult

	// Get capacity information from store_status
	// Note: capacity, available, used are direct columns, not inside metrics JSONB
	query := `
		SELECT
			node_id,
			capacity as capacity_bytes,
			available as available_bytes,
			used as used_bytes,
			range_count
		FROM crdb_internal.kv_store_status
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query capacity: %w", err)
	}
	defer rows.Close()

	var totalCapacity, totalUsed, totalAvailable int64

	for rows.Next() {
		var nc NodeCapacity
		if err := rows.Scan(
			&nc.NodeID,
			&nc.CapacityBytes,
			&nc.AvailableBytes,
			&nc.UsedBytes,
			&nc.RangeCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan capacity row: %w", err)
		}

		if nc.CapacityBytes > 0 {
			nc.UsedPercentage = float64(nc.UsedBytes) / float64(nc.CapacityBytes) * 100
		}

		result.Nodes = append(result.Nodes, nc)

		totalCapacity += nc.CapacityBytes
		totalUsed += nc.UsedBytes
		totalAvailable += nc.AvailableBytes
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating capacity rows: %w", err)
	}

	// Convert to GB
	result.TotalCapacityGB = float64(totalCapacity) / (1024 * 1024 * 1024)
	result.TotalUsedGB = float64(totalUsed) / (1024 * 1024 * 1024)
	result.TotalAvailableGB = float64(totalAvailable) / (1024 * 1024 * 1024)

	if totalCapacity > 0 {
		result.ClusterUsedPercent = float64(totalUsed) / float64(totalCapacity) * 100
	}

	// Determine status based on usage
	if result.ClusterUsedPercent >= 90 {
		result.Status = "critical"
		result.Note = "Cluster is at critical capacity (>90% used). Add nodes or increase storage immediately."
	} else if result.ClusterUsedPercent >= 75 {
		result.Status = "warning"
		result.Note = "Cluster capacity is getting high (>75% used). Consider adding nodes or storage soon."
	} else {
		result.Status = "healthy"
		result.Note = "Cluster has adequate capacity available."
	}

	return result, nil
}
