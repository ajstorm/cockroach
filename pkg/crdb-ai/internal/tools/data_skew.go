package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DataSkewTool detects data skew across ranges
type DataSkewTool struct {
	db *pgxpool.Pool
}

// NewDataSkewTool creates a new data skew tool
func NewDataSkewTool(db *pgxpool.Pool) *DataSkewTool {
	return &DataSkewTool{db: db}
}

// TableSkew represents skew information for a table
type TableSkew struct {
	DatabaseName      string  `json:"database_name"`
	TableName         string  `json:"table_name"`
	RangeCount        int     `json:"range_count"`
	MinRangeSizeMB    float64 `json:"min_range_size_mb"`
	MaxRangeSizeMB    float64 `json:"max_range_size_mb"`
	AvgRangeSizeMB    float64 `json:"avg_range_size_mb"`
	StdDevSizeMB      float64 `json:"std_dev_size_mb"`
	SkewRatio         float64 `json:"skew_ratio"`
	HasSignificantSkew bool   `json:"has_significant_skew"`
}

// DataSkewResult contains data skew analysis
type DataSkewResult struct {
	Tables       []TableSkew `json:"tables"`
	SkewedTables int         `json:"skewed_tables"`
	Note         string      `json:"note"`
}

func (t *DataSkewTool) Name() string {
	return "analyze_skew"
}

func (t *DataSkewTool) Description() string {
	return "Detect data skew across ranges that could indicate hotspotting or unbalanced data distribution"
}

func (t *DataSkewTool) ActiveDescription() string {
	return "I'm detecting any data skew that might indicate hotspotting or imbalanced distribution"
}

func (t *DataSkewTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"database": map[string]interface{}{
				"type":        "string",
				"description": "Filter by database name (optional)",
			},
			"skew_threshold": map[string]interface{}{
				"type":        "number",
				"description": "Skew ratio threshold (default: 3.0, meaning max is 3x average)",
			},
		},
		"required": []string{},
	}
}

func (t *DataSkewTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result DataSkewResult

	database, _ := args["database"].(string)
	skewThreshold := 3.0
	if threshold, ok := args["skew_threshold"].(float64); ok {
		skewThreshold = threshold
	}

	// Analyze skew for each table
	query := `
		SELECT
			t.database_name,
			t.name as table_name,
			COUNT(DISTINCT r.range_id) as range_count,
			MIN(r.range_size) as min_size,
			MAX(r.range_size) as max_size,
			AVG(r.range_size) as avg_size,
			STDDEV(r.range_size) as stddev_size
		FROM crdb_internal.ranges r
		JOIN crdb_internal.table_spans ts ON r.start_key >= ts.start_key AND r.start_key < ts.end_key
		JOIN crdb_internal.tables t ON ts.descriptor_id = t.table_id
		WHERE ts.dropped = false
	`

	if database != "" {
		query += fmt.Sprintf(" AND t.database_name = '%s'", database)
	}

	query += ` GROUP BY t.database_name, t.name HAVING COUNT(DISTINCT r.range_id) > 1 ORDER BY stddev_size DESC NULLS LAST`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query data skew: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ts TableSkew
		var minSize, maxSize, avgSize int64
		var stdDev *float64

		if err := rows.Scan(
			&ts.DatabaseName,
			&ts.TableName,
			&ts.RangeCount,
			&minSize,
			&maxSize,
			&avgSize,
			&stdDev,
		); err != nil {
			return nil, fmt.Errorf("failed to scan data skew row: %w", err)
		}

		ts.MinRangeSizeMB = float64(minSize) / (1024 * 1024)
		ts.MaxRangeSizeMB = float64(maxSize) / (1024 * 1024)
		ts.AvgRangeSizeMB = float64(avgSize) / (1024 * 1024)

		if stdDev != nil {
			ts.StdDevSizeMB = *stdDev / (1024 * 1024)
		}

		// Calculate skew ratio (max / avg)
		if avgSize > 0 {
			ts.SkewRatio = float64(maxSize) / float64(avgSize)
		}

		// Check if skew is significant
		ts.HasSignificantSkew = ts.SkewRatio > skewThreshold || (stdDev != nil && *stdDev > float64(avgSize))

		if ts.HasSignificantSkew {
			result.SkewedTables++
		}

		result.Tables = append(result.Tables, ts)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating data skew rows: %w", err)
	}

	if result.SkewedTables == 0 {
		result.Note = "No significant data skew detected. Data is well-distributed across ranges."
	} else {
		result.Note = fmt.Sprintf("Found %d tables with significant data skew (ratio > %.1f). Consider reviewing partitioning or split points.",
			result.SkewedTables, skewThreshold)
	}

	return result, nil
}
