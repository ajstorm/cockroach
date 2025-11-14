package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClusterEventsLogTool queries the system.eventlog table for cluster events
type ClusterEventsLogTool struct {
	db *pgxpool.Pool
}

// NewClusterEventsLogTool creates a new cluster events log tool
func NewClusterEventsLogTool(db *pgxpool.Pool) *ClusterEventsLogTool {
	return &ClusterEventsLogTool{db: db}
}

func (t *ClusterEventsLogTool) Name() string {
	return "query_cluster_events"
}

func (t *ClusterEventsLogTool) Description() string {
	return `Query the cluster event log (system.eventlog) to investigate node restarts, upgrades, setting changes, and other cluster events.

This is essential for troubleshooting:
- Unexpected latency spikes or performance issues
- Connection drops or cluster instability
- Investigating what changed during a specific time window
- Correlating events with observed problems

Common event types to filter:
- node_restart, node_join, node_decommissioned - Node lifecycle events
- set_cluster_setting - Settings changes that might affect behavior
- create_database, drop_database - Schema changes
- upgrade - Version upgrades

Use relative time ranges like "1h ago" or absolute timestamps.
Results include timestamp, event type, node ID, and detailed event information.`
}

func (t *ClusterEventsLogTool) ActiveDescription() string {
	return "I'm querying the cluster event log for you"
}

func (t *ClusterEventsLogTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start time for events. Can be relative (e.g., '1h ago', '30m ago') or absolute RFC3339 timestamp. Defaults to 1 hour ago.",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End time for events. Can be relative (e.g., 'now') or absolute RFC3339 timestamp. Defaults to now.",
			},
			"event_types": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Optional list of event types to filter (e.g., ['node_restart', 'set_cluster_setting', 'upgrade']). If omitted, returns all events. Use patterns like 'node_%' to match multiple types.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of events to return. Defaults to 100.",
			},
		},
		"required": []string{},
	}
}

// ClusterEvent represents a single cluster event
type ClusterEvent struct {
	Timestamp   string `json:"timestamp"`
	EventType   string `json:"event_type"`
	NodeID      string `json:"node_id,omitempty"`
	Info        string `json:"info"`
	Description string `json:"description,omitempty"`
}

// ClusterEventsResult contains the query results
type ClusterEventsResult struct {
	Events    []ClusterEvent `json:"events"`
	TimeRange string         `json:"time_range"`
	Count     int            `json:"count"`
	Summary   string         `json:"summary"`
}

func (t *ClusterEventsLogTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse time range
	now := time.Now()
	startTime, err := ParseTimeArgument(args["start_time"], now.Add(-1*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("invalid start_time: %w", err)
	}

	endTime, err := ParseTimeArgument(args["end_time"], now)
	if err != nil {
		return nil, fmt.Errorf("invalid end_time: %w", err)
	}

	if startTime.After(endTime) {
		return nil, fmt.Errorf("start_time must be before end_time")
	}

	// Parse optional event type filters
	var eventTypeFilters []string
	if eventTypesRaw, ok := args["event_types"].([]interface{}); ok {
		for _, et := range eventTypesRaw {
			if str, ok := et.(string); ok {
				eventTypeFilters = append(eventTypeFilters, str)
			}
		}
	}

	// Parse limit
	limit := 100
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Build query
	query := `
		SELECT
			timestamp,
			"eventType",
			"reportingID" AS node_id,
			info
		FROM system.eventlog
		WHERE timestamp >= $1 AND timestamp <= $2
	`

	queryArgs := []interface{}{startTime, endTime}
	argNum := 3

	// Add event type filter if provided
	if len(eventTypeFilters) > 0 {
		query += " AND ("
		for i, eventType := range eventTypeFilters {
			if i > 0 {
				query += " OR "
			}
			// Support wildcards using LIKE
			if strings.Contains(eventType, "%") || strings.Contains(eventType, "_") {
				query += fmt.Sprintf(`"eventType" LIKE $%d`, argNum)
			} else {
				query += fmt.Sprintf(`"eventType" = $%d`, argNum)
			}
			queryArgs = append(queryArgs, eventType)
			argNum++
		}
		query += ")"
	}

	query += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT %d", limit)

	// Execute query
	rows, err := t.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query event log: %w", err)
	}
	defer rows.Close()

	var events []ClusterEvent
	eventTypeCounts := make(map[string]int)

	for rows.Next() {
		var event ClusterEvent
		var timestamp time.Time
		var nodeID *int32

		err := rows.Scan(&timestamp, &event.EventType, &nodeID, &event.Info)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}

		event.Timestamp = timestamp.Format(time.RFC3339)
		if nodeID != nil {
			event.NodeID = fmt.Sprintf("%d", *nodeID)
		}

		// Add human-readable description based on event type
		event.Description = getEventDescription(event.EventType)

		events = append(events, event)
		eventTypeCounts[event.EventType]++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}

	// Build summary
	summary := buildEventsSummary(eventTypeCounts, len(events))

	result := ClusterEventsResult{
		Events:    events,
		TimeRange: fmt.Sprintf("%s to %s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339)),
		Count:     len(events),
		Summary:   summary,
	}

	return result, nil
}

// getEventDescription provides human-readable descriptions for common event types
func getEventDescription(eventType string) string {
	descriptions := map[string]string{
		"node_restart":         "Node restarted",
		"node_join":            "Node joined the cluster",
		"node_decommissioned":  "Node was decommissioned",
		"set_cluster_setting":  "Cluster setting was changed",
		"create_database":      "Database was created",
		"drop_database":        "Database was dropped",
		"create_table":         "Table was created",
		"drop_table":           "Table was dropped",
		"alter_table":          "Table schema was altered",
		"upgrade":              "Cluster version was upgraded",
		"create_index":         "Index was created",
		"drop_index":           "Index was dropped",
		"create_user":          "User was created",
		"drop_user":            "User was dropped",
		"set_zone_config":      "Zone configuration was changed",
		"remove_zone_config":   "Zone configuration was removed",
	}

	if desc, ok := descriptions[eventType]; ok {
		return desc
	}
	return ""
}

// buildEventsSummary creates a summary of event types and counts
func buildEventsSummary(eventTypeCounts map[string]int, total int) string {
	if total == 0 {
		return "No events found in the specified time range"
	}

	summary := fmt.Sprintf("Found %d events. Breakdown: ", total)
	first := true
	for eventType, count := range eventTypeCounts {
		if !first {
			summary += ", "
		}
		summary += fmt.Sprintf("%s (%d)", eventType, count)
		first = false
	}

	return summary
}
