package tools

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListMetricsTool discovers all available metrics in the cluster
type ListMetricsTool struct {
	db       *pgxpool.Pool
	insecure bool
}

// NewListMetricsTool creates a new list metrics tool
func NewListMetricsTool(db *pgxpool.Pool, insecure bool) *ListMetricsTool {
	return &ListMetricsTool{
		db:       db,
		insecure: insecure,
	}
}

// MetricInfo contains information about a single metric
// TODO(ajstorm): Remove the Description field - it's no longer populated and just adds noise
type MetricInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ListMetricsResult contains the list of available metrics
type ListMetricsResult struct {
	Metrics      []MetricInfo `json:"metrics"`
	TotalCount   int          `json:"total_count"`
	CategoryInfo string       `json:"category_info"`
	Note         string       `json:"note"`
}

func (t *ListMetricsTool) Name() string {
	return "list_available_metrics"
}

func (t *ListMetricsTool) Description() string {
	return `List all available time series metrics in the cluster. This helps you discover which metrics are available for querying.

Use this tool to:
- Discover available metrics before querying historical data
- Find metrics related to specific subsystems (SQL, storage, replication, etc.)
- Understand what metrics can be monitored

You can optionally filter metrics by:
- Category/prefix (e.g., 'sql', 'storage', 'replication', 'sys')
- Search term (substring match in metric name or description)

After discovering metrics with this tool, use query_timeseries_metrics to retrieve historical data for those metrics.`
}

func (t *ListMetricsTool) ActiveDescription() string {
	return "I'm discovering available metrics in your cluster"
}

func (t *ListMetricsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filter": map[string]interface{}{
				"type":        "string",
				"description": "Optional filter to search for metrics. Can be a category (sql, storage, sys, replication) or any search term to match against metric names and descriptions.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of metrics to return. Defaults to 100. Use higher values if you need a comprehensive list.",
			},
		},
		"required": []string{},
	}
}

func (t *ListMetricsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse filter
	filter := ""
	if f, ok := args["filter"].(string); ok {
		filter = strings.ToLower(f)
	}

	// Parse limit
	limit := 100
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Fetch metrics from node_metrics and kv_store_status tables
	metrics, err := t.fetchMetricsFromSystemTables(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Sort by name
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Name < metrics[j].Name
	})

	// Apply limit
	if len(metrics) > limit {
		metrics = metrics[:limit]
	}

	// Build category info
	categoryInfo := t.buildCategoryInfo(metrics)

	result := ListMetricsResult{
		Metrics:      metrics,
		TotalCount:   len(metrics),
		CategoryInfo: categoryInfo,
		Note:         "Metrics are shown with 'cr.node.' or 'cr.store.' prefix, ready to use directly with query_timeseries_metrics tool.",
	}

	return result, nil
}

// fetchMetricsFromSystemTables queries node_metrics and kv_store_status to get all available metrics
func (t *ListMetricsTool) fetchMetricsFromSystemTables(ctx context.Context, filter string) ([]MetricInfo, error) {
	var allMetrics []MetricInfo

	// Build WHERE clause for filtering
	whereClause := ""
	if filter != "" {
		whereClause = fmt.Sprintf(" WHERE name LIKE '%%%s%%'", filter)
	}

	// Fetch node-level metrics from crdb_internal.node_metrics
	nodeQuery := fmt.Sprintf("SELECT DISTINCT name FROM crdb_internal.node_metrics%s ORDER BY name", whereClause)
	nodeRows, err := t.db.Query(ctx, nodeQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query node_metrics: %w", err)
	}
	defer nodeRows.Close()

	for nodeRows.Next() {
		var metricName string
		if err := nodeRows.Scan(&metricName); err != nil {
			return nil, fmt.Errorf("failed to scan node metric: %w", err)
		}

		// Add cr.node. prefix for timeseries queries
		fullName := "cr.node." + metricName

		allMetrics = append(allMetrics, MetricInfo{
			Name:        fullName,
			Type:        "node",
			Description: "",
		})
	}

	if err := nodeRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node metrics: %w", err)
	}

	// Fetch store-level metrics from crdb_internal.kv_store_status
	// Note: We can't filter in SQL for JSONB keys, so we fetch all and filter in Go
	storeQuery := `
		SELECT DISTINCT jsonb_object_keys(metrics) as metric_name
		FROM crdb_internal.kv_store_status
	`
	storeRows, err := t.db.Query(ctx, storeQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query kv_store_status: %w", err)
	}
	defer storeRows.Close()

	for storeRows.Next() {
		var metricName string
		if err := storeRows.Scan(&metricName); err != nil {
			return nil, fmt.Errorf("failed to scan store metric: %w", err)
		}

		// Apply filter if specified
		if filter != "" && !strings.Contains(strings.ToLower(metricName), filter) {
			continue
		}

		// Add cr.store. prefix for timeseries queries
		fullName := "cr.store." + metricName

		allMetrics = append(allMetrics, MetricInfo{
			Name:        fullName,
			Type:        "store",
			Description: "",
		})
	}

	if err := storeRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating store metrics: %w", err)
	}

	return allMetrics, nil
}

