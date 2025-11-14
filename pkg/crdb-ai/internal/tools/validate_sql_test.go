package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSQLTool(t *testing.T) {
	tool := NewValidateSQLTool()

	tests := []struct {
		name        string
		sql         string
		expectValid bool
		expectError bool
	}{
		{
			name:        "valid SELECT",
			sql:         "SELECT * FROM users WHERE id = 1",
			expectValid: true,
			expectError: false,
		},
		{
			name:        "invalid ALTER PRIMARY KEY - missing USING COLUMNS",
			sql:         "ALTER TABLE tpcc.order_line ALTER PRIMARY KEY USING HASH (ol_o_id) WITH BUCKET_COUNT = 16",
			expectValid: false,
			expectError: false, // Tool returns error in result, not as Go error
		},
		{
			name:        "valid ALTER PRIMARY KEY with USING COLUMNS",
			sql:         "ALTER TABLE tpcc.order_line ALTER PRIMARY KEY USING COLUMNS (ol_o_id) USING HASH WITH (bucket_count=16)",
			expectValid: true,
			expectError: false,
		},
		{
			name:        "multiple valid statements",
			sql:         "CREATE INDEX idx ON users(email); CREATE INDEX idx2 ON users(name)",
			expectValid: true,
			expectError: false,
		},
		{
			name:        "invalid keyword as table name",
			sql:         "SELECT * FROM WHERE",
			expectValid: false,
			expectError: false,
		},
		{
			name:        "valid hash-sharded index",
			sql:         "CREATE INDEX idx ON users(email) USING HASH WITH (bucket_count=8)",
			expectValid: true,
			expectError: false,
		},
		{
			name:        "valid CREATE TABLE with hash-sharded PK",
			sql:         "CREATE TABLE t (id INT PRIMARY KEY USING HASH WITH (bucket_count=10), name STRING)",
			expectValid: true,
			expectError: false,
		},
		{
			name:        "invalid - garbage input",
			sql:         "THIS IS NOT SQL AT ALL",
			expectValid: false,
			expectError: false,
		},
		{
			name:        "empty SQL",
			sql:         "",
			expectValid: false,
			expectError: true, // Should error on empty input
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), map[string]interface{}{
				"sql": tt.sql,
			})

			if tt.expectError {
				require.Error(t, err, "Expected error for test case: %s", tt.name)
				return
			}

			require.NoError(t, err, "Unexpected error for test case: %s", tt.name)
			require.NotNil(t, result, "Result should not be nil")

			// Convert result to ValidationResult
			resultMap, ok := result.(ValidationResult)
			require.True(t, ok, "Result should be ValidationResult type")

			if tt.expectValid {
				require.True(t, resultMap.Valid, "SQL should be valid: %s\nErrors: %v", tt.sql, resultMap.Errors)
				require.Empty(t, resultMap.Errors, "Should have no errors for valid SQL")
				require.NotEmpty(t, resultMap.Statements, "Should have parsed statements")

				// Print the parsed statements for debugging
				t.Logf("Parsed statements: %v", resultMap.Statements)
			} else {
				require.False(t, resultMap.Valid, "SQL should be invalid: %s", tt.sql)
				require.NotEmpty(t, resultMap.Errors, "Should have error messages for invalid SQL")

				// Print the error for debugging - this is what the LLM will see
				t.Logf("Validation errors (LLM will see this): %v", resultMap.Errors)
			}
		})
	}
}

func TestValidateSQLTool_ParameterValidation(t *testing.T) {
	tool := NewValidateSQLTool()

	// Test missing sql parameter
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "sql parameter is required")
	require.Nil(t, result)

	// Test wrong type for sql parameter
	result, err = tool.Execute(context.Background(), map[string]interface{}{
		"sql": 123,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "sql parameter is required")
	require.Nil(t, result)
}

func TestValidateSQLTool_RealWorldExample(t *testing.T) {
	tool := NewValidateSQLTool()

	// This is the exact SQL that was incorrectly suggested to the user
	incorrectSQL := "ALTER TABLE tpcc.order_line\n  ALTER PRIMARY KEY USING HASH (ol_o_id) WITH BUCKET_COUNT = 16;"

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"sql": incorrectSQL,
	})

	require.NoError(t, err)
	resultMap := result.(ValidationResult)

	// This should be invalid
	require.False(t, resultMap.Valid, "The incorrect SQL should be caught as invalid")
	require.NotEmpty(t, resultMap.Errors, "Should provide error messages")

	// Print what the LLM would see
	t.Logf("LLM would receive this error: %v", resultMap.Errors)

	// Now test the corrected version
	correctSQL := "ALTER TABLE tpcc.order_line ALTER PRIMARY KEY USING COLUMNS (ol_o_id) USING HASH WITH (bucket_count=16)"

	result2, err := tool.Execute(context.Background(), map[string]interface{}{
		"sql": correctSQL,
	})

	require.NoError(t, err)
	resultMap2 := result2.(ValidationResult)

	// This should be valid
	require.True(t, resultMap2.Valid, "The corrected SQL should be valid. Errors: %v", resultMap2.Errors)
	require.Empty(t, resultMap2.Errors, "Should have no errors")

	// Print the parsed statement
	jsonBytes, _ := json.MarshalIndent(resultMap2, "", "  ")
	t.Logf("Valid SQL result:\n%s", string(jsonBytes))
}
