package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/log/logpb"
)

// LogsTool retrieves time-bounded log entries from CockroachDB nodes
type LogsTool struct{}

// NewLogsTool creates a new logs tool
func NewLogsTool() *LogsTool {
	return &LogsTool{}
}

// LogEntry represents a single log entry
type LogEntry struct {
	Time     int64  `json:"time"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// LogsResult contains log entries
type LogsResult struct {
	Entries   []LogEntry `json:"entries"`
	Count     int        `json:"count"`
	TimeRange string     `json:"time_range"`
	Note      string     `json:"note,omitempty"`
}

func (t *LogsTool) Name() string {
	return "get_logs"
}

func (t *LogsTool) Description() string {
	return `Retrieve time-bounded log entries from the local CockroachDB node.

This tool fetches actual log file entries from the current node, filtered by:
- Time range (start and end times)
- Log level/severity (INFO, WARNING, ERROR, FATAL)
- Text pattern (regex)

Use this when:
- Investigating errors or issues during a specific time period
- Debugging problems by examining log messages
- Correlating events with specific timestamps
- Searching for specific error messages or patterns

Example time formats:
- RFC3339: "2024-01-15T10:30:00Z"
- Unix timestamp (nanoseconds): "1705315800000000000"

Note: Only retrieves logs from the local node where the AI service is running.`
}

func (t *LogsTool) ActiveDescription() string {
	return "I'm retrieving log entries from the local node"
}

func (t *LogsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start time in RFC3339 format or Unix nanoseconds (default: 24 hours ago). Example: '2024-01-15T10:00:00Z'",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End time in RFC3339 format or Unix nanoseconds (default: now). Example: '2024-01-15T12:00:00Z'",
			},
			"level": map[string]interface{}{
				"type":        "string",
				"description": "Minimum log level to include (INFO, WARNING, ERROR, FATAL). Default: INFO",
			},
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Regex pattern to filter log messages. Optional.",
			},
			"max": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of log entries to return (default: 1000)",
			},
		},
		"required": []string{},
	}
}

func (t *LogsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result LogsResult

	// Parse time range using the shared parser (supports relative times like "7d ago")
	now := time.Now()
	startTime, err := ParseTimeArgument(args["start_time"], now.Add(-24*time.Hour))
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

	level := "INFO"
	if l, ok := args["level"].(string); ok && l != "" {
		level = strings.ToUpper(l)
	}

	pattern := ""
	if p, ok := args["pattern"].(string); ok {
		pattern = p
	}

	maxEntries := 1000
	if m, ok := args["max"].(float64); ok {
		maxEntries = int(m)
	}

	if maxEntries < 1 {
		return nil, fmt.Errorf("max must be greater than 0")
	}

	// Build regex pattern that filters by level and optional user pattern
	// Log levels: I=INFO, W=WARNING, E=ERROR, F=FATAL
	var levelPattern string
	switch level {
	case "FATAL":
		levelPattern = "^F"
	case "ERROR":
		levelPattern = "^[EF]"
	case "WARNING":
		levelPattern = "^[WEF]"
	case "INFO":
		levelPattern = "." // All levels
	default:
		return nil, fmt.Errorf("invalid level: %s (must be INFO, WARNING, ERROR, or FATAL)", level)
	}

	var regex *regexp.Regexp
	if pattern != "" {
		// Combine level filter with user pattern
		combinedPattern := fmt.Sprintf("(%s).*(%s)", levelPattern, pattern)
		regex, err = regexp.Compile(combinedPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}
	} else if levelPattern != "." {
		regex, err = regexp.Compile(levelPattern)
		if err != nil {
			return nil, fmt.Errorf("failed to compile level pattern: %w", err)
		}
	}

	// Flush log files to ensure latest entries are available
	log.FlushFiles()

	// Fetch entries from log files using internal function
	// Use WithMarkedSensitiveData mode to preserve redaction markers
	entries, err := log.FetchEntriesFromFiles(
		startTime.UnixNano(),
		endTime.UnixNano(),
		maxEntries,
		regex,
		log.WithMarkedSensitiveData,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch log entries: %w", err)
	}

	// Convert to our LogEntry format
	for _, entry := range entries {
		result.Entries = append(result.Entries, LogEntry{
			Time:     entry.Time,
			Severity: severityName(entry.Severity),
			Message:  entry.Message,
			File:     entry.File,
			Line:     int(entry.Line),
		})
	}

	result.Count = len(result.Entries)
	result.TimeRange = fmt.Sprintf("%s to %s",
		startTime.Format(time.RFC3339),
		endTime.Format(time.RFC3339))

	if result.Count == 0 {
		result.Note = "No log entries found matching the specified criteria"
	} else if result.Count >= maxEntries {
		result.Note = fmt.Sprintf("Returned maximum of %d entries. There may be more logs available. Consider narrowing your time range or using a more specific pattern.", maxEntries)
	}

	return result, nil
}

// severityName converts severity to name
func severityName(severity logpb.Severity) string {
	switch severity {
	case logpb.Severity_INFO:
		return "INFO"
	case logpb.Severity_WARNING:
		return "WARNING"
	case logpb.Severity_ERROR:
		return "ERROR"
	case logpb.Severity_FATAL:
		return "FATAL"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", severity)
	}
}

