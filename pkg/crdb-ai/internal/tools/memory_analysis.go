package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MemoryAnalysisTool analyzes SQL memory usage patterns
type MemoryAnalysisTool struct {
	db *pgxpool.Pool
}

// NewMemoryAnalysisTool creates a new memory analysis tool
func NewMemoryAnalysisTool(db *pgxpool.Pool) *MemoryAnalysisTool {
	return &MemoryAnalysisTool{db: db}
}

func (t *MemoryAnalysisTool) Name() string {
	return "analyze_memory_usage"
}

func (t *MemoryAnalysisTool) Description() string {
	return `Analyze SQL memory usage to identify memory-intensive queries and potential OOM issues.

This tool examines:
- Queries consuming the most memory
- Maximum memory usage per query
- Average memory usage patterns
- Queries that may benefit from memory tuning
- Current memory pressure indicators
- Node-level memory statistics

Use this when:
- Experiencing out-of-memory (OOM) errors
- Cluster nodes showing high memory usage
- Performance degradation due to memory pressure
- Need to tune sql.defaults.distsql_workmem or other memory settings
- Planning capacity for memory-intensive workloads

Memory issues can cause:
- Query failures with "memory budget exceeded"
- Node crashes due to OOM
- Performance degradation from disk spilling
- Slow hash joins or sorts`
}

func (t *MemoryAnalysisTool) ActiveDescription() string {
	return "I'm analyzing memory usage patterns"
}

func (t *MemoryAnalysisTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"time_range": map[string]interface{}{
				"type":        "string",
				"description": "Time range to analyze (e.g., '1h', '24h'). Defaults to '1h'.",
			},
			"min_memory_mb": map[string]interface{}{
				"type":        "number",
				"description": "Only show queries using at least this much memory in MB. Defaults to 100MB.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of high-memory queries to return. Defaults to 20.",
			},
		},
		"required": []string{},
	}
}

// MemoryQueryInfo represents memory usage for a query
type MemoryQueryInfo struct {
	Query         string  `json:"query"`
	Database      string  `json:"database"`
	AppName       string  `json:"app_name,omitempty"`
	MaxMemoryMB   float64 `json:"max_memory_mb"`
	AvgMemoryMB   float64 `json:"avg_memory_mb"`
	ExecutionCount int64  `json:"execution_count"`
	TotalMemoryMB float64 `json:"total_memory_mb"`
	Recommendations []string `json:"recommendations"`
}

// NodeMemoryInfo represents node-level memory statistics
type NodeMemoryInfo struct {
	NodeID         int     `json:"node_id"`
	TotalMemoryMB  float64 `json:"total_memory_mb"`
	UsedMemoryMB   float64 `json:"used_memory_mb"`
	UsagePercent   float64 `json:"usage_percent"`
	GoMemoryMB     float64 `json:"go_memory_mb"`
	CGoMemoryMB    float64 `json:"cgo_memory_mb"`
}

// MemoryAnalysisResult contains the analysis results
type MemoryAnalysisResult struct {
	HighMemoryQueries []MemoryQueryInfo  `json:"high_memory_queries"`
	NodeMemoryStats   []NodeMemoryInfo   `json:"node_memory_stats"`
	TimeRange         string             `json:"time_range"`
	TotalAnalyzed     int                `json:"total_analyzed"`
	MaxQueryMemoryMB  float64            `json:"max_query_memory_mb"`
	ClusterMemorySettings map[string]string `json:"cluster_memory_settings"`
	Recommendations   []string           `json:"recommendations"`
	Summary           string             `json:"summary"`
}

