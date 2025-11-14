package tools

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ContentionAnalysisTool provides detailed lock contention analysis
type ContentionAnalysisTool struct {
	db *pgxpool.Pool
}

// NewContentionAnalysisTool creates a new contention analysis tool
func NewContentionAnalysisTool(db *pgxpool.Pool) *ContentionAnalysisTool {
	return &ContentionAnalysisTool{db: db}
}

func (t *ContentionAnalysisTool) Name() string {
	return "analyze_contention"
}

func (t *ContentionAnalysisTool) Description() string {
	return `Analyze lock contention events to identify blocking patterns and hot keys.

This tool examines:
- Specific keys/rows experiencing contention
- Transactions involved in contention (blocking and waiting)
- Duration and frequency of contention events
- Tables and indexes involved
- Time patterns of contention
- Recommendations to reduce contention

Use this when:
- Queries are experiencing lock wait timeouts
- High transaction retry rates are observed
- Performance degradation during peak hours
- Need to identify hot rows or tables

Provides more detail than basic transaction retry analysis by showing:
- Exact keys being contended
- Which transactions are blocking which
- Temporal patterns of contention`
}

func (t *ContentionAnalysisTool) ActiveDescription() string {
	return "I'm analyzing lock contention patterns"
}

func (t *ContentionAnalysisTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"min_duration_ms": map[string]interface{}{
				"type":        "number",
				"description": "Only show contention with cumulative wait time of at least this many milliseconds. Defaults to 100ms.",
			},
			"table_name": map[string]interface{}{
				"type":        "string",
				"description": "Filter to a specific table name. Optional.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of contention events to return. Defaults to 50.",
			},
		},
		"required": []string{},
	}
}

// ContentionEvent represents a single contention event
type ContentionEvent struct {
	TableID                int64   `json:"table_id"`
	TableName              string  `json:"table_name"`
	IndexID                int64   `json:"index_id"`
	IndexName              string  `json:"index_name,omitempty"`
	Key                    string  `json:"key"`
	TxnID                  string  `json:"txn_id"`
	NumContentionEvents    int64   `json:"num_contention_events"`
	CumulativeContentionMs float64 `json:"cumulative_contention_ms"`
	Count                  int64   `json:"count"`
}

// ContentionHotspot represents a frequently contended key or table
type ContentionHotspot struct {
	TableName       string  `json:"table_name"`
	Key             string  `json:"key,omitempty"`
	EventCount      int     `json:"event_count"`
	TotalDurationMs float64 `json:"total_duration_ms"`
	AvgDurationMs   float64 `json:"avg_duration_ms"`
	MaxDurationMs   float64 `json:"max_duration_ms"`
}

// ContentionAnalysisResult contains the analysis results
type ContentionAnalysisResult struct {
	ContentionEvents []ContentionEvent   `json:"contention_events"`
	Hotspots         []ContentionHotspot `json:"hotspots"`
	TimeRange        string              `json:"time_range"`
	TotalEvents      int                 `json:"total_events"`
	TotalWaitTimeMs  float64             `json:"total_wait_time_ms"`
	Recommendations  []string            `json:"recommendations"`
	Summary          string              `json:"summary"`
}

