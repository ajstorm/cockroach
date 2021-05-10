// Copyright 2021 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

// Package auto_multi_region_job contains the jobs.Resumer implementation
// used for auto_multi_region stats collection.
package auto_multi_region

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/col/coldata"
	"github.com/cockroachdb/cockroach/pkg/gossip"
	"github.com/cockroachdb/cockroach/pkg/jobs"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/security"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog"
	"github.com/cockroachdb/cockroach/pkg/sql/colexec/colexectestutils"
	"github.com/cockroachdb/cockroach/pkg/sql/colexecerror"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/protoutil"
	"github.com/cockroachdb/errors"
)

// getAutoMultiRegionJobRecord gets an auto-multi-region job record.
// FIXME: should this belong in the auto_multi_region_job package?
func getAutoMultiRegionJobRecord(
	tableMutation string, rowMutation string, database string, user security.SQLUsername,
) jobs.Record {
	return jobs.Record{
		Description: "Updating automatic multi-region statistics",
		Details: jobspb.AutoMultiRegionDetails{
			TableMutation: tableMutation,
			RowMutation:   rowMutation,
			Database:      database,
		},
		// FIXME: Not sure if this should be changed to a system user.
		Username:      user,
		Progress:      jobspb.MigrationProgress{},
		NonCancelable: true,
	}
}

type updateRecord struct {
	ctx     context.Context
	tblDesc catalog.TableDescriptor
	evalCtx *tree.EvalContext //double check
	// FIXME: this is always going to be nil.  Rip it out.
	jobRegistry *jobs.Registry
	gossip      gossip.OptionalGossip
	nodeID      roachpb.NodeID
	rowsRead    int64
	rowsWritten int64
	batch       coldata.Batch
	rows        []tree.Datums
	cols        []catalog.Column
}

func CreateUpdateRecordForRead(
	ctx context.Context,
	tblDesc catalog.TableDescriptor,
	evalCtx *tree.EvalContext,
	jobsRegistry *jobs.Registry,
	gossip gossip.OptionalGossip,
	nodeID roachpb.NodeID,
	batch coldata.Batch,
	rowsRead int64,
	cols []catalog.Column,
) updateRecord {
	return updateRecord{
		ctx:         ctx,
		tblDesc:     tblDesc,
		evalCtx:     evalCtx,
		jobRegistry: jobsRegistry,
		gossip:      gossip,
		nodeID:      nodeID,
		batch:       batch,
		rowsRead:    rowsRead,
		cols:        cols,
	}
}