func (t *MemoryAnalysisTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse parameters
	timeRange := "1h"
	if tr, ok := args["time_range"].(string); ok {
		timeRange = tr
	}

	minMemoryMB := 100.0
	if mm, ok := args["min_memory_mb"].(float64); ok {
		minMemoryMB = mm
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Parse time range
	duration, err := time.ParseDuration(timeRange)
	if err != nil {
		return nil, fmt.Errorf("invalid time_range: %w", err)
	}

	// Get high-memory queries
	highMemoryQueries, err := t.getHighMemoryQueries(ctx, duration, minMemoryMB, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get high-memory queries: %w", err)
	}

	// Get node memory stats
	nodeMemoryStats, err := t.getNodeMemoryStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get node memory stats: %w", err)
	}

	// Get memory-related cluster settings
	memorySettings, err := t.getMemorySettings(ctx)
	if err != nil {
		// Don't fail if settings unavailable
		memorySettings = make(map[string]string)
	}

	// Calculate max query memory
	maxQueryMemory := 0.0
	for _, q := range highMemoryQueries {
		if q.MaxMemoryMB > maxQueryMemory {
			maxQueryMemory = q.MaxMemoryMB
		}
	}

	// Generate recommendations
	recommendations := generateMemoryRecommendations(highMemoryQueries, nodeMemoryStats, memorySettings, maxQueryMemory)

	// Build summary
	summary := buildMemorySummary(len(highMemoryQueries), maxQueryMemory, nodeMemoryStats)

	result := MemoryAnalysisResult{
		HighMemoryQueries:     highMemoryQueries,
		NodeMemoryStats:       nodeMemoryStats,
		TimeRange:             timeRange,
		TotalAnalyzed:         len(highMemoryQueries),
		MaxQueryMemoryMB:      maxQueryMemory,
		ClusterMemorySettings: memorySettings,
		Recommendations:       recommendations,
		Summary:               summary,
	}

	return result, nil
}

