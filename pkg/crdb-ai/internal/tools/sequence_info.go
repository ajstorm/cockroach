package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SequenceInfoTool lists sequences and their current values
type SequenceInfoTool struct {
	db *pgxpool.Pool
}

// NewSequenceInfoTool creates a new sequence info tool
func NewSequenceInfoTool(db *pgxpool.Pool) *SequenceInfoTool {
	return &SequenceInfoTool{db: db}
}

// SequenceInfo represents information about a sequence
type SequenceInfo struct {
	DatabaseName  string `json:"database_name"`
	SchemaName    string `json:"schema_name"`
	SequenceName  string `json:"sequence_name"`
	DataType      string `json:"data_type"`
	StartValue    int64  `json:"start_value"`
	MinValue      int64  `json:"min_value"`
	MaxValue      int64  `json:"max_value"`
	Increment     int64  `json:"increment"`
	CacheSize     int64  `json:"cache_size"`
	IsCycle       bool   `json:"is_cycle"`
}

// SequenceInfoResult contains sequence information
type SequenceInfoResult struct {
	Sequences []SequenceInfo `json:"sequences"`
	Count     int            `json:"count"`
}

func (t *SequenceInfoTool) Name() string {
	return "get_sequence_info"
}

func (t *SequenceInfoTool) Description() string {
	return "List all sequences in the database and their configuration"
}

func (t *SequenceInfoTool) ActiveDescription() string {
	return "I'm listing all sequences in your database"
}

func (t *SequenceInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
		},
		"required": []string{},
	}
}

func (t *SequenceInfoTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result SequenceInfoResult

	database, _ := args["database"].(string)

	// Note: information_schema.sequences doesn't have cache_size column
	// Cache size isn't exposed in the standard schema, we'll use 1 as default
	query := `
		SELECT
			sequence_catalog as database_name,
			sequence_schema as schema_name,
			sequence_name,
			data_type,
			start_value::BIGINT,
			minimum_value::BIGINT,
			maximum_value::BIGINT,
			increment::BIGINT,
			1::BIGINT as cache_size,
			cycle_option = 'YES' as is_cycle
		FROM information_schema.sequences
		WHERE sequence_schema NOT IN ('information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	if database != "" {
		query += fmt.Sprintf(" AND sequence_catalog = '%s'", database)
	}

	query += " ORDER BY sequence_catalog, sequence_schema, sequence_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query sequences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var si SequenceInfo
		if err := rows.Scan(
			&si.DatabaseName,
			&si.SchemaName,
			&si.SequenceName,
			&si.DataType,
			&si.StartValue,
			&si.MinValue,
			&si.MaxValue,
			&si.Increment,
			&si.CacheSize,
			&si.IsCycle,
		); err != nil {
			return nil, fmt.Errorf("failed to scan sequence row: %w", err)
		}

		result.Sequences = append(result.Sequences, si)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sequence rows: %w", err)
	}

	result.Count = len(result.Sequences)

	return result, nil
}
