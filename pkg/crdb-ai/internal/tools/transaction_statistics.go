package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TransactionStatisticsTool provides transaction-level statistics
type TransactionStatisticsTool struct {
	db *pgxpool.Pool
}

// NewTransactionStatisticsTool creates a new transaction statistics tool
func NewTransactionStatisticsTool(db *pgxpool.Pool) *TransactionStatisticsTool {
	return &TransactionStatisticsTool{db: db}
}

// TransactionStats represents statistics for a transaction pattern
type TransactionStats struct {
	AppName           string  `json:"app_name"`
	ExecutionCount    int     `json:"execution_count"`
	AvgLatencySec     float64 `json:"avg_latency_sec"`
	P99LatencySec     float64 `json:"p99_latency_sec"`
	MaxLatencySec     float64 `json:"max_latency_sec"`
	AvgRetries        float64 `json:"avg_retries"`
	MaxRetries        int     `json:"max_retries"`
	ContentionTime    float64 `json:"contention_time_sec"`
	StatementsPerTxn  float64 `json:"statements_per_txn"`
}

// TransactionStatisticsResult contains transaction statistics
type TransactionStatisticsResult struct {
	Transactions []TransactionStats `json:"transactions"`
	TotalTxns    int                `json:"total_txns"`
	Note         string             `json:"note"`
}

func (t *TransactionStatisticsTool) Name() string {
	return "get_transaction_statistics"
}

func (t *TransactionStatisticsTool) Description() string {
	return "Get statistics about transaction patterns including latency, retries, and contention"
}

func (t *TransactionStatisticsTool) ActiveDescription() string {
	return "I'm analyzing transaction patterns including latency and contention"
}

func (t *TransactionStatisticsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of transaction patterns to return (default: 10)",
			},
			"order_by": map[string]interface{}{
				"type":        "string",
				"description": "Order by 'latency', 'count', or 'contention' (default: latency)",
				"enum":        []string{"latency", "count", "contention"},
			},
		},
		"required": []string{},
	}
}

func (t *TransactionStatisticsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result TransactionStatisticsResult

	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	orderBy := "avg_latency"
	if o, ok := args["order_by"].(string); ok {
		switch o {
		case "count":
			orderBy = "exec_count"
		case "contention":
			orderBy = "contention_time"
		default:
			orderBy = "avg_latency"
		}
	}

	// Note: 'cnt' (total count) is in statistics, not execution_statistics (which has sampled counts)
	// Note: transaction_statistics does NOT have latencyInfo - only svcLat (service latency)
	// Note: maxRetries is a simple integer, not an object with mean/max
	query := fmt.Sprintf(`
		SELECT
			app_name,
			(statistics->'statistics'->>'cnt')::INT as exec_count,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as avg_latency,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as p99_latency,
			(statistics->'statistics'->'svcLat'->>'mean')::FLOAT as max_latency,
			COALESCE((statistics->'statistics'->>'maxRetries')::FLOAT, 0) as avg_retries,
			COALESCE((statistics->'statistics'->>'maxRetries')::INT, 0) as max_retries,
			COALESCE((statistics->'execution_statistics'->'contentionTime'->>'mean')::FLOAT, 0) as contention_time,
			COALESCE((statistics->'statistics'->'numRows'->>'mean')::FLOAT, 0) as statements_per_txn
		FROM crdb_internal.transaction_statistics
		WHERE (statistics->'statistics'->>'cnt')::INT > 0
		ORDER BY %s DESC NULLS LAST
		LIMIT %d
	`, orderBy, limit)

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query transaction statistics: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ts TransactionStats
		var p99, maxLat *float64

		if err := rows.Scan(
			&ts.AppName,
			&ts.ExecutionCount,
			&ts.AvgLatencySec,
			&p99,
			&maxLat,
			&ts.AvgRetries,
			&ts.MaxRetries,
			&ts.ContentionTime,
			&ts.StatementsPerTxn,
		); err != nil {
			return nil, fmt.Errorf("failed to scan transaction stats row: %w", err)
		}

		if p99 != nil {
			ts.P99LatencySec = *p99
		}
		if maxLat != nil {
			ts.MaxLatencySec = *maxLat
		}

		result.Transactions = append(result.Transactions, ts)
		result.TotalTxns += ts.ExecutionCount
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transaction stats rows: %w", err)
	}

	result.Note = fmt.Sprintf("Showing top %d transaction patterns ordered by %s", len(result.Transactions), orderBy)

	return result, nil
}