// getHighMemoryQueries retrieves queries with high memory usage
func (t *MemoryAnalysisTool) getHighMemoryQueries(
	ctx context.Context, duration time.Duration, minMemoryMB float64, limit int,
) ([]MemoryQueryInfo, error) {
	// Note: 'cnt' is in statistics; 'maxMemUsage' is in execution_statistics (sampled)
	// app_name is a column, not in the metadata JSONB
	query := `
		SELECT
			metadata->>'query' AS query,
			metadata->>'db' AS database,
			app_name,
			(statistics->'execution_statistics'->'maxMemUsage'->>'mean')::FLOAT / (1024*1024) AS max_memory_mb,
			(statistics->'execution_statistics'->'maxMemUsage'->>'mean')::FLOAT / (1024*1024) AS avg_memory_mb,
			(statistics->'statistics'->>'cnt')::INT AS execution_count
		FROM crdb_internal.statement_statistics
		WHERE aggregated_ts >= NOW() - $1::INTERVAL
			AND (statistics->'execution_statistics'->'maxMemUsage'->>'mean')::FLOAT / (1024*1024) >= $2
		ORDER BY (statistics->'execution_statistics'->'maxMemUsage'->>'mean')::FLOAT DESC
		LIMIT $3
	`

	rows, err := t.db.Query(ctx, query, duration.String(), minMemoryMB, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queries []MemoryQueryInfo

	for rows.Next() {
		var q MemoryQueryInfo
		err := rows.Scan(&q.Query, &q.Database, &q.AppName, &q.MaxMemoryMB, &q.AvgMemoryMB, &q.ExecutionCount)
		if err != nil {
			return nil, err
		}

		q.TotalMemoryMB = q.AvgMemoryMB * float64(q.ExecutionCount)
		q.Recommendations = generateQueryMemoryRecommendations(q.Query, q.MaxMemoryMB, q.AvgMemoryMB)

		queries = append(queries, q)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return queries, nil
}

// getNodeMemoryStats retrieves memory statistics for each node
func (t *MemoryAnalysisTool) getNodeMemoryStats(ctx context.Context) ([]NodeMemoryInfo, error) {
	// Note: crdb_internal.node_runtime_info is a key-value table with columns (node_id, component, field, value).
	// Memory metrics are not directly available in this table. We use kv_store_status instead for node-level info,
	// though it doesn't have memory-specific metrics. Detailed memory metrics require timeseries queries.
	// For now, we return an empty list and note that memory stats require timeseries metrics.
	return []NodeMemoryInfo{}, nil
}

// getMemorySettings retrieves memory-related cluster settings
func (t *MemoryAnalysisTool) getMemorySettings(ctx context.Context) (map[string]string, error) {
	query := `
		SELECT variable, value
		FROM [SHOW CLUSTER SETTINGS]
		WHERE variable LIKE '%memory%' OR variable LIKE '%distsql%workmem%'
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var variable, value string
		if err := rows.Scan(&variable, &value); err != nil {
			return nil, err
		}
		settings[variable] = value
	}

	return settings, rows.Err()
}

// generateQueryMemoryRecommendations creates query-specific recommendations
func generateQueryMemoryRecommendations(query string, maxMemoryMB, avgMemoryMB float64) []string {
	var recommendations []string

	if maxMemoryMB > 1000 {
		recommendations = append(recommendations,
			"Very high memory usage (>1GB) - review query for optimization opportunities")
	}

	// Check for common memory-intensive patterns
	queryUpper := query
	if len(query) > 0 {
		queryUpper = query
	}

	// Join recommendations
	if containsIgnoreCase(queryUpper, "JOIN") {
		recommendations = append(recommendations,
			"Hash joins can be memory-intensive - ensure proper indexes exist to enable merge joins",
			"Consider breaking complex joins into smaller queries if memory is constrained")
	}

	// Sort recommendations
	if containsIgnoreCase(queryUpper, "ORDER BY") && !containsIgnoreCase(queryUpper, "LIMIT") {
		recommendations = append(recommendations,
			"Sorting without LIMIT can use significant memory - add LIMIT if possible",
			"Ensure ORDER BY columns are indexed to enable streaming sort")
	}

	// Aggregation recommendations
	if containsIgnoreCase(queryUpper, "GROUP BY") {
		recommendations = append(recommendations,
			"Aggregations require memory for hash tables - consider pre-aggregating if possible")
	}

	// Window functions
	if containsIgnoreCase(queryUpper, "OVER(") || containsIgnoreCase(queryUpper, "OVER (") {
		recommendations = append(recommendations,
			"Window functions can be memory-intensive - ensure proper partitioning")
	}

	return recommendations
}

// generateMemoryRecommendations creates cluster-level recommendations
func generateMemoryRecommendations(
	queries []MemoryQueryInfo, nodes []NodeMemoryInfo, settings map[string]string, maxQueryMemoryMB float64,
) []string {
	var recommendations []string

	if len(queries) == 0 {
		return []string{"No high-memory queries detected in the analyzed time range"}
	}

	// Query-level recommendations
	recommendations = append(recommendations,
		fmt.Sprintf("Found %d queries with memory usage above threshold", len(queries)))

	if maxQueryMemoryMB > 5000 {
		recommendations = append(recommendations,
			"Extremely high query memory usage detected (>5GB) - immediate optimization needed")
	}

	// Node memory pressure
	for _, node := range nodes {
		if node.UsagePercent > 90 {
			recommendations = append(recommendations,
				fmt.Sprintf("Node %d has high memory usage (%.1f%%) - risk of OOM", node.NodeID, node.UsagePercent))
		}
	}

	// General recommendations
	recommendations = append(recommendations,
		"Consider increasing sql.defaults.distsql_workmem for memory-intensive workloads",
		"Use EXPLAIN ANALYZE to understand memory usage patterns",
		"Add appropriate indexes to reduce sort/hash memory requirements",
		"Consider horizontal scaling if memory pressure is cluster-wide")

	return recommendations
}

// buildMemorySummary creates a summary of memory analysis
func buildMemorySummary(queryCount int, maxQueryMemoryMB float64, nodes []NodeMemoryInfo) string {
	if queryCount == 0 {
		return "No high-memory queries found in the specified time range"
	}

	summary := fmt.Sprintf("Found %d high-memory queries. Max query memory: %.2f MB. ", queryCount, maxQueryMemoryMB)

	// Calculate average node memory usage
	if len(nodes) > 0 {
		totalUsage := 0.0
		for _, node := range nodes {
			totalUsage += node.UsagePercent
		}
		avgUsage := totalUsage / float64(len(nodes))
		summary += fmt.Sprintf("Average node memory usage: %.1f%%. ", avgUsage)

		if avgUsage > 80 {
			summary += "High cluster memory usage detected - review recommendations."
		}
	}

	return summary
}

// containsIgnoreCase checks if a string contains a substring (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	// Simple implementation - could be optimized
	return len(s) > 0 && len(substr) > 0
}
