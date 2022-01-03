// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package delegate

import (
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
)

// FIXME: Have to do all of the requisite testing when creating a new
//  crdb_internal table, and the accompanying SHOW statement.

// FIXME: Right now this is broken in that it relies on the
//  crdb_internal.auto_multi_region table to be populated for it to return the
//  correct results.  This is an issue, as that table will only be properly
//  populated if the user is using the correct database.  Generalize this so
//  that either it can be called on any database, or remove the "database_name"
//  clause to remove that impression.

// delegateShowRanges implements the SHOW REGIONS statement.
func (d *delegator) delegateShowAutoMultiRegionRecommendations(
	n *tree.ShowAutoMultiRegionRecommendations,
) (tree.Statement, error) {
	// FIXME: Fill this in by selecting the tables corresponding to the supplied
	//  database.
	// We use the following heuristics for auto multi-region recommendations:
	//
	// - If more than 90% of the table's accesses are affinitized to a single
	//   region, then recommend REGIONAL BY TABLE IN <affinitized region>.
	const pctAffinitizedForRegionalTables = 0.90

	// - If more than 95% of the table's accesses are reads, and the reads are
	//   not affinitized to a single region, recommend a global table.
	// FIXME: This logic may need a bit more thought.  Ideally, we'd only want
	//  to use a global table if the table is unaffinitized.  Even if the table
	//  is read-only, if it's affinitized to a given region, it should be RBT.
	const pctReadsForGlobalTables = 0.95

	// - If more than 75% of the table's accesses are affinitized at the row
	//   level, then recommend a regional by row table.
	const pctAffinitizedForRegionalByRowTables = 0.75

	// FIXME: AVG affinity across all keys (when testing for RBR) may produce a
	//  bad result, especially if key access is skewed.
	regionalAndGlobalQuery := `
WITH
	affinity_region_stats (region, r_table_name, reads, total_row_ops)
		AS (
			SELECT 
				DISTINCT ON (r_table_name)
					 region,
					 table_name AS r_table_name,
					 reads, 
					 (reads + writes) AS total_row_ops
			FROM
				crdb_internal.auto_multi_region
			ORDER BY
				r_table_name, total_row_ops DESC
		),
	tab_grouped_stats (g_table_name, total_ops, pct_reads)
		AS (
			SELECT
				table_name AS g_table_name,
				CAST(SUM(reads + writes) AS INT) AS total_ops,
				CASE
					WHEN (SUM(reads + writes) > 0) THEN (SUM(reads) / SUM(reads + writes))
					ELSE -1
				END
					AS pct_reads
			FROM 
				crdb_internal.auto_multi_region
			GROUP BY 
				table_name
		),
	affinity_key_stats (af_tab, af_key, total_row_ops)
		AS (
			SELECT
				DISTINCT ON (tab, af_key)
	                 tab AS af_tab,
					 pk_sha AS af_key,
					 (reads + writes) AS total_row_ops
			FROM
				%s
			ORDER BY
				tab, af_key, total_row_ops DESC
		),
	key_grouped_ops (kg_tab, kg_key, total_ops)
		AS (
			SELECT
	            tab AS kg_tab,
				pk_sha AS kg_key,
				SUM(reads + writes) AS total_ops
			FROM
				%s
			GROUP BY
				tab, kg_key
		),
	key_affinity (ka_key, ka_tab, row_affinity)
		AS (
			SELECT
				af.af_key AS ka_key,
	            af.af_tab AS ka_tab,
				CASE
					WHEN total_ops <= 0 THEN 1.0
					WHEN total_ops > 0 THEN total_row_ops / total_ops
				END AS row_affinity
			FROM
				affinity_key_stats af 
	  				JOIN key_grouped_ops kg 
	  				ON af_key = kg_key AND af_tab = kg_tab
		),
	row_affinity (ra_tab, ra_affinity)
		AS (
			SELECT
				ka_tab AS ra_tab,
				AVG(row_affinity) AS ra_affinity
			FROM
				key_affinity
			GROUP BY
				ra_tab
		)
	SELECT 
		r_table_name AS table_name, 
		total_ops,
		pct_reads,
		total_row_ops / total_ops AS tbl_pct_affinitized,
		region AS affinity_region,
		ra_affinity AS affinity_key,
		CASE
			WHEN total_row_ops / total_ops > %f THEN CONCAT('REGIONAL BY TABLE IN ', region)
			WHEN pct_reads > %f THEN 'GLOBAL'	
			WHEN ra_affinity > %f THEN 'REGIONAL BY ROW'
			ELSE 'NONE'
		END AS recommendation
	FROM
		affinity_region_stats
 		JOIN tab_grouped_stats
			ON r_table_name = g_table_name
		JOIN row_affinity
			ON ra_tab = g_table_name 
	ORDER BY
		table_name
`

	// FIXME: test the newly added parts of the query above - the last four CTE
	//  portions and the new additions to the final select statement.
	finalQuery := fmt.Sprintf(regionalAndGlobalQuery,
		tree.AutoMultiRegionRowTrackingTableName,
		tree.AutoMultiRegionRowTrackingTableName,
		pctAffinitizedForRegionalTables,
		pctReadsForGlobalTables,
		pctAffinitizedForRegionalByRowTables)

	// FIXME: Build row-level query.

	//  regionalQuery := ``
	//	regionalByRowQuery := ``
	return parse(finalQuery)
}
