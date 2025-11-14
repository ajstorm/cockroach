package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryInsightsTool analyzes query patterns and provides optimization recommendations
type QueryInsightsTool struct {
	db *pgxpool.Pool
}

// NewQueryInsightsTool creates a new query insights tool
func NewQueryInsightsTool(db *pgxpool.Pool) *QueryInsightsTool {
	return &QueryInsightsTool{db: db}
}

func (t *QueryInsightsTool) Name() string {
	return "query_insights"
}

func (t *QueryInsightsTool) Description() string {
	return `Analyze query patterns and provide specific optimization recommendations.

This tool examines recent queries to identify:
- Anti-patterns like SELECT *, missing WHERE clauses, or inefficient JOINs
- Queries that might benefit from indexes
- Queries with high latency or execution counts
- Full table scans on large tables
- Queries that could use query hints for better performance

Use this when:
- Performance issues are reported but root cause is unclear
- You want to proactively identify optimization opportunities
- A specific query is slow and you need recommendations
- You want to validate query efficiency across the workload

Results include specific SQL recommendations and explanations.`
}

func (t *QueryInsightsTool) ActiveDescription() string {
	return "I'm analyzing query patterns for optimization opportunities"
}

func (t *QueryInsightsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start of time range. Supports RFC3339 (e.g., '2024-12-10T18:10:00Z'), relative (e.g., '2h ago', '7d ago'), or 'now'. If provided with end_time, defines an explicit time window.",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End of time range. Supports same formats as start_time. Defaults to 'now' if start_time is provided.",
			},
			"time_range": map[string]interface{}{
				"type":        "string",
				"description": "Relative time range from now (e.g., '1h', '24h', '7d'). Defaults to '1h'. Ignored if start_time is provided.",
			},
			"min_latency_ms": map[string]interface{}{
				"type":        "number",
				"description": "Only analyze queries with latency above this threshold in milliseconds. Defaults to 100ms.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of queries to analyze. Defaults to 20.",
			},
			"app_name": map[string]interface{}{
				"type":        "string",
				"description": "Filter by application name. Optional.",
			},
		},
		"required": []string{},
	}
}

// QueryInsight represents an insight about a specific query
type QueryInsight struct {
	Query           string   `json:"query"`
	Database        string   `json:"database"`
	AppName         string   `json:"app_name,omitempty"`
	AvgLatencyMs    float64  `json:"avg_latency_ms"`
	TotalCount      int64    `json:"total_count"`
	Issues          []string `json:"issues"`
	Recommendations []string `json:"recommendations"`
	Severity        string   `json:"severity"` // "high", "medium", "low"
}

// QueryInsightsResult contains the analysis results
type QueryInsightsResult struct {
	Insights      []QueryInsight `json:"insights"`
	TimeRange     string         `json:"time_range"`
	TotalAnalyzed int            `json:"total_analyzed"`
	Summary       string         `json:"summary"`
}

