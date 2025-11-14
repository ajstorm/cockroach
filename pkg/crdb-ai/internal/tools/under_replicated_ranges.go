package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UnderReplicatedRangesTool identifies under-replicated ranges
type UnderReplicatedRangesTool struct {
	db *pgxpool.Pool
}

// NewUnderReplicatedRangesTool creates a new under-replicated ranges tool
func NewUnderReplicatedRangesTool(db *pgxpool.Pool) *UnderReplicatedRangesTool {
	return &UnderReplicatedRangesTool{db: db}
}

// UnderReplicatedRangeInfo represents an under-replicated range
type UnderReplicatedRangeInfo struct {
	RangeID       int    `json:"range_id"`
	DatabaseName  string `json:"database_name,omitempty"`
	TableName     string `json:"table_name,omitempty"`
	Replicas      []int  `json:"replicas"`
	ReplicaCount  int    `json:"replica_count"`
	ExpectedCount int    `json:"expected_count"`
	MissingCount  int    `json:"missing_count"`
	StartKey      string `json:"start_key"`
}

// UnderReplicatedRangesResult contains under-replicated range information
type UnderReplicatedRangesResult struct {
	UnderReplicatedRanges []UnderReplicatedRangeInfo `json:"under_replicated_ranges"`
	Count                 int                        `json:"count"`
	ReplicationFactor     int                        `json:"replication_factor"`
	Status                string                     `json:"status"`
	Note                  string                     `json:"note"`
}

func (t *UnderReplicatedRangesTool) Name() string {
	return "check_under_replicated_ranges"
}

func (t *UnderReplicatedRangesTool) Description() string {
	return "Check for under-replicated ranges that have fewer replicas than the configured replication factor. This is critical for data availability."
}

func (t *UnderReplicatedRangesTool) ActiveDescription() string {
	return "I'm checking for under-replicated ranges that need attention"
}

func (t *UnderReplicatedRangesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of under-replicated ranges to return (default: 50)",
			},
		},
		"required": []string{},
	}
}

func (t *UnderReplicatedRangesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result UnderReplicatedRangesResult

	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Get default replication factor
	var replicationFactor int
	err := t.db.QueryRow(ctx, `
		SELECT (crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'numReplicas')::INT
		FROM system.zones
		WHERE id = 0
	`).Scan(&replicationFactor)
	if err != nil {
		replicationFactor = 3 // Default
	}
	result.ReplicationFactor = replicationFactor

	// Find under-replicated ranges
	query := fmt.Sprintf(`
		SELECT
			r.range_id,
			t.database_name,
			t.name as table_name,
			rnl.replicas,
			array_length(rnl.replicas, 1) as replica_count,
			r.start_pretty
		FROM crdb_internal.ranges r
		LEFT JOIN crdb_internal.table_spans ts ON r.start_key >= ts.start_key AND r.start_key < ts.end_key AND ts.dropped = false
		LEFT JOIN crdb_internal.tables t ON ts.descriptor_id = t.table_id
		JOIN crdb_internal.ranges_no_leases rnl ON r.range_id = rnl.range_id
		WHERE array_length(rnl.replicas, 1) < $1
			AND array_length(rnl.replicas, 1) > 0
		ORDER BY array_length(rnl.replicas, 1) ASC, r.range_id
		LIMIT %d
	`, limit)

	rows, err := t.db.Query(ctx, query, replicationFactor)
	if err != nil {
		return nil, fmt.Errorf("failed to query under-replicated ranges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var uri UnderReplicatedRangeInfo
		var dbName, tableName *string

		if err := rows.Scan(
			&uri.RangeID,
			&dbName,
			&tableName,
			&uri.Replicas,
			&uri.ReplicaCount,
			&uri.StartKey,
		); err != nil {
			return nil, fmt.Errorf("failed to scan under-replicated range row: %w", err)
		}

		if dbName != nil {
			uri.DatabaseName = *dbName
		}
		if tableName != nil {
			uri.TableName = *tableName
		}

		uri.ExpectedCount = replicationFactor
		uri.MissingCount = replicationFactor - uri.ReplicaCount

		result.UnderReplicatedRanges = append(result.UnderReplicatedRanges, uri)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating under-replicated range rows: %w", err)
	}

	result.Count = len(result.UnderReplicatedRanges)

	// Determine status
	if result.Count == 0 {
		result.Status = "healthy"
		result.Note = "All ranges are fully replicated"
	} else if result.Count < 10 {
		result.Status = "warning"
		result.Note = fmt.Sprintf("Found %d under-replicated ranges - some data may be at risk", result.Count)
	} else {
		result.Status = "critical"
		result.Note = fmt.Sprintf("Found %d under-replicated ranges - immediate attention required", result.Count)
	}

	return result, nil
}
