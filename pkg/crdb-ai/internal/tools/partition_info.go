package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PartitionInfoTool shows table partitioning information
type PartitionInfoTool struct {
	db *pgxpool.Pool
}

// NewPartitionInfoTool creates a new partition info tool
func NewPartitionInfoTool(db *pgxpool.Pool) *PartitionInfoTool {
	return &PartitionInfoTool{db: db}
}

// PartitionInfo represents partition information for a table
type PartitionInfo struct {
	DatabaseName   string `json:"database_name"`
	TableName      string `json:"table_name"`
	IndexName      string `json:"index_name"`
	PartitionName  string `json:"partition_name"`
	PartitionValue string `json:"partition_value"`
	ZoneConfig     string `json:"zone_config,omitempty"`
}

// PartitionInfoResult contains partition information
type PartitionInfoResult struct {
	Partitions []PartitionInfo `json:"partitions"`
	Count      int             `json:"count"`
	Note       string          `json:"note,omitempty"`
}

func (t *PartitionInfoTool) Name() string {
	return "get_partition_info"
}

func (t *PartitionInfoTool) Description() string {
	return "Show table partitioning information including partition values and zone configurations"
}

func (t *PartitionInfoTool) ActiveDescription() string {
	return "I'm reviewing your table partitioning configurations"
}

func (t *PartitionInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
			"table": map[string]interface{}{
				"type":        "string",
				"description": "Filter by table name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *PartitionInfoTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result PartitionInfoResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	// Query partitions by joining with tables, table_indexes, and zones
	// Note: crdb_internal.partitions only has table_id, index_id - we need joins for names
	query := `
		SELECT
			t.database_name,
			t.name as table_name,
			ti.index_name,
			p.name as partition_name,
			COALESCE(p.list_value, p.range_value) as partition_value,
			z.raw_config_sql as zone_config
		FROM crdb_internal.partitions p
		JOIN crdb_internal.tables t ON p.table_id = t.table_id
		JOIN crdb_internal.table_indexes ti ON p.table_id = ti.descriptor_id AND p.index_id = ti.index_id
		LEFT JOIN crdb_internal.zones z ON p.zone_id = z.zone_id AND p.subzone_id = z.subzone_id
		WHERE t.database_name NOT IN ('system', 'crdb_internal', 'information_schema', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND t.database_name = '%s'", database)
	}

	if table != "" {
		query += fmt.Sprintf(" AND t.name = '%s'", table)
	}

	query += " ORDER BY t.database_name, t.name, ti.index_name, p.name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		// Partitions might not be available if not used
		result.Note = "No partitions found. Partitioning may not be used in this cluster."
		return result, nil
	}
	defer rows.Close()

	for rows.Next() {
		var pi PartitionInfo
		var partitionValue *string
		var zoneConfig *string

		if err := rows.Scan(
			&pi.DatabaseName,
			&pi.TableName,
			&pi.IndexName,
			&pi.PartitionName,
			&partitionValue,
			&zoneConfig,
		); err != nil {
			return nil, fmt.Errorf("failed to scan partition info row: %w", err)
		}

		if partitionValue != nil {
			pi.PartitionValue = *partitionValue
		}
		if zoneConfig != nil {
			pi.ZoneConfig = *zoneConfig
		}

		result.Partitions = append(result.Partitions, pi)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating partition info rows: %w", err)
	}

	result.Count = len(result.Partitions)

	if result.Count == 0 {
		result.Note = "No partitions found in the cluster"
	}

	return result, nil
}
