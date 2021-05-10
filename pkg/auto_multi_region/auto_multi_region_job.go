// Copyright 2018 The Cockroach Authors.
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
package auto_multi_region_job

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/sql"

	"github.com/cockroachdb/cockroach/pkg/jobs"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/security"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/sql/sessiondata"
)

func init() {
	jobs.RegisterConstructor(jobspb.TypeAutoMultiRegion, func(job *jobs.Job, settings *cluster.Settings) jobs.Resumer {
		return &resumer{j: job}
	})
}

// NewRecord constructs a new jobs.Record for auto multi-region.
func NewRecord(mutation string, database string, user security.SQLUsername) jobs.Record {
	return jobs.Record{
		Description: "Updating automatic multi-region statistics",
		Details: jobspb.AutoMultiRegionDetails{
			Mutation: mutation,
			Database: database,
		},
		// FIXME: Not sure if this should be changed to a system user.
		Username:      user,
		Progress:      jobspb.MigrationProgress{},
		NonCancelable: true,
	}
}

type resumer struct {
	j *jobs.Job
}

var _ jobs.Resumer = (*resumer)(nil)

func (r resumer) Resume(ctx context.Context, execCtxI interface{}) error {
	execCtx := execCtxI.(sql.JobExecContext)
	db := execCtx.ExecCfg().DB
	// FIXME: this might not be right either.
	nodeID, _ := execCtx.ExecCfg().NodeID.OptionalNodeID()

	txn := kv.NewTxnWithSteppingEnabled(ctx, db, nodeID)

	pl := r.j.Payload()
	mutation := pl.GetAutoMultiRegion().Mutation
	database := pl.GetAutoMultiRegion().Database

	if _, err := execCtx.ExecCfg().InternalExecutor.ExecEx(
		ctx,
		"update-auto-multi-region-table",
		txn,
		sessiondata.InternalExecutorOverride{
			// FIXME: not sure if this will be set right.
			User:     execCtx.User(),
			Database: database,
		},
		mutation,
	); err != nil {
		return err
	}

	return nil
}

// OnFailOrCancel doesn't do anything for auto multi-region.  We're prepared to
// have some inaccuracies in our stats.
func (r resumer) OnFailOrCancel(ctx context.Context, execCtx interface{}) error {
	return nil
}
