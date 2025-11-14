package tools

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LocalityDistributionTool shows data distribution by locality
type LocalityDistributionTool struct {
	db *pgxpool.Pool
}

// NewLocalityDistributionTool creates a new locality distribution tool
func NewLocalityDistributionTool(db *pgxpool.Pool) *LocalityDistributionTool {
	return &LocalityDistributionTool{db: db}
}

// LocalityDistribution represents data distribution for a locality
type LocalityDistribution struct {
	Locality    string  `json:"locality"`
	NodeCount   int     `json:"node_count"`
	RangeCount  int     `json:"range_count"`
	TotalSizeGB float64 `json:"total_size_gb"`
	Percentage  float64 `json:"percentage"`
}

// LocalityDistributionResult contains locality distribution information
type LocalityDistributionResult struct {
	Distribution []LocalityDistribution `json:"distribution"`
	TotalRanges  int                    `json:"total_ranges"`
	TotalSizeGB  float64                `json:"total_size_gb"`
	Note         string                 `json:"note,omitempty"`
}

func (t *LocalityDistributionTool) Name() string {
	return "get_locality_distribution"
}

func (t *LocalityDistributionTool) Description() string {
	return "Show data distribution across different localities/regions in multi-region deployments"
}

func (t *LocalityDistributionTool) ActiveDescription() string {
	return "I'm checking how your data is distributed across different regions"
}

func (t *LocalityDistributionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

func (t *LocalityDistributionTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var result LocalityDistributionResult

	// Get locality information from nodes and their range counts
	query := `
		SELECT
			COALESCE(gn.locality, 'default') as locality,
			COUNT(DISTINCT gn.node_id) as node_count,
			COUNT(DISTINCT r.range_id) as range_count,
			SUM(r.range_size) as total_size
		FROM crdb_internal.gossip_nodes gn
		LEFT JOIN crdb_internal.ranges r ON gn.node_id = r.lease_holder
		GROUP BY locality
		ORDER BY range_count DESC
	`

	rows, err := t.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query locality distribution: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ld LocalityDistribution
		var totalSize *int64

		if err := rows.Scan(
			&ld.Locality,
			&ld.NodeCount,
			&ld.RangeCount,
			&totalSize,
		); err != nil {
			return nil, fmt.Errorf("failed to scan locality distribution row: %w", err)
		}

		if totalSize != nil {
			ld.TotalSizeGB = float64(*totalSize) / (1024 * 1024 * 1024)
		}

		result.Distribution = append(result.Distribution, ld)
		result.TotalRanges += ld.RangeCount
		result.TotalSizeGB += ld.TotalSizeGB
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating locality distribution rows: %w", err)
	}

	// Calculate percentages
	if result.TotalRanges > 0 {
		for i := range result.Distribution {
			result.Distribution[i].Percentage = float64(result.Distribution[i].RangeCount) / float64(result.TotalRanges) * 100
		}
	}

	if len(result.Distribution) == 0 {
		result.Note = "No locality information available. Cluster may not be configured for multi-region."
	} else if len(result.Distribution) == 1 {
		result.Note = "Single locality detected. This appears to be a single-region deployment."
	} else {
		result.Note = fmt.Sprintf("Data distributed across %d localities", len(result.Distribution))
	}

	return result, nil
}
