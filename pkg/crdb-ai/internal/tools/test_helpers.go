// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// CreateTestDBPool creates a pgxpool connected to the test server
func CreateTestDBPool(t *testing.T, s serverutils.TestServerInterface) *pgxpool.Pool {
	pgURL, cleanup := s.PGUrl(t)
	defer cleanup()

	// Convert the URL to the format pgxpool expects
	connStr := pgURL.String()

	config, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse connection string")

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	require.NoError(t, err, "Failed to create connection pool")

	// Test the connection
	err = pool.Ping(context.Background())
	require.NoError(t, err, "Failed to ping database")

	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

// ExecuteQuery executes a query and returns the result count
func ExecuteQuery(t *testing.T, pool *pgxpool.Pool, query string, args ...interface{}) int {
	rows, err := pool.Query(context.Background(), query, args...)
	require.NoError(t, err, fmt.Sprintf("Failed to execute query: %s", query))
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	require.NoError(t, rows.Err(), "Error iterating rows")
	return count
}
