package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReplicationStatusTool checks replication health across the cluster
type ReplicationStatusTool struct {
	db *pgxpool.Pool
}

// NewReplicationStatusTool creates a new replication status tool
func NewReplicationStatusTool(db *pgxpool.Pool) *ReplicationStatusTool {
	return &ReplicationStatusTool{db: db}
}

// ReplicationStatusResult contains replication health information
type ReplicationStatusResult struct {
	TotalRanges          int     `json:"total_ranges"`
	FullyReplicatedRanges int    `json:"fully_replicated_ranges"`
	UnderReplicatedRanges int    `json:"under_replicated_ranges"`
	OverReplicatedRanges  int    `json:"over_replicated_ranges"`
	UnavailableRanges    int     `json:"unavailable_ranges"`
	ReplicationFactor    int     `json:"replication_factor"`
	HealthPercentage     float64 `json:"health_percentage"`
	Status               string  `json:"status"`
}

func (t *ReplicationStatusTool) Name() string {
	return "get_replication_status"
}

func (t *ReplicationStatusTool) Description() string {
	return "Check replication health across the cluster, including under-replicated, over-replicated, and unavailable ranges"
}

func (t *ReplicationStatusTool) ActiveDescription() string {
	return "I'm checking your cluster's replication health"
}

func (t *ReplicationStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *ReplicationStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ReplicationStatusResult

	// Get default replication factor from zone config
	var replicationFactor int
	err := t.db.QueryRow(ctx, `
		SELECT (crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'numReplicas')::INT
		FROM system.zones
		WHERE id = 0
	`).Scan(&replicationFactor)
	if err != nil {
		// Default to 3 if we can't get it
		replicationFactor = 3
	}
	result.ReplicationFactor = replicationFactor

	// Count ranges by replication status
	query := `
		SELECT
			COUNT(*) as total_ranges,
			COUNT(*) FILTER (WHERE array_length(replicas, 1) = $1) as fully_replicated,
			COUNT(*) FILTER (WHERE array_length(replicas, 1) < $1) as under_replicated,
			COUNT(*) FILTER (WHERE array_length(replicas, 1) > $1) as over_replicated,
			COUNT(*) FILTER (WHERE array_length(replicas, 1) = 0 OR replicas IS NULL) as unavailable
		FROM crdb_internal.ranges_no_leases
	`

	err = t.db.QueryRow(ctx, query, replicationFactor).Scan(
		&result.TotalRanges,
		&result.FullyReplicatedRanges,
		&result.UnderReplicatedRanges,
		&result.OverReplicatedRanges,
		&result.UnavailableRanges,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query replication status: %w", err)
	}

	// Calculate health percentage
	if result.TotalRanges > 0 {
		result.HealthPercentage = float64(result.FullyReplicatedRanges) / float64(result.TotalRanges) * 100
	}

	// Determine overall status
	if result.UnavailableRanges > 0 {
		result.Status = "critical"
	} else if result.UnderReplicatedRanges > 0 {
		result.Status = "warning"
	} else if result.OverReplicatedRanges > 0 {
		result.Status = "degraded"
	} else {
		result.Status = "healthy"
	}

	return result, nil
}
