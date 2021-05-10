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
package auto_multi_region_job

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/jobs"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/sql"
	"github.com/cockroachdb/cockroach/pkg/sql/sessiondata"
)

func init() {
	jobs.RegisterConstructor(jobspb.TypeAutoMultiRegion, func(job *jobs.Job, settings *cluster.Settings) jobs.Resumer {
		return &resumer{j: job}
	})
}

type resumer struct {
	j *jobs.Job
}

var _ jobs.Resumer = (*resumer)(nil)

func (r resumer) Resume(ctx context.Context, execCtxI interface{}) error {
	execCtx := execCtxI.(sql.JobExecContext)
	pl := r.j.Payload()
	tableMutation := pl.GetAutoMultiRegion().TableMutation
	rowMutation := pl.GetAutoMultiRegion().RowMutation
	database := pl.GetAutoMultiRegion().Database

	if _, err := execCtx.ExecCfg().InternalExecutor.ExecEx(
		ctx,
		"update-auto-multi-region-table",
		nil,
		sessiondata.InternalExecutorOverride{
			User:     execCtx.User(),
			Database: database,
		},
		tableMutation,
	); err != nil {
		return err
	}

	if _, err := execCtx.ExecCfg().InternalExecutor.ExecEx(
		ctx,
		"update-auto-multi-region-row",
		nil,
		sessiondata.InternalExecutorOverride{
			User:     execCtx.User(),
			Database: database,
		},
		rowMutation,
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
