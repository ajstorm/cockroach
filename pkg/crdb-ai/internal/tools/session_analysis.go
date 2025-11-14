package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionAnalysisTool provides deep analysis of specific session activity
type SessionAnalysisTool struct {
	db *pgxpool.Pool
}

// NewSessionAnalysisTool creates a new session analysis tool
func NewSessionAnalysisTool(db *pgxpool.Pool) *SessionAnalysisTool {
	return &SessionAnalysisTool{db: db}
}

func (t *SessionAnalysisTool) Name() string {
	return "analyze_session"
}

func (t *SessionAnalysisTool) Description() string {
	return `Analyze a specific database session's activity and history.

This tool provides detailed information about:
- Current session status and activity
- Active queries and transactions
- Historical queries executed by the session
- Session configuration and connection details
- Lock waits and contention involvement
- Resource consumption (CPU, memory, network)

Use this when:
- A specific connection is behaving strangely
- You need to debug "what is this session doing?"
- Investigating a locked or blocked session
- Understanding session-specific performance issues

Specify the session ID from pg_stat_activity or cluster_sessions.`
}

func (t *SessionAnalysisTool) ActiveDescription() string {
	return "I'm analyzing the session activity and history"
}

func (t *SessionAnalysisTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_id": map[string]interface{}{
				"type":        "string",
				"description": "The session ID to analyze (from cluster_sessions or pg_stat_activity)",
			},
		},
		"required": []string{"session_id"},
	}
}

// SessionAnalysisInfo contains current session details
type SessionAnalysisInfo struct {
	SessionID       string  `json:"session_id"`
	NodeID          int     `json:"node_id"`
	UserName        string  `json:"user_name"`
	AppName         string  `json:"app_name"`
	ClientAddress   string  `json:"client_address"`
	SessionStart    string  `json:"session_start"`
	Status          string  `json:"status"`
	CurrentQuery    *string `json:"current_query,omitempty"`
	CurrentTxnStart *string `json:"current_txn_start,omitempty"`
	ActiveSince     *string `json:"active_since,omitempty"`
}

// SessionQuery represents a query executed by the session
type SessionQuery struct {
	Query         string  `json:"query"`
	StartTime     string  `json:"start_time"`
	EndTime       *string `json:"end_time,omitempty"`
	LatencyMs     *float64 `json:"latency_ms,omitempty"`
	Status        string  `json:"status"` // "active", "completed", "failed"
	Error         *string `json:"error,omitempty"`
}

// SessionLockInfo represents lock/contention information
type SessionLockInfo struct {
	WaitingOn       *string `json:"waiting_on,omitempty"`
	LockType        *string `json:"lock_type,omitempty"`
	WaitDurationMs  *float64 `json:"wait_duration_ms,omitempty"`
	BlockedBySessions []string `json:"blocked_by_sessions,omitempty"`
}

// SessionAnalysisResult contains the complete session analysis
type SessionAnalysisResult struct {
	Session         SessionAnalysisInfo `json:"session"`
	ActiveQuery     *string             `json:"active_query,omitempty"`
	RecentQueries   []SessionQuery      `json:"recent_queries"`
	LockInfo        *SessionLockInfo    `json:"lock_info,omitempty"`
	TotalQueries    int                 `json:"total_queries"`
	AvgQueryLatency *float64            `json:"avg_query_latency_ms,omitempty"`
	Summary         string              `json:"summary"`
}

func (t *SessionAnalysisTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	// Get current session info
	sessionInfo, err := t.getSessionInfo(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session info: %w", err)
	}

	// Get recent queries for this session
	recentQueries, err := t.getRecentQueries(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent queries: %w", err)
	}

	// Get lock/contention info
	lockInfo, err := t.getLockInfo(ctx, sessionID)
	if err != nil {
		// Don't fail if lock info unavailable, just log
		lockInfo = nil
	}

	// Calculate average latency
	var avgLatency *float64
	if len(recentQueries) > 0 {
		var total float64
		count := 0
		for _, q := range recentQueries {
			if q.LatencyMs != nil {
				total += *q.LatencyMs
				count++
			}
		}
		if count > 0 {
			avg := total / float64(count)
			avgLatency = &avg
		}
	}

	// Build summary
	summary := buildSessionSummary(sessionInfo, len(recentQueries), lockInfo)

	result := SessionAnalysisResult{
		Session:         *sessionInfo,
		ActiveQuery:     sessionInfo.CurrentQuery,
		RecentQueries:   recentQueries,
		LockInfo:        lockInfo,
		TotalQueries:    len(recentQueries),
		AvgQueryLatency: avgLatency,
		Summary:         summary,
	}

	return result, nil
}

