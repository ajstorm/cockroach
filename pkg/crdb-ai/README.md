# CockroachDB AI Assistant

AI-powered tool for CockroachDB cluster management, schema optimization, and query debugging.

## Features

- **Cluster Health Monitoring**: Get real-time status of nodes and ranges
- **Schema Optimization**: Receive AI-powered recommendations for table schemas
- **Query Debugging**: Analyze slow queries and get optimization suggestions
- **RESTful API**: HTTP API for integration with other tools

## Quick Start

### Prerequisites

- Go 1.21 or later
- CockroachDB cluster (local or remote)
- OpenAI API key

### Build

```bash
cd pkg/crdb-ai
go build -o crdb-ai .
```

### Using the CLI (Recommended)

The easiest way to use crdb-ai is through the CLI:

```bash
export OPENAI_API_KEY="sk-..."

./crdb-ai ask \
  --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "What is the state of my cluster?"
```

**Example output:**
```
🤔 Thinking...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📊 Answer:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Your cluster is healthy. It currently has one active node...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔧 Tools used: get_cluster_status
📈 Confidence: high | 🎫 Tokens: 467
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### Using the HTTP Server

For integration with other tools, you can run crdb-ai as a server:

```bash
export OPENAI_API_KEY="sk-..."
export API_KEY="your-secret-key"

./crdb-ai server \
  --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable"
```

Then query via HTTP:

```bash
# Check health
curl http://localhost:8080/health

# Ask a question
curl -X POST http://localhost:8080/ask \
  -H "Authorization: Bearer your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "What is the state of my cluster?"}'
```

## Configuration

### Server Flags

- `--db-url`: CockroachDB connection URL (required)
- `--openai-key`: OpenAI API key (or set `OPENAI_API_KEY` env var)
- `--api-key`: Server API key for authentication (or set `API_KEY` env var)
- `--port`: Server port (default: 8080)
- `--rate-limit`: Requests per second rate limit (default: 10)
- `--rate-burst`: Rate limit burst size (default: 20)

### Environment Variables

- `OPENAI_API_KEY`: OpenAI API key (required)
- `API_KEY`: Server authentication key (required)

## API Endpoints

### GET /health

Health check endpoint.

**Response:**
```json
{
  "status": "healthy",
  "time": "2025-01-01T00:00:00Z"
}
```

### POST /ask

Ask the AI assistant a question about your cluster.

**Request:**
```json
{
  "prompt": "What is the state of my cluster?",
  "context": {
    "database": "mydb",
    "table": "users"
  }
}
```

**Response:**
```json
{
  "answer": "Your cluster has 3 nodes, all healthy...",
  "tools_used": ["get_cluster_status"],
  "recommendations": [],
  "confidence": "high",
  "tokens_used": 412
}
```

## Available Tools

The AI assistant has access to **50 comprehensive tools** across 10 categories:

### Core Tools (6 tools)
1. **get_cluster_status**: Cluster health and topology
2. **list_tables**: List all databases and tables
3. **get_table_schema**: Detailed table schema information
4. **explain_query**: SQL query execution plan analysis
5. **get_table_stats**: Table statistics and row counts
6. **list_indexes**: Index definitions and types

### Performance Analysis (5 tools)
7. **get_slow_queries**: Identify slowest queries by latency
8. **get_active_queries**: Show currently executing queries
9. **get_query_statistics**: Detailed stats for specific query patterns
10. **get_hot_ranges**: Identify contended tables and hot ranges
11. **analyze_index_usage**: Find unused or rarely-used indexes

### Health & Diagnostics (5 tools)
12. **get_node_status**: Detailed node health and liveness
13. **get_replication_status**: Replication health across cluster
14. **get_range_status**: Detailed range information
15. **check_under_replicated_ranges**: Find under-replicated ranges
16. **get_lease_status**: Lease distribution analysis

### Availability & Operations (5 tools)
17. **get_jobs_status**: Running, failed, and recent jobs
18. **get_transaction_statistics**: Transaction latency and retries
19. **get_session_info**: Active database sessions
20. **check_capacity**: Storage capacity and usage
21. **get_network_latency**: Inter-node network latency

### Schema & Configuration (5 tools)
22. **get_zone_configs**: Replication zone configurations
23. **get_constraints**: Table constraints (FK, check, unique)
24. **get_cluster_settings**: Cluster settings and values
25. **get_sequence_info**: Sequence definitions
26. **get_table_localities**: Multi-region table configurations

### Monitoring & Metrics (5 tools)
27. **get_metrics_summary**: CPU, memory, disk, network metrics
28. **get_mvcc_stats**: MVCC statistics and dead data
29. **get_raft_status**: Raft consensus status
30. **get_changefeed_status**: Changefeed status and errors
31. **get_backup_status**: Backup/restore job status

### Security & Permissions (5 tools)
32. **get_user_grants**: User privileges and grants
33. **get_role_memberships**: Role hierarchy and memberships
34. **check_table_privileges**: Table-level permissions
35. **get_audit_log**: Security audit log entries
36. **get_authentication_methods**: Auth methods and configuration

### Data Distribution (5 tools)
37. **get_table_ranges**: Range distribution for tables
38. **get_partition_info**: Table partitioning information
39. **check_split_points**: Recommended split points
40. **get_locality_distribution**: Data distribution by locality
41. **analyze_skew**: Detect data skew across ranges

### Advanced Schema (4 tools)
42. **get_view_definitions**: View definitions and dependencies
43. **get_materialized_views**: Materialized view status
44. **get_enum_types**: User-defined enum types
45. **get_table_dependencies**: Foreign key relationships graph

### Troubleshooting (3 tools)
46. **get_range_log**: Range event history
47. **get_statement_diagnostics**: Statement bundle information
48. **check_version_mismatch**: Node version compatibility

### Cost Estimation (2 tools)
49. **estimate_query_cost**: Query cost estimation
50. **get_optimizer_hints**: Query optimizer statistics

## Example Questions

Try these questions with the CLI:

### Cluster Health
```bash
./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "What is the state of my cluster?"