// getHTTPAddress queries the local node to determine the HTTP address
func (t *ListMetricsTool) getHTTPAddress(ctx context.Context) (string, error) {
	var port int
	err := t.db.QueryRow(ctx, `
		SELECT value::INT
		FROM crdb_internal.node_runtime_info
		WHERE component = 'UI' AND field = 'Port'
	`).Scan(&port)
	if err != nil {
		return "", fmt.Errorf("failed to query HTTP port: %w", err)
	}

	return fmt.Sprintf("localhost:%d", port), nil
}

// fetchMetricsFromPrometheus queries the /metrics endpoint and parses metric information
func (t *ListMetricsTool) fetchMetricsFromPrometheus(ctx context.Context, httpAddr string) ([]MetricInfo, error) {
	// Try both endpoints - /_status/vars first, then /metrics as fallback
	endpoints := []string{"/_status/vars", "/metrics"}

	// Determine protocol based on cluster security mode
	protocol := "https"
	if t.insecure {
		protocol = "http"
	}

	// Create HTTP client with appropriate TLS configuration
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// If using HTTPS, configure TLS to skip certificate verification
	// (we're connecting to localhost, and the cert is likely self-signed)
	if protocol == "https" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	var lastErr error
	for _, endpoint := range endpoints {
		url := fmt.Sprintf("%s://%s%s", protocol, httpAddr, endpoint)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			lastErr = fmt.Errorf("failed to create request for %s: %w", endpoint, err)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to fetch from %s: %w", endpoint, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			lastErr = fmt.Errorf("%s returned status %d: %s", endpoint, resp.StatusCode, string(body))
			continue
		}

		// Parse the Prometheus text format
		metrics, err := t.parsePrometheusMetrics(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("failed to parse metrics from %s: %w", endpoint, err)
			continue
		}

		// Success - return the metrics
		if len(metrics) > 0 {
			return metrics, nil
		}

		lastErr = fmt.Errorf("%s returned no metrics", endpoint)
	}

	return nil, fmt.Errorf("failed to fetch metrics from all endpoints: %w", lastErr)
}

// parsePrometheusMetrics parses the Prometheus exposition format
func (t *ListMetricsTool) parsePrometheusMetrics(r io.Reader) ([]MetricInfo, error) {
	scanner := bufio.NewScanner(r)
	metrics := make(map[string]MetricInfo)

	var currentType string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments that aren't TYPE or HELP
		if line == "" {
			continue
		}

		// Parse TYPE lines
		if strings.HasPrefix(line, "# TYPE ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentType = parts[3]
			}
			continue
		}

		// Parse HELP lines
		if strings.HasPrefix(line, "# HELP ") {
			parts := strings.SplitN(line, " ", 4)
			if len(parts) >= 4 {
				metricName := parts[2]
				description := parts[3]

				// Store the metric info
				metrics[metricName] = MetricInfo{
					Name:        metricName,
					Type:        currentType,
					Description: description,
				}
			}
			continue
		}

		// Skip other comment lines
		if strings.HasPrefix(line, "#") {
			continue
		}

		// This is a metric value line - extract the metric name
		spaceIdx := strings.IndexAny(line, " {")
		if spaceIdx > 0 {
			metricName := line[:spaceIdx]
			// Remove any label suffix (e.g., metric{label="value"})
			baseName := strings.Split(metricName, "{")[0]

			// If we haven't seen this metric yet, add it with minimal info
			if _, exists := metrics[baseName]; !exists {
				metrics[baseName] = MetricInfo{
					Name:        baseName,
					Type:        "unknown",
					Description: "",
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan metrics: %w", err)
	}

	// Convert map to slice
	var result []MetricInfo
	for _, metric := range metrics {
		result = append(result, metric)
	}

	return result, nil
}

// buildCategoryInfo provides helpful information about metric categories
func (t *ListMetricsTool) buildCategoryInfo(metrics []MetricInfo) string {
	categories := make(map[string]int)

	for _, metric := range metrics {
		// Determine category from prefix
		parts := strings.Split(metric.Name, ".")
		if len(parts) > 0 {
			prefix := parts[0]
			categories[prefix]++
		}
	}

	var categoryList []string
	for category, count := range categories {
		categoryList = append(categoryList, fmt.Sprintf("%s (%d metrics)", category, count))
	}
	sort.Strings(categoryList)

	return fmt.Sprintf("Available categories: %s", strings.Join(categoryList, ", "))
}
