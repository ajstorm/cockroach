package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LeaseStatusTool analyzes lease distribution across nodes
type LeaseStatusTool struct {
	db *pgxpool.Pool
}

// NewLeaseStatusTool creates a new lease status tool
func NewLeaseStatusTool(db *pgxpool.Pool) *LeaseStatusTool {
	return &LeaseStatusTool{db: db}
}

// LeaseDistribution represents lease distribution for a node
type LeaseDistribution struct {
	NodeID     int     `json:"node_id"`
	LeaseCount int     `json:"lease_count"`
	Percentage float64 `json:"percentage"`
}

// LeaseStatusResult contains lease distribution information
type LeaseStatusResult struct {
	Distribution      []LeaseDistribution `json:"distribution"`
	TotalLeases       int                 `json:"total_leases"`
	IsBalanced        bool                `json:"is_balanced"`
	MaxImbalance      float64             `json:"max_imbalance_percentage"`
	Note              string              `json:"note"`
}

func (t *LeaseStatusTool) Name() string {
	return "get_lease_status"
}

func (t *LeaseStatusTool) Description() string {
	return "Analyze lease distribution across nodes to identify imbalances that could affect query performance"
}

func (t *LeaseStatusTool) ActiveDescription() string {
	return "I'm analyzing lease distribution across nodes to spot any imbalances"
}

func (t *LeaseStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *LeaseStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result LeaseStatusResult

	// Get lease distribution by node
	query := `
		SELECT
			lease_holder as node_id,
			COUNT(*) as lease_count
		FROM crdb_internal.ranges
		GROUP BY lease_holder
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query lease status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ld LeaseDistribution
		if err := rows.Scan(&ld.NodeID, &ld.LeaseCount); err != nil {
			return nil, fmt.Errorf("failed to scan lease distribution row: %w", err)
		}
		result.Distribution = append(result.Distribution, ld)
		result.TotalLeases += ld.LeaseCount
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating lease distribution rows: %w", err)
	}

	// Calculate percentages and check balance
	if result.TotalLeases > 0 {
		idealPercentage := 100.0 / float64(len(result.Distribution))
		maxDeviation := 0.0

		for i := range result.Distribution {
			result.Distribution[i].Percentage = float64(result.Distribution[i].LeaseCount) / float64(result.TotalLeases) * 100
			deviation := result.Distribution[i].Percentage - idealPercentage
			if deviation < 0 {
				deviation = -deviation
			}
			if deviation > maxDeviation {
				maxDeviation = deviation
			}
		}

		result.MaxImbalance = maxDeviation

		// Consider balanced if max deviation is less than 10%
		result.IsBalanced = maxDeviation < 10.0

		if result.IsBalanced {
			result.Note = "Leases are well-balanced across nodes"
		} else {
			result.Note = fmt.Sprintf("Lease distribution is imbalanced with max deviation of %.1f%% from ideal. Consider rebalancing.", maxDeviation)
		}
	}

	return result, nil
}