./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "Are there any issues with my cluster?"
```

### Schema Analysis
```bash
./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "What is the schema of the users table?"

./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "What indexes exist on the users table?"

./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "How many rows are in the users table?"
```

### Query Optimization
```bash
./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "Why is this query slow: SELECT * FROM users WHERE email = 'alice@example.com'"

./crdb-ai ask --db-url "postgresql://root@localhost:26257/defaultdb?sslmode=disable" \
  "Can you help me optimize the users table?"
```

## Security

- The service uses a read-only database connection
- API key required for all requests (except /health)
- Rate limiting enabled by default (10 req/s)
- Only metadata sent to OpenAI (no actual data)

## Development

### Project Structure

```
pkg/crdb-ai/
├── main.go              # Entry point
├── cmd/                 # CLI commands
│   ├── root.go
│   └── server.go
├── internal/
│   ├── agent/          # AI agent orchestration
│   ├── tools/          # CRDB query tools
│   ├── db/             # Database connection
│   ├── server/         # HTTP server
│   └── config/         # Configuration
└── testdata/           # Test data and scripts
```

### Implementation Status

**Phase 1 (Complete):**
- ✅ Project structure
- ✅ Database connection pool
- ✅ Tool interface and registry
- ✅ Agent orchestration loop
- ✅ HTTP server with auth and rate limiting
- ✅ CLI with `ask` command
- ✅ Tool: get_cluster_status

**Phase 2 (Complete):**
- ✅ Tool: get_table_schema
- ✅ Tool: explain_query
- ✅ Tool: get_table_stats
- ✅ Tool: list_indexes
- ✅ Multi-tool optimization queries
- ✅ Comprehensive testing

### Potential Future Enhancements

- Integration with CockroachDB DB Console
- Prometheus/TSDB metrics integration for historical analysis
- Query log analysis for workload patterns
- Anthropic Claude support for alternative LLM
- Auto-remediation capabilities (apply DDL changes)
- Multi-cluster comparison and analysis
- Capacity planning and forecasting

## License

See CockroachDB license.
