package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TransactionRetryAnalysisTool analyzes transaction retry patterns
type TransactionRetryAnalysisTool struct {
	db *pgxpool.Pool
}

// NewTransactionRetryAnalysisTool creates a new transaction retry analysis tool
func NewTransactionRetryAnalysisTool(db *pgxpool.Pool) *TransactionRetryAnalysisTool {
	return &TransactionRetryAnalysisTool{db: db}
}

func (t *TransactionRetryAnalysisTool) Name() string {
	return "analyze_transaction_retries"
}

func (t *TransactionRetryAnalysisTool) Description() string {
	return `Analyze transaction retry patterns to identify contention and optimization opportunities.

This tool examines:
- Transactions with high retry rates
- Retry reasons (serialization conflicts, write too old, etc.)
- Tables and queries involved in retries
- Time patterns of retries
- Recommendations to reduce retry rates

Use this when:
- Applications are experiencing frequent transaction retries
- Performance degradation due to contention
- You want to optimize transaction isolation or structure
- Investigating specific retry errors

High retry rates can indicate:
- Lock contention on hot rows
- Long-running transactions blocking others
- Schema design issues (e.g., counter tables)
- Inappropriate isolation levels`
}

func (t *TransactionRetryAnalysisTool) ActiveDescription() string {
	return "I'm analyzing transaction retry patterns"
}

func (t *TransactionRetryAnalysisTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"time_range": map[string]interface{}{
				"type":        "string",
				"description": "Time range to analyze (e.g., '1h', '24h'). Defaults to '1h'.",
			},
			"min_retry_count": map[string]interface{}{
				"type":        "number",
				"description": "Only show statements with at least this many retries. Defaults to 10.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of high-retry transactions to return. Defaults to 20.",
			},
		},
		"required": []string{},
	}
}

// TransactionRetryInfo represents retry information for a transaction pattern
type TransactionRetryInfo struct {
	Query                string  `json:"query"`
	Database             string  `json:"database"`
	AppName              string  `json:"app_name,omitempty"`
	TotalExecutions      int64   `json:"total_executions"`
	TotalRetries         int64   `json:"total_retries"`
	RetryRate            float64 `json:"retry_rate_percent"`
	AvgRetriesPerExec    float64 `json:"avg_retries_per_execution"`
	MaxRetries           int64   `json:"max_retries"`
	TablesInvolved       []string `json:"tables_involved,omitempty"`
	RecommendedActions   []string `json:"recommended_actions"`
}

// TransactionRetryAnalysisResult contains the analysis results
type TransactionRetryAnalysisResult struct {
	HighRetryTransactions []TransactionRetryInfo `json:"high_retry_transactions"`
	TimeRange             string                 `json:"time_range"`
	TotalAnalyzed         int                    `json:"total_analyzed"`
	ClusterRetryRate      float64                `json:"cluster_retry_rate_percent"`
	Summary               string                 `json:"summary"`
}

func (t *TransactionRetryAnalysisTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse parameters
	timeRange := "1h"
	if tr, ok := args["time_range"].(string); ok {
		timeRange = tr
	}

	minRetryCount := 10
	if mrc, ok := args["min_retry_count"].(float64); ok {
		minRetryCount = int(mrc)
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

	// Query transaction statistics with retry information
	// Note: 'cnt' (total count) and 'maxRetries' are in statistics->'statistics', not execution_statistics
	// app_name is a column, not in the metadata JSONB
	query := `
		SELECT
			metadata->>'query' AS query,
			metadata->>'db' AS database,
			app_name,
			(statistics->'statistics'->>'cnt')::INT AS total_executions,
			(statistics->'statistics'->>'maxRetries')::INT AS total_retries,
			(statistics->'statistics'->>'maxRetries')::INT AS max_retries
		FROM crdb_internal.statement_statistics
		WHERE aggregated_ts >= NOW() - $1::INTERVAL
			AND (statistics->'statistics'->>'maxRetries')::INT >= $2
			AND (statistics->'statistics'->>'cnt')::INT > 0
		ORDER BY (statistics->'statistics'->>'maxRetries')::INT DESC
		LIMIT $3
	`

	rows, err := t.db.Query(ctx, query, duration.String(), float64(minRetryCount), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query transaction statistics: %w", err)
	}
	defer rows.Close()

	var highRetryTxns []TransactionRetryInfo
	var totalExecutions int64
	var totalRetries float64

	for rows.Next() {
		var queryStr, database, appName string
		var executions, maxRetries int64
		var retries float64

		err := rows.Scan(&queryStr, &database, &appName, &executions, &retries, &maxRetries)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		totalExecutions += executions
		totalRetries += retries

		retryRate := 0.0
		avgRetries := 0.0
		if executions > 0 {
			retryRate = (retries / float64(executions)) * 100
			avgRetries = retries / float64(executions)
		}

		// Extract tables from query
		tables := extractTablesFromQuery(queryStr)

		// Generate recommendations
		recommendations := generateRetryRecommendations(queryStr, retryRate, tables)

		highRetryTxns = append(highRetryTxns, TransactionRetryInfo{
			Query:              queryStr,
			Database:           database,
			AppName:            appName,
			TotalExecutions:    executions,
			TotalRetries:       int64(retries),
			RetryRate:          retryRate,
			AvgRetriesPerExec:  avgRetries,
			MaxRetries:         maxRetries,
			TablesInvolved:     tables,
			RecommendedActions: recommendations,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	// Calculate cluster-wide retry rate
	clusterRetryRate := 0.0
	if totalExecutions > 0 {
		clusterRetryRate = (totalRetries / float64(totalExecutions)) * 100
	}

	// Build summary
	summary := buildRetryAnalysisSummary(highRetryTxns, clusterRetryRate)

	result := TransactionRetryAnalysisResult{
		HighRetryTransactions: highRetryTxns,
		TimeRange:             timeRange,
		TotalAnalyzed:         len(highRetryTxns),
		ClusterRetryRate:      clusterRetryRate,
		Summary:               summary,
	}

	return result, nil
}

// extractTablesFromQuery attempts to extract table names from a query
func extractTablesFromQuery(query string) []string {
	var tables []string
	queryUpper := strings.ToUpper(query)

	// Simple heuristic: find FROM and JOIN clauses
	// This is not perfect but gives useful hints
	words := strings.Fields(queryUpper)
	for i := 0; i < len(words)-1; i++ {
		if words[i] == "FROM" || words[i] == "JOIN" || words[i] == "UPDATE" || words[i] == "INTO" {
			// Next word might be a table name
			tableName := strings.Trim(words[i+1], "(),;")
			if tableName != "" && tableName != "SELECT" {
				tables = append(tables, strings.ToLower(tableName))
			}
		}
	}

	return tables
}

// generateRetryRecommendations creates actionable recommendations
func generateRetryRecommendations(query string, retryRate float64, tables []string) []string {
	var recommendations []string
	queryUpper := strings.ToUpper(query)

	// High retry rate recommendations
	if retryRate > 50 {
		recommendations = append(recommendations,
			"Very high retry rate - investigate lock contention on involved tables")
	}

	// SELECT FOR UPDATE recommendations
	if strings.Contains(queryUpper, "SELECT") && strings.Contains(queryUpper, "FOR UPDATE") {
		recommendations = append(recommendations,
			"FOR UPDATE locks can cause contention - consider optimistic locking if appropriate")
	}

	// UPDATE recommendations
	if strings.Contains(queryUpper, "UPDATE") {
		recommendations = append(recommendations,
			"High retry rate on UPDATE - check for hot rows or consider using SELECT FOR UPDATE with SKIP LOCKED")
	}

	// Counter pattern
	if strings.Contains(queryUpper, "SET") && (strings.Contains(queryUpper, "+ 1") || strings.Contains(queryUpper, "- 1")) {
		recommendations = append(recommendations,
			"Counter update pattern detected - consider using separate counter table or eventual consistency")
	}

	// Long transaction recommendation
	if strings.Contains(queryUpper, "BEGIN") || strings.Contains(queryUpper, "SAVEPOINT") {
		recommendations = append(recommendations,
			"Keep transactions short - release locks quickly to reduce contention")
	}

	// General recommendations
	if retryRate > 20 {
		recommendations = append(recommendations,
			"Consider using READ COMMITTED isolation if serializable consistency is not required",
			"Review schema design - normalize hot tables or use sharding strategies",
			"Check if queries are using optimal indexes to minimize lock hold time")
	}

	// Table-specific recommendations
	if len(tables) > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("Investigate contention on tables: %s", strings.Join(tables, ", ")))
	}

	return recommendations
}

// buildRetryAnalysisSummary creates a summary of the retry analysis
func buildRetryAnalysisSummary(txns []TransactionRetryInfo, clusterRetryRate float64) string {
	if len(txns) == 0 {
		return "No high-retry transactions found in the specified time range"
	}

	summary := fmt.Sprintf("Found %d transaction patterns with high retry rates. ", len(txns))
	summary += fmt.Sprintf("Cluster-wide retry rate: %.2f%%. ", clusterRetryRate)

	// Find highest retry rate
	maxRetryRate := 0.0
	for _, txn := range txns {
		if txn.RetryRate > maxRetryRate {
			maxRetryRate = txn.RetryRate
		}
	}

	summary += fmt.Sprintf("Highest retry rate: %.2f%%. ", maxRetryRate)

	if clusterRetryRate > 10 {
		summary += "Retry rate is elevated - review recommendations to reduce contention."
	} else {
		summary += "Retry rate is within normal range."
	}

	return summary
}
