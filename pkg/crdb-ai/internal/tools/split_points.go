package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SplitPointsTool suggests recommended split points for ranges
type SplitPointsTool struct {
	db *pgxpool.Pool
}

// NewSplitPointsTool creates a new split points tool
func NewSplitPointsTool(db *pgxpool.Pool) *SplitPointsTool {
	return &SplitPointsTool{db: db}
}

// SplitPointRecommendation represents a recommended split point
type SplitPointRecommendation struct {
	DatabaseName string  `json:"database_name"`
	TableName    string  `json:"table_name"`
	RangeID      int     `json:"range_id"`
	RangeSizeMB  float64 `json:"range_size_mb"`
	Reason       string  `json:"reason"`
	Priority     string  `json:"priority"`
}

// SplitPointsResult contains split point recommendations
type SplitPointsResult struct {
	Recommendations []SplitPointRecommendation `json:"recommendations"`
	Count           int                        `json:"count"`
	Note            string                     `json:"note"`
}

func (t *SplitPointsTool) Name() string {
	return "check_split_points"
}

func (t *SplitPointsTool) Description() string {
	return "Identify ranges that should be split based on size or access patterns"
}

func (t *SplitPointsTool) ActiveDescription() string {
	return "I'm finding ranges that might need to be split"
}

func (t *SplitPointsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"size_threshold_mb": map[string]interface{}{
				"type":        "integer",
				"description": "Size threshold in MB for split recommendations (default: 512)",
			},
		},
		"required": []string{},
	}
}

func (t *SplitPointsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result SplitPointsResult

	sizeThreshold := 512 * 1024 * 1024 // 512MB in bytes
	if threshold, ok := args["size_threshold_mb"].(float64); ok {
		sizeThreshold = int(threshold) * 1024 * 1024
	}

	// Find ranges that exceed the size threshold
	query := fmt.Sprintf(`
		SELECT
			t.database_name,
			t.name as table_name,
			r.range_id,
			r.range_size
		FROM crdb_internal.ranges r
		JOIN crdb_internal.table_spans ts ON r.start_key >= ts.start_key AND r.start_key < ts.end_key
		JOIN crdb_internal.tables t ON ts.descriptor_id = t.table_id
		WHERE r.range_size > %d
			AND ts.dropped = false
		ORDER BY r.range_size DESC
		LIMIT 50
	`, sizeThreshold)

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query split points: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rec SplitPointRecommendation
		var rangeSize int64

		if err := rows.Scan(
			&rec.DatabaseName,
			&rec.TableName,
			&rec.RangeID,
			&rangeSize,
		); err != nil {
			return nil, fmt.Errorf("failed to scan split point row: %w", err)
		}

		rec.RangeSizeMB = float64(rangeSize) / (1024 * 1024)

		// Determine priority based on size
		if rangeSize > int64(2*sizeThreshold) {
			rec.Priority = "high"
			rec.Reason = fmt.Sprintf("Range is %.0fMB, significantly over threshold", rec.RangeSizeMB)
		} else if rangeSize > int64(sizeThreshold) {
			rec.Priority = "medium"
			rec.Reason = fmt.Sprintf("Range is %.0fMB, over threshold", rec.RangeSizeMB)
		}

		result.Recommendations = append(result.Recommendations, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating split point rows: %w", err)
	}

	result.Count = len(result.Recommendations)

	if result.Count == 0 {
		result.Note = "No ranges found that need splitting. All ranges are within acceptable size limits."
	} else {
		result.Note = fmt.Sprintf("Found %d ranges that should be considered for splitting", result.Count)
	}

	return result, nil
}
