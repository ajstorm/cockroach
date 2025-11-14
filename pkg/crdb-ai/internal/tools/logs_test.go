package tools

import (
	"context"
	"testing"
	"time"
)

func TestLogsTool(t *testing.T) {
	tool := NewLogsTool()

	// Test 1: Basic functionality - get last 5 log entries
	t.Run("GetRecentLogs", func(t *testing.T) {
		args := map[string]interface{}{
			"max": float64(5),
		}

		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		logsResult, ok := result.(LogsResult)
		if !ok {
			t.Fatalf("Expected LogsResult, got %T", result)
		}

		t.Logf("Found %d log entries", logsResult.Count)
		t.Logf("Time range: %s", logsResult.TimeRange)

		if logsResult.Count > 5 {
			t.Errorf("Expected at most 5 entries, got %d", logsResult.Count)
		}

		// Print first entry if available
		if len(logsResult.Entries) > 0 {
			entry := logsResult.Entries[0]
			t.Logf("Sample entry: severity=%s, message=%s", entry.Severity, entry.Message)
		}
	})

	// Test 2: Time range filtering
	t.Run("TimeRangeFilter", func(t *testing.T) {
		now := time.Now()
		oneHourAgo := now.Add(-1 * time.Hour)

		args := map[string]interface{}{
			"start_time": oneHourAgo.Format(time.RFC3339),
			"end_time":   now.Format(time.RFC3339),
			"max":        float64(10),
		}

		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute with time range failed: %v", err)
		}

		logsResult := result.(LogsResult)
		t.Logf("Found %d entries in last hour", logsResult.Count)
	})

	// Test 3: Level filtering
	t.Run("LevelFilter", func(t *testing.T) {
		args := map[string]interface{}{
			"level": "ERROR",
			"max":   float64(10),
		}

		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute with level filter failed: %v", err)
		}

		logsResult := result.(LogsResult)
		t.Logf("Found %d ERROR-level entries", logsResult.Count)

		// Verify all returned entries are ERROR or FATAL
		for _, entry := range logsResult.Entries {
			if entry.Severity != "ERROR" && entry.Severity != "FATAL" {
				t.Errorf("Expected ERROR or FATAL severity, got %s", entry.Severity)
			}
		}
	})

	// Test 4: Pattern matching
	t.Run("PatternFilter", func(t *testing.T) {
		args := map[string]interface{}{
			"pattern": "server",
			"max":     float64(5),
		}

		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute with pattern filter failed: %v", err)
		}

		logsResult := result.(LogsResult)
		t.Logf("Found %d entries matching 'server'", logsResult.Count)
	})

	// Test 5: Invalid time range
	t.Run("InvalidTimeRange", func(t *testing.T) {
		args := map[string]interface{}{
			"start_time": "2024-01-01T10:00:00Z",
			"end_time":   "2024-01-01T09:00:00Z", // end before start
		}

		_, err := tool.Execute(context.Background(), args)
		if err == nil {
			t.Error("Expected error for invalid time range, got nil")
		}
		t.Logf("Correctly rejected invalid time range: %v", err)
	})

	// Test 6: Invalid log level
	t.Run("InvalidLevel", func(t *testing.T) {
		args := map[string]interface{}{
			"level": "INVALID",
		}

		_, err := tool.Execute(context.Background(), args)
		if err == nil {
			t.Error("Expected error for invalid level, got nil")
		}
		t.Logf("Correctly rejected invalid level: %v", err)
	})
}

func TestLogsToolMetadata(t *testing.T) {
	tool := NewLogsTool()

	// Test name
	if name := tool.Name(); name != "get_logs" {
		t.Errorf("Expected name 'get_logs', got '%s'", name)
	}

	// Test description is not empty
	if desc := tool.Description(); desc == "" {
		t.Error("Description should not be empty")
	}

	// Test active description is not empty
	if activeDesc := tool.ActiveDescription(); activeDesc == "" {
		t.Error("ActiveDescription should not be empty")
	}

	// Test parameters structure
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters should not be nil")
	}

	properties, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Parameters should have 'properties' field")
	}

	// Check expected parameters exist
	expectedParams := []string{"start_time", "end_time", "level", "pattern", "max"}
	for _, param := range expectedParams {
		if _, exists := properties[param]; !exists {
			t.Errorf("Expected parameter '%s' not found", param)
		}
	}
}
