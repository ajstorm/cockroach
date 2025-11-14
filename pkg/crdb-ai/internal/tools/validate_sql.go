package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
)

// ValidateSQLTool validates SQL syntax without executing
type ValidateSQLTool struct{}

// NewValidateSQLTool creates a new SQL validation tool
func NewValidateSQLTool() *ValidateSQLTool {
	return &ValidateSQLTool{}
}

// ValidationResult contains the result of SQL validation
type ValidationResult struct {
	Valid      bool     `json:"valid"`
	Statements []string `json:"statements,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

func (t *ValidateSQLTool) Name() string {
	return "validate_sql"
}

func (t *ValidateSQLTool) Description() string {
	return `Validate SQL syntax without executing the statements.

This tool uses CockroachDB's SQL parser to verify that SQL statements are syntactically correct.
It does NOT execute the SQL - it only checks if the syntax is valid.

Use this tool to:
- Verify SQL recommendations before presenting them to users
- Check complex SQL syntax (ALTER TABLE, CREATE INDEX, etc.)
- Validate multi-statement SQL scripts
- Ensure hash-sharded index/primary key syntax is correct

IMPORTANT: You should call this tool to validate ANY SQL you plan to recommend to the user.
This prevents suggesting syntactically invalid SQL.

Input: SQL statement(s) as a string (can be multiple statements separated by semicolons)
Output: Whether the SQL is valid and any parse errors if invalid`
}

func (t *ValidateSQLTool) ActiveDescription() string {
	return "I'm validating the SQL syntax"
}

func (t *ValidateSQLTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sql": map[string]interface{}{
				"type":        "string",
				"description": "The SQL statement(s) to validate. Can include multiple statements separated by semicolons.",
			},
		},
		"required": []string{"sql"},
	}
}

func (t *ValidateSQLTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	sql, ok := args["sql"].(string)
	if !ok || sql == "" {
		return nil, fmt.Errorf("sql parameter is required and must be a non-empty string")
	}

	result := ValidationResult{
		Valid:      true,
		Statements: []string{},
		Errors:     []string{},
	}

	// Parse the SQL using CockroachDB's parser
	stmts, err := parser.Parse(sql)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("Parse error: %v", err))
		return result, nil
	}

	// Successfully parsed - collect the parsed statements
	for _, stmt := range stmts {
		// Format the statement back to SQL to show what was parsed
		formatted := tree.AsStringWithFlags(stmt.AST, tree.FmtSimple)
		result.Statements = append(result.Statements, formatted)
	}

	// Check if any statements were parsed
	if len(stmts) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "No valid SQL statements found")
	}

	return result, nil
}

// Helper function to extract SQL from markdown code blocks
func extractSQLFromMarkdown(text string) string {
	// Look for ```sql ... ``` blocks
	lines := strings.Split(text, "\n")
	var sqlLines []string
	inCodeBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```sql") {
			inCodeBlock = true
			continue
		}

		if inCodeBlock && strings.HasPrefix(trimmed, "```") {
			inCodeBlock = false
			continue
		}

		if inCodeBlock {
			sqlLines = append(sqlLines, line)
		}
	}

	if len(sqlLines) > 0 {
		return strings.Join(sqlLines, "\n")
	}

	// If no code block found, return the original text
	return text
}