func (r updateRecord) UpdateForRead() error {
	desc := r.tblDesc
	ctx := r.ctx
	rowsRead := r.rowsRead
	evalCtx := r.evalCtx
	nodeID := r.nodeID
	bat := r.batch

	log.VEvent(ctx, 1, "updating auto-multi-region stats on read")

	if !desc.IsAutoMultiRegionEnabled() ||
		strings.Contains(desc.GetName(), tree.AutoMultiRegionTableName) {
		return nil
	}

	loc, err := r.getLocalityForNode(nodeID)
	if err != nil {
		colexecerror.InternalError(err)
	}

	region, found := loc.Find("region")

	// If we can't find a region on the gateway node, there's no reporting to
	// do here.
	if found {
		autoMultiRegionTableStatement := fmt.Sprintf(
			`INSERT INTO %s.%s (crdb_region, tab, reads, writes) VALUES ('%s', '%s', %d, 0) ON CONFLICT (crdb_region, tab) DO UPDATE SET reads = %q.reads + %d`,
			tree.AutoMultiRegionSchemaName,
			tree.AutoMultiRegionTableName,
			region,
			desc.GetName(),
			rowsRead,
			tree.AutoMultiRegionTableName,
			rowsRead,
		)

		// Generate the row tracking insert statement
		idx := desc.GetPrimaryIndex()
		pkColsIDs := make(map[int]bool, idx.NumKeyColumns())
		for i := 0; i < idx.NumKeyColumns(); i++ {
			pkColsIDs[int(idx.GetKeyColumnID(i))] = true
		}

		// Build up the list of columns to use in the insert statement
		colListString := "(crdb_region, reads, writes"
		onConflictString := "(crdb_region"
		colsIsInPK := make([]bool, len(r.cols))
		// FIXME: add some validation in here to ensure that we're adding
		//  at least one more column.
		for i, c := range r.cols {
			if pkColsIDs[int(c.GetID())] {
				colListString += ", " + c.GetName()
				onConflictString += ", " + c.GetName()
				colsIsInPK[i] = true
			} else {
				colsIsInPK[i] = false
			}
		}
		colListString += ")"
		onConflictString += ")"

		firstVal := true
		valuesString := ""
		for i := 0; i < bat.Length(); i++ {
			if firstVal {
				firstVal = false
			} else {
				valuesString += ", "
			}
			valuesString += fmt.Sprintf(`('%s', 1, 0`, region)
			t := colexectestutils.GetTupleFromBatch(bat, i)
			for j := 0; j < len(r.cols); j++ {
				if !colsIsInPK[j] {
					continue
				}
				valuesString += fmt.Sprintf(", %v", t[j])
			}
			valuesString += ")"
		}

		// FIXME: Turn this into a function for reliable table generation.
		rowTableName := tree.AutoMultiRegionTableName + "_" + desc.GetName()
		autoMultiRegionRowStatement := fmt.Sprintf(
			`INSERT INTO %s.%s %s VALUES %s ON CONFLICT %s DO UPDATE SET reads = %q.reads + 1`,
			tree.AutoMultiRegionSchemaName,
			rowTableName,
			colListString,
			valuesString,
			onConflictString,
			rowTableName,
		)

		rec := getAutoMultiRegionJobRecord(
			autoMultiRegionTableStatement,
			autoMultiRegionRowStatement,
			evalCtx.SessionData().Database,
			evalCtx.SessionData().User())

		jID := r.jobRegistry.MakeJobID()
		// Execute the schema job in a new transaction.
		_, err = r.jobRegistry.CreateJobWithTxn(ctx, rec, jID, nil /* txn */)
		if err != nil {
			log.VEventf(ctx, 1, "Couldn't create auto-multi-region job. Error: %v", err)
			// Eat the error.
			return nil
		}
	}
	return nil
}

// FIXME: which of these fields can we remove?

func CreateUpdateRecordForWrite(
	ctx context.Context,
	tblDesc catalog.TableDescriptor,
	evalCtx *tree.EvalContext,
	jobsRegistry *jobs.Registry,
	gossip gossip.OptionalGossip,
	nodeID roachpb.NodeID,
	rows []tree.Datums,
	rowsWritten int64,
	cols []catalog.Column,
) updateRecord {
	return updateRecord{
		ctx:         ctx,
		tblDesc:     tblDesc,
		evalCtx:     evalCtx,
		jobRegistry: jobsRegistry,
		gossip:      gossip,
		nodeID:      nodeID,
		rows:        rows,
		rowsWritten: rowsWritten,
		cols:        cols,
	}
}

