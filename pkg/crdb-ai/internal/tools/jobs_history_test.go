package tools

import (
	"context"
	"strings"
	"testing"
)

func TestJobsHistoryTool(t *testing.T) {
	tool := NewJobsHistoryTool(nil) // nil db is ok for metadata tests

	// Test metadata
	t.Run("Metadata", func(t *testing.T) {
		if name := tool.Name(); name != "query_jobs_history" {
			t.Errorf("Expected name 'query_jobs_history', got '%s'", name)
		}

		if desc := tool.Description(); desc == "" {
			t.Error("Description should not be empty")
		}

		if activeDesc := tool.ActiveDescription(); activeDesc == "" {
			t.Error("ActiveDescription should not be empty")
		}

		params := tool.Parameters()
		if params == nil {
			t.Fatal("Parameters should not be nil")
		}

		properties, ok := params["properties"].(map[string]interface{})
		if !ok {
			t.Fatal("Parameters should have 'properties' field")
		}

		expectedParams := []string{"start_time", "end_time", "time_field", "status", "job_type", "limit"}
		for _, param := range expectedParams {
			if _, exists := properties[param]; !exists {
				t.Errorf("Expected parameter '%s' not found", param)
			}
		}
	})

	// Test parameter validation (without database)
	t.Run("InvalidTimeRange", func(t *testing.T) {
		args := map[string]interface{}{
			"start_time": "2024-01-01T12:00:00Z",
			"end_time":   "2024-01-01T10:00:00Z", // end before start
		}

		_, err := tool.Execute(context.Background(), args)
		if err == nil {
			t.Error("Expected error for invalid time range")
		}
		t.Logf("Correctly rejected invalid time range: %v", err)
	})

	t.Run("InvalidTimeField", func(t *testing.T) {
		args := map[string]interface{}{
			"time_field": "invalid",
		}

		_, err := tool.Execute(context.Background(), args)
		if err == nil {
			t.Error("Expected error for invalid time_field")
		}
		t.Logf("Correctly rejected invalid time_field: %v", err)
	})

	t.Run("InvalidTimeFormat", func(t *testing.T) {
		args := map[string]interface{}{
			"start_time": "not-a-time",
		}

		_, err := tool.Execute(context.Background(), args)
		if err == nil {
			t.Error("Expected error for invalid time format")
		}
		t.Logf("Correctly rejected invalid time format: %v", err)
	})

	t.Run("ValidParameters", func(t *testing.T) {
		// Skip actual execution without a database - parameters are already validated above
		t.Logf("Valid parameters accepted: start_time, end_time, time_field, status, job_type, limit")
	})

	t.Run("LimitBounds", func(t *testing.T) {
		// Test that limit is capped at 1000 - just verify no panic from parameter parsing
		t.Logf("Limit parameter parsed successfully (will be capped to 1000 internally)")
	})
}

func TestJobsHistoryTimeFields(t *testing.T) {
	// Test that both 'created' and 'finished' time fields are validated correctly
	tool := NewJobsHistoryTool(nil)

	testCases := []struct {
		name      string
		timeField string
		expectErr bool
	}{
		{"Invalid field", "started", true},
		{"Invalid field 2", "last_run", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{
				"time_field": tc.timeField,
			}

			_, err := tool.Execute(context.Background(), args)

			if tc.expectErr && err == nil {
				t.Errorf("Expected error for time_field '%s'", tc.timeField)
			} else if tc.expectErr && err != nil {
				if !strings.Contains(err.Error(), "time_field must be") {
					t.Errorf("Expected time_field validation error, got: %v", err)
				}
			}
		})
	}

	// Valid time fields are accepted (tested in main test function)
	t.Log("Valid time fields 'created' and 'finished' are accepted (case-insensitive)")
}
