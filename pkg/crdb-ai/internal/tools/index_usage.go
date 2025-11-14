package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// IndexUsageTool analyzes index usage patterns
type IndexUsageTool struct {
	db *pgxpool.Pool
}

// NewIndexUsageTool creates a new index usage tool
func NewIndexUsageTool(db *pgxpool.Pool) *IndexUsageTool {
	return &IndexUsageTool{db: db}
}

// IndexUsageInfo represents usage statistics for an index
type IndexUsageInfo struct {
	DatabaseName string     `json:"database_name"`
	SchemaName   string     `json:"schema_name"`
	TableName    string     `json:"table_name"`
	IndexName    string     `json:"index_name"`
	IndexType    string     `json:"index_type"`
	TotalReads   int64      `json:"total_reads"`
	LastRead     *time.Time `json:"last_read,omitempty"`
	DaysSinceUse *int       `json:"days_since_use,omitempty"`
}

// IndexUsageResult contains index usage analysis
type IndexUsageResult struct {
	UnusedIndexes []IndexUsageInfo `json:"unused_indexes,omitempty"`
	LowUseIndexes []IndexUsageInfo `json:"low_use_indexes,omitempty"`
	Count         int              `json:"count"`
	Note          string           `json:"note"`
}

func (t *IndexUsageTool) Name() string {
	return "analyze_index_usage"
}

func (t *IndexUsageTool) Description() string {
	return "Analyze index usage patterns to identify unused or rarely used indexes that may be candidates for removal"
}

func (t *IndexUsageTool) ActiveDescription() string {
	return "I'm analyzing your index usage patterns to find unused or underutilized indexes"
}

func (t *IndexUsageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Specific database to analyze (optional, analyzes all if not specified)",
			},
			"table": map[string]interface{}{
				"type":        "string",
				"description": "Specific table to analyze (optional, analyzes all if not specified)",
			},
		},
		"required": []string{},
	}
}

func (t *IndexUsageTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result IndexUsageResult

	database, _ := args["database"].(string)
	table, _ := args["table"].(string)

	// Build query to join index_usage_statistics with table_indexes
	query := `
		SELECT
			t.database_name,
			t.schema_name,
			ti.descriptor_name as table_name,
			ti.index_name,
			ti.index_type,
			COALESCE(ius.total_reads, 0) as total_reads,
			ius.last_read
		FROM crdb_internal.table_indexes ti
		JOIN crdb_internal.tables t ON ti.descriptor_id = t.table_id
		LEFT JOIN crdb_internal.index_usage_statistics ius
			ON ti.descriptor_id = ius.table_id
			AND ti.index_id = ius.index_id
		WHERE t.database_name NOT IN ('system', 'information_schema', 'crdb_internal', 'pg_catalog', 'pg_extension')
	`

	// Add filters if specified
	if database != "" {
		query += fmt.Sprintf(" AND t.database_name = '%s'", database)
	}
	if table != "" {
		query += fmt.Sprintf(" AND ti.descriptor_name = '%s'", table)
	}

	query += " ORDER BY total_reads ASC, ti.descriptor_name, ti.index_name"

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query index usage: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var idx IndexUsageInfo
		var lastRead *time.Time

		if err := rows.Scan(
			&idx.DatabaseName,
			&idx.SchemaName,
			&idx.TableName,
			&idx.IndexName,
			&idx.IndexType,
			&idx.TotalReads,
			&lastRead,
		); err != nil {
			return nil, fmt.Errorf("failed to scan index usage row: %w", err)
		}

		idx.LastRead = lastRead
		if lastRead != nil {
			days := int(now.Sub(*lastRead).Hours() / 24)
			idx.DaysSinceUse = &days
		}

		// Categorize indexes
		if idx.TotalReads == 0 {
			// Skip primary indexes - they're always needed
			if idx.IndexType != "primary" {
				result.UnusedIndexes = append(result.UnusedIndexes, idx)
			}
		} else if idx.TotalReads < 100 && idx.IndexType != "primary" {
			result.LowUseIndexes = append(result.LowUseIndexes, idx)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating index usage rows: %w", err)
	}

	result.Count = len(result.UnusedIndexes) + len(result.LowUseIndexes)

	if result.Count == 0 {
		result.Note = "All indexes are being actively used - no optimization opportunities found"
	} else {
		result.Note = fmt.Sprintf("Found %d unused indexes and %d low-use indexes that may be candidates for removal",
			len(result.UnusedIndexes), len(result.LowUseIndexes))
	}

	return result, nil
}
