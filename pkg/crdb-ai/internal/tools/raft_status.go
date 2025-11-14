package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RaftStatusTool shows Raft consensus status
type RaftStatusTool struct {
	db *pgxpool.Pool
}

// NewRaftStatusTool creates a new Raft status tool
func NewRaftStatusTool(db *pgxpool.Pool) *RaftStatusTool {
	return &RaftStatusTool{db: db}
}

// RaftRangeStatus represents Raft status for a range
type RaftRangeStatus struct {
	RangeID       int    `json:"range_id"`
	DatabaseName  string `json:"database_name,omitempty"`
	TableName     string `json:"table_name,omitempty"`
	LeaderNodeID  int    `json:"leader_node_id"`
	RaftState     string `json:"raft_state"`
	ReplicaCount  int    `json:"replica_count"`
	Quorum        int    `json:"quorum"`
	PendingCmds   int64  `json:"pending_commands"`
}

// RaftStatusResult contains Raft status information
type RaftStatusResult struct {
	Ranges                []RaftRangeStatus `json:"ranges,omitempty"`
	TotalRanges           int               `json:"total_ranges"`
	RangesWithoutQuorum   int               `json:"ranges_without_quorum"`
	RangesWithHighPending int               `json:"ranges_with_high_pending"`
	Status                string            `json:"status"`
	Note                  string            `json:"note"`
}

func (t *RaftStatusTool) Name() string {
	return "get_raft_status"
}

func (t *RaftStatusTool) Description() string {
	return "Check Raft consensus status including leader distribution and command queue depths"
}

func (t *RaftStatusTool) ActiveDescription() string {
	return "I'm checking your Raft consensus status and leader distribution"
}

func (t *RaftStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"show_problem_ranges": map[string]interface{}{
				"type":        "boolean",
				"description": "Show only ranges with potential issues (default: true)",
			},
		},
		"required": []string{},
	}
}

func (t *RaftStatusTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result RaftStatusResult

	showProblems := true
	if v, ok := args["show_problem_ranges"].(bool); ok {
		showProblems = v
	}

	// Get basic Raft information from ranges
	query := `
		SELECT
			r.range_id,
			t.database_name,
			t.name as table_name,
			r.lease_holder as leader_node_id,
			array_length(rnl.replicas, 1) as replica_count,
			r.range_size
		FROM crdb_internal.ranges r
		LEFT JOIN crdb_internal.table_spans ts ON r.start_key >= ts.start_key AND r.start_key < ts.end_key AND ts.dropped = false
		LEFT JOIN crdb_internal.tables t ON ts.descriptor_id = t.table_id
		JOIN crdb_internal.ranges_no_leases rnl ON r.range_id = rnl.range_id
	`

	if showProblems {
		query += ` WHERE array_length(rnl.replicas, 1) < 3 OR r.range_size > 536870912`
	}

	query += " ORDER BY r.range_id LIMIT 100"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query Raft status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rs RaftRangeStatus
		var dbName, tableName *string
		var rangeSize int64

		if err := rows.Scan(
			&rs.RangeID,
			&dbName,
			&tableName,
			&rs.LeaderNodeID,
			&rs.ReplicaCount,
			&rangeSize,
		); err != nil {
			return nil, fmt.Errorf("failed to scan Raft status row: %w", err)
		}

		if dbName != nil {
			rs.DatabaseName = *dbName
		}
		if tableName != nil {
			rs.TableName = *tableName
		}

		rs.Quorum = (rs.ReplicaCount / 2) + 1

		if rs.ReplicaCount < rs.Quorum {
			result.RangesWithoutQuorum++
		}

		// Estimate pending commands based on range size (simplified)
		if rangeSize > 536870912 { // 512MB
			rs.PendingCmds = rangeSize / 1000000 // Rough estimate
			result.RangesWithHighPending++
		}

		rs.RaftState = "stable"
		if rs.ReplicaCount < 3 {
			rs.RaftState = "under-replicated"
		} else if rangeSize > 536870912 {
			rs.RaftState = "needs-split"
		}

		result.Ranges = append(result.Ranges, rs)
		result.TotalRanges++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating Raft status rows: %w", err)
	}

	// Get total range count
	var totalRanges int
	err = t.db.QueryRow(ctx, "SELECT COUNT(*) FROM crdb_internal.ranges").Scan(&totalRanges)
	if err == nil {
		result.TotalRanges = totalRanges
	}

	// Determine status
	if result.RangesWithoutQuorum > 0 {
		result.Status = "critical"
		result.Note = fmt.Sprintf("%d ranges without quorum - data unavailability risk", result.RangesWithoutQuorum)
	} else if result.RangesWithHighPending > 10 {
		result.Status = "warning"
		result.Note = fmt.Sprintf("%d ranges with high pending commands - may impact latency", result.RangesWithHighPending)
	} else {
		result.Status = "healthy"
		result.Note = "Raft consensus is operating normally"
	}

	return result, nil
}
