package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Tool defines the interface that all tools must implement
type Tool interface {
	// Name returns the tool's unique identifier
	Name() string

	// Description returns a human-readable description for the LLM
	Description() string

	// ActiveDescription returns a conversational description shown to users while the tool is executing
	// This should describe what the AI is actively doing (e.g., "I'm checking your cluster's replication health...")
	ActiveDescription() string

	// Parameters returns the JSON schema for tool parameters (OpenAI format)
	Parameters() map[string]interface{}

	// Execute runs the tool with the given arguments
	Execute(ctx context.Context, args map[string]interface{}) (interface{}, error)
}

// ToolResult represents the result of executing a tool
type ToolResult struct {
	ToolName string      `json:"tool_name"`
	Success  bool        `json:"success"`
	Data     interface{} `json:"data,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// ParseTimeArgument parses a time argument which can be:
// - "now"
// - Relative times: "Xd ago", "Xh ago", "Xm ago", "Xs ago", "Xw ago" (days, hours, minutes, seconds, weeks)
// - RFC3339 timestamp (absolute)
// - Unix nanoseconds as a string
//
// This is the canonical time parsing function that all tools should use.
func ParseTimeArgument(arg interface{}, defaultTime time.Time) (time.Time, error) {
	if arg == nil {
		return defaultTime, nil
	}

	str, ok := arg.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("time must be a string")
	}

	str = strings.TrimSpace(str)

	// Handle empty string
	if str == "" {
		return defaultTime, nil
	}

	// Handle "now"
	if strings.ToLower(str) == "now" {
		return time.Now(), nil
	}

	// Handle relative times like "1h ago", "30m ago", "7d ago", "2w ago"
	if strings.HasSuffix(strings.ToLower(str), " ago") {
		durationStr := strings.TrimSpace(str[:len(str)-4])
		duration, err := ParseExtendedDuration(durationStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration format '%s': %w", durationStr, err)
		}
		return time.Now().Add(-duration), nil
	}

	// Try parsing as RFC3339
	if t, err := time.Parse(time.RFC3339, str); err == nil {
		return t, nil
	}

	// Try parsing as Unix nanoseconds
	if nanos, err := strconv.ParseInt(str, 10, 64); err == nil {
		return time.Unix(0, nanos), nil
	}

	return time.Time{}, fmt.Errorf("time must be 'now', relative (e.g., '1h ago', '7d ago'), RFC3339 format, or Unix nanoseconds")
}

// ParseExtendedDuration parses a duration string with extended support for days and weeks.
// Supports: d (days), w (weeks), h (hours), m (minutes), s (seconds), ms, us, ns
//
// Examples: "7d", "2w", "24h", "30m", "1h30m", "7d12h"
func ParseExtendedDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	var total time.Duration
	remaining := s

	for len(remaining) > 0 {
		// Find the next number
		numEnd := 0
		for numEnd < len(remaining) && (remaining[numEnd] >= '0' && remaining[numEnd] <= '9') {
			numEnd++
		}

		if numEnd == 0 {
			return 0, fmt.Errorf("invalid duration format: expected number at '%s'", remaining)
		}

		numStr := remaining[:numEnd]
		remaining = remaining[numEnd:]

		num, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number '%s': %w", numStr, err)
		}

		// Find the unit
		unitEnd := 0
		for unitEnd < len(remaining) && (remaining[unitEnd] < '0' || remaining[unitEnd] > '9') {
			unitEnd++
		}

		if unitEnd == 0 {
			return 0, fmt.Errorf("missing unit after '%s'", numStr)
		}

		unit := remaining[:unitEnd]
		remaining = remaining[unitEnd:]

		var duration time.Duration
		switch unit {
		case "w":
			duration = time.Duration(num) * 7 * 24 * time.Hour
		case "d":
			duration = time.Duration(num) * 24 * time.Hour
		case "h":
			duration = time.Duration(num) * time.Hour
		case "m":
			duration = time.Duration(num) * time.Minute
		case "s":
			duration = time.Duration(num) * time.Second
		case "ms":
			duration = time.Duration(num) * time.Millisecond
		case "us", "µs":
			duration = time.Duration(num) * time.Microsecond
		case "ns":
			duration = time.Duration(num) * time.Nanosecond
		default:
			return 0, fmt.Errorf("unknown unit '%s' (supported: w, d, h, m, s, ms, us, ns)", unit)
		}

		total += duration
	}

	return total, nil
}