func (t *QueryInsightsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse parameters
	minLatencyMs := 100.0
	if ml, ok := args["min_latency_ms"].(float64); ok {
		minLatencyMs = ml
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	var appNameFilter *string
	if an, ok := args["app_name"].(string); ok && an != "" {
		appNameFilter = &an
	}

	// Parse time range parameters
	var startTime, endTime *time.Time
	now := time.Now()
	timeRangeStr := "1h" // Default for display

	if st, ok := args["start_time"].(string); ok && st != "" {
		t, err := ParseTimeArgument(st, now)
		if err != nil {
			return nil, fmt.Errorf("invalid start_time: %w", err)
		}
		startTime = &t
	}

	if et, ok := args["end_time"].(string); ok && et != "" {
		t, err := ParseTimeArgument(et, now)
		if err != nil {
			return nil, fmt.Errorf("invalid end_time: %w", err)
		}
		endTime = &t
	}

	// If time_range is provided and start_time is not, use time_range
	if startTime == nil {
		if tr, ok := args["time_range"].(string); ok && tr != "" {
			timeRangeStr = tr
		}
		duration, err := ParseExtendedDuration(timeRangeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid time_range: %w", err)
		}
		st := now.Add(-duration)
		startTime = &st
	}

	// Default end_time to now if start_time is provided but end_time is not
	if endTime == nil {
		endTime = &now
	}

	// Build time range display string
	if _, ok := args["start_time"].(string); ok {
		timeRangeStr = fmt.Sprintf("%s to %s", startTime.UTC().Format(time.RFC3339), endTime.UTC().Format(time.RFC3339))
	}

	// Query statement statistics
	// Note: 'cnt' (total count) is in statistics->cnt; latency is in statistics->svcLat->mean
	// app_name is a column, not in the metadata JSONB
	query := `
		SELECT
			metadata->>'query' AS query,
			metadata->>'db' AS database,
			app_name,
			statistics->'statistics'->'svcLat'->>'mean' AS mean_latency_seconds,
			statistics->'statistics'->>'cnt' AS count
		FROM crdb_internal.statement_statistics
		WHERE aggregated_ts >= $1
			AND aggregated_ts <= $2
			AND (statistics->'statistics'->'svcLat'->>'mean')::FLOAT * 1000 >= $3
	`

	// Subtract 1 hour from start time to account for aggregation bucket alignment.
	// Statement statistics are aggregated into hourly buckets, and aggregated_ts
	// represents the START of each bucket. A query executed at 18:35 would have
	// aggregated_ts of 18:00, so we need to look back one aggregation interval.
	adjustedStart := startTime.Add(-time.Hour)
	queryArgs := []interface{}{adjustedStart, *endTime, minLatencyMs / 1000.0} // Convert to seconds
	argNum := 4

	if appNameFilter != nil {
		query += fmt.Sprintf(" AND app_name = $%d", argNum)
		queryArgs = append(queryArgs, *appNameFilter)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY (statistics->'statistics'->'svcLat'->>'mean')::FLOAT DESC LIMIT %d", limit)

	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query statement statistics: %w", err)
	}
	defer rows.Close()

	var insights []QueryInsight

	for rows.Next() {
		var query, meanLatencyStr, countStr *string
		var database, appName *string

		err := rows.Scan(&query, &database, &appName, &meanLatencyStr, &countStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Skip rows with NULL query
		if query == nil {
			continue
		}

		// Parse numeric values (handle NULLs)
		var meanLatency float64
		var count int64
		if meanLatencyStr != nil {
			fmt.Sscanf(*meanLatencyStr, "%f", &meanLatency)
		}
		if countStr != nil {
			fmt.Sscanf(*countStr, "%d", &count)
		}

		// Get string values with defaults for NULLs
		queryStr := *query
		dbStr := ""
		if database != nil {
			dbStr = *database
		}
		appStr := ""
		if appName != nil {
			appStr = *appName
		}

		// Analyze query for issues
		issues := analyzeQueryIssues(queryStr)
		recommendations := generateRecommendations(queryStr, issues, meanLatency*1000)
		severity := determineSeverity(issues, meanLatency*1000)

		if len(issues) > 0 {
			insights = append(insights, QueryInsight{
				Query:           queryStr,
				Database:        dbStr,
				AppName:         appStr,
				AvgLatencyMs:    meanLatency * 1000, // Convert to ms
				TotalCount:      count,
				Issues:          issues,
				Recommendations: recommendations,
				Severity:        severity,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	// Build summary
	summary := buildInsightsSummary(insights)

	result := QueryInsightsResult{
		Insights:      insights,
		TimeRange:     timeRangeStr,
		TotalAnalyzed: len(insights),
		Summary:       summary,
	}

	return result, nil
}

// analyzeQueryIssues identifies potential issues in a query
func analyzeQueryIssues(query string) []string {
	var issues []string
	queryUpper := strings.ToUpper(query)
	queryLower := strings.ToLower(query)

	// Check for SELECT *
	if strings.Contains(queryUpper, "SELECT *") {
		issues = append(issues, "Using SELECT * instead of specific columns")
	}

	// Check for missing WHERE clause on SELECT
	if strings.Contains(queryUpper, "SELECT") &&
		!strings.Contains(queryUpper, "WHERE") &&
		!strings.Contains(queryUpper, "LIMIT") {
		issues = append(issues, "SELECT without WHERE clause - potential full table scan")
	}

	// Check for OR in WHERE clause (can prevent index usage)
	if strings.Contains(queryUpper, "WHERE") && strings.Contains(queryUpper, " OR ") {
		issues = append(issues, "OR condition in WHERE clause may prevent index usage")
	}

	// Check for LIKE with leading wildcard
	if strings.Contains(queryLower, "like '%") || strings.Contains(queryLower, "ilike '%") {
		issues = append(issues, "LIKE with leading wildcard prevents index usage")
	}

	// Check for functions on indexed columns
	if strings.Contains(queryUpper, "WHERE") &&
		(strings.Contains(queryUpper, "UPPER(") ||
			strings.Contains(queryUpper, "LOWER(") ||
			strings.Contains(queryUpper, "SUBSTRING(")) {
		issues = append(issues, "Function call on column in WHERE clause may prevent index usage")
	}

	// Check for implicit type conversions
	if strings.Contains(queryUpper, "::") {
		issues = append(issues, "Type casting in query may indicate schema mismatch")
	}

	// Check for NOT IN (better to use NOT EXISTS or LEFT JOIN)
	if strings.Contains(queryUpper, "NOT IN") {
		issues = append(issues, "NOT IN can be slow; consider NOT EXISTS or LEFT JOIN instead")
	}

	// Check for subquery in SELECT list
	if strings.Count(queryUpper, "SELECT") > 1 &&
		strings.Index(queryUpper, "(SELECT") < strings.Index(queryUpper, "FROM") {
		issues = append(issues, "Subquery in SELECT list - may execute once per row")
	}

	return issues
}

// generateRecommendations creates actionable recommendations based on issues
func generateRecommendations(query string, issues []string, avgLatencyMs float64) []string {
	var recommendations []string

	for _, issue := range issues {
		switch {
		case strings.Contains(issue, "SELECT *"):
			recommendations = append(recommendations,
				"Specify only needed columns to reduce data transfer and memory usage")

		case strings.Contains(issue, "full table scan"):
			recommendations = append(recommendations,
				"Add a WHERE clause to filter rows, or add a LIMIT if you only need a subset")

		case strings.Contains(issue, "OR condition"):
			recommendations = append(recommendations,
				"Consider rewriting with UNION ALL or creating separate indexes for OR conditions")

		case strings.Contains(issue, "leading wildcard"):
			recommendations = append(recommendations,
				"Use full-text search or consider suffix/prefix indexes if available")

		case strings.Contains(issue, "Function call"):
			recommendations = append(recommendations,
				"Create a computed/generated column with an index, or use expression indexes")

		case strings.Contains(issue, "Type casting"):
			recommendations = append(recommendations,
				"Ensure application uses correct data types matching schema definitions")

		case strings.Contains(issue, "NOT IN"):
			recommendations = append(recommendations,
				"Rewrite using NOT EXISTS or LEFT JOIN ... WHERE right_table.id IS NULL")

		case strings.Contains(issue, "Subquery in SELECT"):
			recommendations = append(recommendations,
				"Move subquery to JOIN or use window functions if possible")
		}
	}

	// Add latency-specific recommendations
	if avgLatencyMs > 1000 {
		recommendations = append(recommendations,
			"High latency detected - run EXPLAIN to check execution plan and index usage")
	}

	return recommendations
}

// determineSeverity assigns severity based on issues and latency
func determineSeverity(issues []string, avgLatencyMs float64) string {
	if len(issues) == 0 {
		return "low"
	}

	// High severity if multiple issues or very high latency
	if len(issues) >= 3 || avgLatencyMs > 5000 {
		return "high"
	}

	// Medium severity for moderate issues
	if len(issues) >= 2 || avgLatencyMs > 1000 {
		return "medium"
	}

	return "low"
}

// buildInsightsSummary creates a summary of findings
func buildInsightsSummary(insights []QueryInsight) string {
	if len(insights) == 0 {
		return "No significant query issues found in the analyzed time range"
	}

	highCount := 0
	mediumCount := 0
	lowCount := 0

	for _, insight := range insights {
		switch insight.Severity {
		case "high":
			highCount++
		case "medium":
			mediumCount++
		case "low":
			lowCount++
		}
	}

	summary := fmt.Sprintf("Found %d queries with optimization opportunities: ", len(insights))
	parts := []string{}

	if highCount > 0 {
		parts = append(parts, fmt.Sprintf("%d high severity", highCount))
	}
	if mediumCount > 0 {
		parts = append(parts, fmt.Sprintf("%d medium severity", mediumCount))
	}
	if lowCount > 0 {
		parts = append(parts, fmt.Sprintf("%d low severity", lowCount))
	}

	summary += strings.Join(parts, ", ")

	return summary
}
