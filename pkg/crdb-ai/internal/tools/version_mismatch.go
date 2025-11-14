package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VersionMismatchTool checks node version compatibility
type VersionMismatchTool struct {
	db *pgxpool.Pool
}

// NewVersionMismatchTool creates a new version mismatch tool
func NewVersionMismatchTool(db *pgxpool.Pool) *VersionMismatchTool {
	return &VersionMismatchTool{db: db}
}

// NodeVersion represents a node and its version
type NodeVersion struct {
	NodeID  int    `json:"node_id"`
	Version string `json:"version"`
	Address string `json:"address"`
}

// VersionMismatchResult contains version compatibility information
type VersionMismatchResult struct {
	Nodes             []NodeVersion     `json:"nodes"`
	UniqueVersions    map[string]int    `json:"unique_versions"`
	HasMismatch       bool              `json:"has_mismatch"`
	ConsensusVersion  string            `json:"consensus_version"`
	Note              string            `json:"note"`
}

func (t *VersionMismatchTool) Name() string {
	return "check_version_mismatch"
}

func (t *VersionMismatchTool) Description() string {
	return "Check for node version mismatches that could indicate upgrade issues or compatibility problems"
}

func (t *VersionMismatchTool) ActiveDescription() string {
	return "I'm checking for any node version mismatches"
}

func (t *VersionMismatchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *VersionMismatchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result VersionMismatchResult
	result.UniqueVersions = make(map[string]int)

	// Get node versions
	query := `
		SELECT
			node_id,
			build_tag as version,
			address
		FROM crdb_internal.gossip_nodes
		ORDER BY node_id
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query node versions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var nv NodeVersion
		if err := rows.Scan(
			&nv.NodeID,
			&nv.Version,
			&nv.Address,
		); err != nil {
			return nil, fmt.Errorf("failed to scan node version row: %w", err)
		}

		result.Nodes = append(result.Nodes, nv)
		result.UniqueVersions[nv.Version]++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node version rows: %w", err)
	}

	// Get cluster version setting
	var clusterVersion string
	err = t.db.QueryRow(ctx, `
		SELECT value
		FROM crdb_internal.cluster_settings
		WHERE variable = 'version'
	`).Scan(&clusterVersion)

	if err == nil {
		result.ConsensusVersion = clusterVersion
	}

	// Check for mismatches
	result.HasMismatch = len(result.UniqueVersions) > 1

	if result.HasMismatch {
		result.Note = fmt.Sprintf("Version mismatch detected! Found %d different versions across nodes. This can occur during rolling upgrades but should be temporary.",
			len(result.UniqueVersions))
	} else if len(result.UniqueVersions) == 1 {
		result.Note = "All nodes are running the same version - no version mismatch"
	} else {
		result.Note = "Unable to determine version information"
	}

	return result, nil
}