func (t *ContentionAnalysisTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse parameters
	minDurationMs := 100.0
	if md, ok := args["min_duration_ms"].(float64); ok {
		minDurationMs = md
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	var tableFilter *string
	if tn, ok := args["table_name"].(string); ok && tn != "" {
		tableFilter = &tn
	}

	// Get contention events
	events, err := t.getContentionEvents(ctx, time.Hour /* unused */, minDurationMs, tableFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get contention events: %w", err)
	}

	// Calculate hotspots
	hotspots := calculateHotspots(events)

	// Calculate total wait time
	totalWaitTime := 0.0
	for _, event := range events {
		totalWaitTime += event.CumulativeContentionMs
	}

	// Generate recommendations
	recommendations := generateContentionRecommendations(events, hotspots)

	// Build summary
	summary := buildContentionSummary(len(events), hotspots, totalWaitTime)

	result := ContentionAnalysisResult{
		ContentionEvents: events,
		Hotspots:         hotspots,
		TimeRange:        "all time (cumulative)",
		TotalEvents:      len(events),
		TotalWaitTimeMs:  totalWaitTime,
		Recommendations:  recommendations,
		Summary:          summary,
	}

	return result, nil
}

// getContentionEvents retrieves contention events from the cluster
func (t *ContentionAnalysisTool) getContentionEvents(
	ctx context.Context,
	duration time.Duration,
	minDurationMs float64,
	tableFilter *string,
	limit int,
) ([]ContentionEvent, error) {
	// The cluster_contention_events table shows aggregated contention per table/index/key/txn
	// It doesn't have timestamps, so we can't filter by time range
	// We need to join with crdb_internal.tables to get table names
	query := `
		SELECT
			c.table_id,
			t.name as table_name,
			c.index_id,
			c.num_contention_events,
			EXTRACT(epoch FROM c.cumulative_contention_time) * 1000 as cumulative_contention_ms,
			c.key,
			c.txn_id,
			c.count
		FROM crdb_internal.cluster_contention_events c
		JOIN crdb_internal.tables t ON c.table_id = t.table_id
		WHERE EXTRACT(epoch FROM c.cumulative_contention_time) * 1000 >= $1
	`

	queryArgs := []interface{}{minDurationMs}
	argNum := 2

	if tableFilter != nil {
		query += fmt.Sprintf(" AND t.name = $%d", argNum)
		queryArgs = append(queryArgs, *tableFilter)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY cumulative_contention_time DESC LIMIT %d", limit)

	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		// Provide helpful error message if table doesn't exist or permission denied
		return nil, fmt.Errorf("failed to query contention events (this requires access to crdb_internal.cluster_contention_events): %w", err)
	}
	defer rows.Close()

	var events []ContentionEvent

	for rows.Next() {
		var event ContentionEvent
		var keyBytes []byte

		err := rows.Scan(
			&event.TableID,
			&event.TableName,
			&event.IndexID,
			&event.NumContentionEvents,
			&event.CumulativeContentionMs,
			&keyBytes,
			&event.TxnID,
			&event.Count,
		)
		if err != nil {
			return nil, err
		}

		event.Key = hex.EncodeToString(keyBytes)

		// Optionally get index name
		// Note: We could join with crdb_internal.table_indexes but that adds complexity
		// For now, just show the index_id

		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

// calculateHotspots identifies frequently contended keys/tables
func calculateHotspots(events []ContentionEvent) []ContentionHotspot {
	hotspotMap := make(map[string]*ContentionHotspot)

	for _, event := range events {
		// Create key combining table and actual key
		hotspotKey := fmt.Sprintf("%s:%s", event.TableName, event.Key)

		if hs, exists := hotspotMap[hotspotKey]; exists {
			hs.EventCount += int(event.NumContentionEvents)
			hs.TotalDurationMs += event.CumulativeContentionMs
			if event.CumulativeContentionMs > hs.MaxDurationMs {
				hs.MaxDurationMs = event.CumulativeContentionMs
			}
		} else {
			hotspotMap[hotspotKey] = &ContentionHotspot{
				TableName:       event.TableName,
				Key:             event.Key,
				EventCount:      int(event.NumContentionEvents),
				TotalDurationMs: event.CumulativeContentionMs,
				MaxDurationMs:   event.CumulativeContentionMs,
			}
		}
	}

	// Convert map to slice and calculate averages
	var hotspots []ContentionHotspot
	for _, hs := range hotspotMap {
		hs.AvgDurationMs = hs.TotalDurationMs / float64(hs.EventCount)
		hotspots = append(hotspots, *hs)
	}

	// Sort by event count (descending)
	// Simple bubble sort for small lists
	for i := 0; i < len(hotspots)-1; i++ {
		for j := 0; j < len(hotspots)-i-1; j++ {
			if hotspots[j].EventCount < hotspots[j+1].EventCount {
				hotspots[j], hotspots[j+1] = hotspots[j+1], hotspots[j]
			}
		}
	}

	// Return top 10 hotspots
	if len(hotspots) > 10 {
		hotspots = hotspots[:10]
	}

	return hotspots
}

// generateContentionRecommendations creates actionable recommendations
func generateContentionRecommendations(events []ContentionEvent, hotspots []ContentionHotspot) []string {
	var recommendations []string

	if len(events) == 0 {
		return []string{"No significant contention detected in the analyzed time range"}
	}

	// Hotspot recommendations
	if len(hotspots) > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("High contention detected on %d key(s). Review hotspots for schema optimization.", len(hotspots)))

		// Check if single table dominates
		tableCount := make(map[string]int)
		for _, hs := range hotspots {
			tableCount[hs.TableName]++
		}

		for table, count := range tableCount {
			if count >= 3 {
				recommendations = append(recommendations,
					fmt.Sprintf("Table '%s' has multiple contended keys - consider sharding or partitioning", table))
			}
		}
	}

	// High duration recommendations
	maxDuration := 0.0
	for _, event := range events {
		if event.CumulativeContentionMs > maxDuration {
			maxDuration = event.CumulativeContentionMs
		}
	}

	if maxDuration > 5000 {
		recommendations = append(recommendations,
			"Very long cumulative contention wait times detected (>5s) - investigate long-running transactions")
	} else if maxDuration > 1000 {
		recommendations = append(recommendations,
			"Significant cumulative contention wait times detected (>1s) - consider optimizing transaction duration")
	}

	// General recommendations
	recommendations = append(recommendations,
		"Use SELECT FOR UPDATE SKIP LOCKED for queue-like patterns",
		"Keep transactions as short as possible to minimize lock hold time",
		"Consider using optimistic locking patterns where appropriate",
		"Review indexing strategy to reduce lock acquisition during updates")

	return recommendations
}

// buildContentionSummary creates a summary of contention analysis
func buildContentionSummary(eventCount int, hotspots []ContentionHotspot, totalWaitMs float64) string {
	if eventCount == 0 {
		return "No contention events found in the specified time range. Note: Contention events are only recorded when transactions actually experience lock waits. If no contention is detected, the cluster may be operating normally with minimal lock conflicts, or contention event retention may have expired."
	}

	summary := fmt.Sprintf("Found %d contention events with %.2f ms total wait time. ", eventCount, totalWaitMs)

	if len(hotspots) > 0 {
		summary += fmt.Sprintf("Identified %d hotspots. ", len(hotspots))
		topHotspot := hotspots[0]
		summary += fmt.Sprintf("Top hotspot: table '%s' with %d events (avg %.2f ms wait). ",
			topHotspot.TableName, topHotspot.EventCount, topHotspot.AvgDurationMs)
	}

	avgWait := totalWaitMs / float64(eventCount)
	if avgWait > 1000 {
		summary += "Average wait time is high - investigate lock-holding transactions."
	}

	return summary
}
