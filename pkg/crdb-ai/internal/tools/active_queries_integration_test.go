// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tools

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/stretchr/testify/require"
)

// TestActiveQueriesIntegration tests whether active_queries tool can actually see queries
func TestActiveQueriesIntegration(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	s, sqlDB, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer s.Stopper().Stop(ctx)

	// Create a test table
	_, err := sqlDB.Exec("CREATE TABLE test_table (id INT PRIMARY KEY, value TEXT)")
	require.NoError(t, err)

	// Insert some test data
	_, err = sqlDB.Exec("INSERT INTO test_table VALUES (1, 'test')")
	require.NoError(t, err)

	// Create the tool with our test DB pool
	pool := CreateTestDBPool(t, s)
	tool := NewActiveQueriesTool(pool)

	// Start a long-running query in a goroutine
	queryChan := make(chan error, 1)
	go func() {
		_, err := sqlDB.Exec("SELECT pg_sleep(5)")
		queryChan <- err
	}()

	// Give the query time to start
	time.Sleep(100 * time.Millisecond)

	// Now check if we can see it
	result, err := tool.Execute(ctx, map[string]interface{}{})
	require.NoError(t, err)

	queryResult, ok := result.(ActiveQueriesResult)
	require.True(t, ok, "Expected ActiveQueriesResult")

	t.Logf("Active queries count: %d", queryResult.Count)
	t.Logf("Number of queries returned: %d", len(queryResult.Queries))

	if len(queryResult.Queries) > 0 {
		for i, q := range queryResult.Queries {
			t.Logf("Query %d: %s (user: %s, duration: %.2fs)", i, q.Query, q.UserName, q.DurationSec)
		}
	}

	// We should see at least 1 active query (the pg_sleep)
	require.Greater(t, queryResult.Count, 0,
		"Expected to see at least one active query (pg_sleep)")

	// Wait for the query to finish
	select {
	case err := <-queryChan:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Query took too long to complete")
	}
}

// TestSessionInfoIntegration tests whether session_info tool can see sessions
func TestSessionInfoIntegration(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	s, _, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer s.Stopper().Stop(ctx)

	// Create the tool with our test DB pool
	pool := CreateTestDBPool(t, s)
	tool := NewSessionInfoTool(pool)

	// Execute the tool
	result, err := tool.Execute(ctx, map[string]interface{}{})
	require.NoError(t, err)

	sessionResult, ok := result.(SessionInfoResult)
	require.True(t, ok, "Expected SessionInfoResult")

	t.Logf("Total sessions: %d", sessionResult.TotalSessions)
	t.Logf("Active sessions: %d", sessionResult.ActiveSessions)
	t.Logf("Idle sessions: %d", sessionResult.IdleSessions)

	if len(sessionResult.Sessions) > 0 {
		for i, s := range sessionResult.Sessions {
			t.Logf("Session %d: user=%s, app=%s, status=%s",
				i, s.UserName, s.ApplicationName, s.Status)
		}
	}

	// We should see at least 1 session (our own connection)
	require.Greater(t, sessionResult.TotalSessions, 0,
		"Expected to see at least one session (our test connection)")
}

// TestWorkloadActivityIntegration tests whether workload_activity shows recent queries
func TestWorkloadActivityIntegration(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	s, sqlDB, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer s.Stopper().Stop(ctx)

	// Create a test table
	_, err := sqlDB.Exec("CREATE TABLE workload_test (id INT PRIMARY KEY, value TEXT)")
	require.NoError(t, err)

	// Execute some queries to generate workload statistics
	for i := 0; i < 100; i++ {
		_, err := sqlDB.Exec("INSERT INTO workload_test VALUES ($1, $2)", i, "test")
		require.NoError(t, err)
	}

	// Execute some SELECTs
	for i := 0; i < 50; i++ {
		_, err := sqlDB.Exec("SELECT * FROM workload_test WHERE id = $1", i)
		require.NoError(t, err)
	}

	// Wait a bit for stats to be collected
	time.Sleep(1 * time.Second)

	// Create the tool with our test DB pool
	pool := CreateTestDBPool(t, s)
	tool := NewWorkloadActivityTool(pool)

	// Execute the tool
	result, err := tool.Execute(ctx, map[string]interface{}{})
	require.NoError(t, err)

	workloadResult, ok := result.(WorkloadActivityResult)
	require.True(t, ok, "Expected WorkloadActivityResult")

	t.Logf("Total queries: %d", workloadResult.TotalQueries)
	t.Logf("Total executions: %d", workloadResult.TotalExecutions)
	t.Logf("Note: %s", workloadResult.Note)

	if len(workloadResult.Activities) > 0 {
		for i, a := range workloadResult.Activities {
			t.Logf("Activity %d: query=%s, executions=%d, avg_latency=%.2fms",
				i, a.QuerySummary, a.ExecutionCount, a.AvgLatencyMs)
		}
	}

	// We should see some workload activity from our INSERTs and SELECTs
	require.Greater(t, workloadResult.TotalExecutions, int64(0),
		"Expected to see workload activity from our test queries")
}