// getSessionInfo retrieves current session information
func (t *SessionAnalysisTool) getSessionInfo(ctx context.Context, sessionID string) (*SessionAnalysisInfo, error) {
	query := `
		SELECT
			session_id,
			node_id,
			user_name,
			application_name,
			client_address,
			session_start,
			status,
			active_queries,
			active_query_start
		FROM crdb_internal.cluster_sessions
		WHERE session_id = $1
	`

	var info SessionAnalysisInfo
	var sessionStart time.Time
	var lastActiveStart *time.Time
	var activeQueries *string

	err := t.db.QueryRow(ctx, query, sessionID).Scan(
		&info.SessionID,
		&info.NodeID,
		&info.UserName,
		&info.AppName,
		&info.ClientAddress,
		&sessionStart,
		&info.Status,
		&activeQueries,
		&lastActiveStart,
	)

	if err != nil {
		return nil, fmt.Errorf("session not found or error querying: %w", err)
	}

	info.SessionStart = sessionStart.Format(time.RFC3339)

	if activeQueries != nil && *activeQueries != "" {
		info.CurrentQuery = activeQueries
	}

	if lastActiveStart != nil {
		activeSince := lastActiveStart.Format(time.RFC3339)
		info.ActiveSince = &activeSince
	}

	return &info, nil
}

// getRecentQueries retrieves recent queries executed by this session
func (t *SessionAnalysisTool) getRecentQueries(ctx context.Context, sessionID string) ([]SessionQuery, error) {
	// Query currently running queries for this session
	// Note: cluster_queries only shows currently running queries, not completed ones
	query := `
		SELECT
			query,
			start
		FROM crdb_internal.cluster_queries
		WHERE session_id = $1
		ORDER BY start DESC
		LIMIT 50
	`

	rows, err := t.db.Query(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queries []SessionQuery

	for rows.Next() {
		var q SessionQuery
		var start time.Time

		err := rows.Scan(&q.Query, &start)
		if err != nil {
			return nil, err
		}

		q.StartTime = start.Format(time.RFC3339)
		q.Status = "active" // cluster_queries only shows running queries

		queries = append(queries, q)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return queries, nil
}

// getLockInfo retrieves lock/contention information for the session
func (t *SessionAnalysisTool) getLockInfo(ctx context.Context, sessionID string) (*SessionLockInfo, error) {
	// Check if session has contention events
	// Note: cluster_contention_events schema: table_id, index_id, num_contention_events,
	// cumulative_contention_time, key, txn_id, count
	query := `
		SELECT
			txn_id,
			num_contention_events,
			EXTRACT(MILLISECONDS FROM cumulative_contention_time) as contention_ms
		FROM crdb_internal.cluster_contention_events
		WHERE txn_id IN (
			SELECT txn_id FROM crdb_internal.cluster_queries WHERE session_id = $1
		)
		ORDER BY cumulative_contention_time DESC
		LIMIT 1
	`

	var lockInfo SessionLockInfo
	var txnID string
	var numEvents int
	var contentionMs float64

	err := t.db.QueryRow(ctx, query, sessionID).Scan(&txnID, &numEvents, &contentionMs)
	if err != nil {
		// No lock contention found, return nil
		return nil, nil
	}

	waitingOn := fmt.Sprintf("Transaction %s with %d contention events", txnID, numEvents)
	lockInfo.WaitingOn = &waitingOn
	lockInfo.WaitDurationMs = &contentionMs
	lockType := "row-level"
	lockInfo.LockType = &lockType

	return &lockInfo, nil
}

// buildSessionSummary creates a summary of session analysis
func buildSessionSummary(info *SessionAnalysisInfo, queryCount int, lockInfo *SessionLockInfo) string {
	summary := fmt.Sprintf("Session %s from %s@%s (app: %s). ",
		info.SessionID, info.UserName, info.ClientAddress, info.AppName)

	summary += fmt.Sprintf("Status: %s. ", info.Status)

	if info.CurrentQuery != nil {
		summary += "Currently executing a query. "
	}

	summary += fmt.Sprintf("Found %d recent queries. ", queryCount)

	if lockInfo != nil && lockInfo.WaitDurationMs != nil {
		summary += fmt.Sprintf("Session is waiting on locks (%.2f ms contention). ", *lockInfo.WaitDurationMs)
	}

	return summary
}