func (r *updateRecord) UpdateForWrite() error {
	desc := r.tblDesc
	ctx := r.ctx
	evalCtx := r.evalCtx
	nodeID := r.nodeID
	rows := r.rowsWritten
	//bat := r.batch

	if !desc.IsAutoMultiRegionEnabled() || strings.Contains(desc.GetName(), tree.AutoMultiRegionTableName) {
		return nil
	}

	loc, err := r.getLocalityForNode(nodeID)
	if err != nil {
		return err
	}

	region, found := loc.Find("region")

	// If we can't find a region on the gateway node, there's no reporting to
	// do here.
	if found {
		autoMultiRegionTableStatement := fmt.Sprintf(
			`INSERT INTO %s.%s (crdb_region, tab, reads, writes) VALUES ('%s', '%s', 0, %d) ON CONFLICT (crdb_region, tab) DO UPDATE SET writes = %q.writes + %d`,
			tree.AutoMultiRegionSchemaName,
			tree.AutoMultiRegionTableName,
			region,
			desc.GetName(),
			rows,
			tree.AutoMultiRegionTableName,
			rows,
		)

		// BEGINNING OF CODE THAT NEEDS REWORKING

		// Generate the row tracking insert statement
		idx := desc.GetPrimaryIndex()
		pkColsIDs := make(map[int]bool, idx.NumKeyColumns())
		for i := 0; i < idx.NumKeyColumns(); i++ {
			pkColsIDs[int(idx.GetKeyColumnID(i))] = true
		}

		// Build up the list of columns to use in the insert statement
		colListString := "(crdb_region, writes, reads"
		onConflictString := "(crdb_region"
		colsIsInPK := make([]bool, len(r.cols))
		// FIXME: add some validation in here to ensure that we're adding
		//  at least one more column.
		for i, c := range r.cols {
			if pkColsIDs[int(c.GetID())] {
				colListString += ", " + c.GetName()
				onConflictString += ", " + c.GetName()
				colsIsInPK[i] = true
			} else {
				colsIsInPK[i] = false
			}
		}
		colListString += ")"
		onConflictString += ")"

		firstVal := true
		valuesString := ""
		for i := 0; i < len(r.rows); i++ {
			if firstVal {
				firstVal = false
			} else {
				valuesString += ", "
			}
			valuesString += fmt.Sprintf(`('%s', 1, 0`, region)
			t := r.rows[i]
			for j := 0; j < len(r.cols); j++ {
				if !colsIsInPK[j] {
					continue
				}
				valuesString += fmt.Sprintf(", %v", t[j])
			}
			valuesString += ")"
		}

		// FIXME: Turn this into a function for reliable table generation.
		rowTableName := tree.AutoMultiRegionTableName + "_" + desc.GetName()
		autoMultiRegionRowStatement := fmt.Sprintf(
			`INSERT INTO %s.%s %s VALUES %s ON CONFLICT %s DO UPDATE SET writes = %q.writes + 1`,
			tree.AutoMultiRegionSchemaName,
			rowTableName,
			colListString,
			valuesString,
			onConflictString,
			rowTableName,
		)

		rec := getAutoMultiRegionJobRecord(
			autoMultiRegionTableStatement,
			autoMultiRegionRowStatement,
			evalCtx.SessionData().Database,
			evalCtx.SessionData().User())

		jID := r.jobRegistry.MakeJobID()
		// Execute the schema job in a new transaction.
		_, err = r.jobRegistry.CreateJobWithTxn(ctx, rec, jID, nil /* txn */)
		if err != nil {
			log.VEventf(ctx, 1, "Couldn't create auto-multi-region job. Error: %v", err)
			// Eat the error.
			return nil
		}
	}
	return nil
}

func (r *updateRecord) getLocalityForNode(id roachpb.NodeID) (roachpb.Locality, error) {
	var loc roachpb.Locality

	// FIXME: do we still need the server config for this?!?!? Nope, but have to
	// plumb gossip down too (it exists in both cfg)
	g, err := r.gossip.OptionalErr(47899)
	if err != nil {
		return loc, err
	}

	if err := g.IterateInfos(gossip.KeyNodeIDPrefix, func(key string, i gossip.Info) error {
		bytes, err := i.Value.GetBytes()
		if err != nil {
			return errors.NewAssertionErrorWithWrappedErrf(err,
				"failed to extract bytes for key %q", key)
		}

		var d roachpb.NodeDescriptor
		if err := protoutil.Unmarshal(bytes, &d); err != nil {
			return errors.NewAssertionErrorWithWrappedErrf(err,
				"failed to parse value for key %q", key)
		}

		// Don't use node descriptors with NodeID 0, because that's meant to
		// indicate that the node has been removed from the cluster.
		if d.NodeID == id {
			loc = d.Locality
		}
		return nil
	}); err != nil {
		return loc, err
	}
	return loc, nil
}
