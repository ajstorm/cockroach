package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ZoneConfigsTool shows zone configurations for databases and tables
type ZoneConfigsTool struct {
	db *pgxpool.Pool
}

// NewZoneConfigsTool creates a new zone configs tool
func NewZoneConfigsTool(db *pgxpool.Pool) *ZoneConfigsTool {
	return &ZoneConfigsTool{db: db}
}

// ZoneConfig represents a zone configuration
type ZoneConfig struct {
	Target           string `json:"target"`
	DatabaseName     string `json:"database_name,omitempty"`
	TableName        string `json:"table_name,omitempty"`
	NumReplicas      int    `json:"num_replicas"`
	RangeMinBytes    int64  `json:"range_min_bytes"`
	RangeMaxBytes    int64  `json:"range_max_bytes"`
	GCTTLSeconds     int    `json:"gc_ttl_seconds"`
	Constraints      string `json:"constraints,omitempty"`
}

// ZoneConfigsResult contains zone configuration information
type ZoneConfigsResult struct {
	Configs []ZoneConfig `json:"configs"`
	Count   int          `json:"count"`
}

func (t *ZoneConfigsTool) Name() string {
	return "get_zone_configs"
}

func (t *ZoneConfigsTool) Description() string {
	return "Get replication zone configurations for databases and tables including replica count, constraints, and GC settings"
}

func (t *ZoneConfigsTool) ActiveDescription() string {
	return "I'm checking your replication zone configurations"
}

func (t *ZoneConfigsTool) Parameters() map[string]interface{} {
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

func (t *ZoneConfigsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result ZoneConfigsResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	query := `
		SELECT
			target,
			database_name,
			table_name,
			(crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'numReplicas')::INT as num_replicas,
			(crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'rangeMinBytes')::BIGINT as range_min_bytes,
			(crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'rangeMaxBytes')::BIGINT as range_max_bytes,
			(crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'gc'->'ttlSeconds')::INT as gc_ttl_seconds,
			crdb_internal.pb_to_json('cockroach.config.zonepb.ZoneConfig', raw_config_protobuf)->'constraints' as constraints
		FROM crdb_internal.zones
		WHERE target NOT LIKE 'RANGE%'
	`

	if database != "" {
		query += fmt.Sprintf(" AND database_name = '%s'", database)
	}

	if table != "" {
		query += fmt.Sprintf(" AND table_name = '%s'", table)
	}

	query += " ORDER BY target"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query zone configs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var zc ZoneConfig
		var dbName, tableName, constraints *string
		var numReplicas, gcTTL *int
		var rangeMin, rangeMax *int64

		if err := rows.Scan(
			&zc.Target,
			&dbName,
			&tableName,
			&numReplicas,
			&rangeMin,
			&rangeMax,
			&gcTTL,
			&constraints,
		); err != nil {
			return nil, fmt.Errorf("failed to scan zone config row: %w", err)
		}

		if dbName != nil {
			zc.DatabaseName = *dbName
		}
		if tableName != nil {
			zc.TableName = *tableName
		}
		if numReplicas != nil {
			zc.NumReplicas = *numReplicas
		}
		if rangeMin != nil {
			zc.RangeMinBytes = *rangeMin
		}
		if rangeMax != nil {
			zc.RangeMaxBytes = *rangeMax
		}
		if gcTTL != nil {
			zc.GCTTLSeconds = *gcTTL
		}
		if constraints != nil {
			zc.Constraints = *constraints
		}

		result.Configs = append(result.Configs, zc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating zone config rows: %w", err)
	}

	result.Count = len(result.Configs)

	return result, nil
}
