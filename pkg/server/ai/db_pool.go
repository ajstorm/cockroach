// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package ai

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateDBPool creates a pgxpool connection for AI tools.
// This uses a loopback connection to the local SQL server.
func CreateDBPool(ctx context.Context, insecure bool, sslCertsDir string) (*pgxpool.Pool, error) {
	var connStr string

	if insecure {
		// Insecure mode - no SSL
		connStr = "postgresql://root@localhost:26257/defaultdb?sslmode=disable"
	} else {
		// Secure mode - use certificates from the certs directory
		connStr = fmt.Sprintf(
			"postgresql://root@localhost:26257/defaultdb?sslmode=require&sslrootcert=%s/ca.crt&sslcert=%s/client.root.crt&sslkey=%s/client.root.key",
			sslCertsDir, sslCertsDir, sslCertsDir,
		)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test the connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}
