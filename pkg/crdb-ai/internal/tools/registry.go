package tools

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/ts"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openai/openai-go/responses"
)

// Registry manages all available tools
type Registry struct {
	tools    map[string]Tool
	insecure bool
	tsServer *ts.Server
}

// NewRegistry creates a new tool registry and registers all tools
func NewRegistry(db *pgxpool.Pool, insecure bool, tsServer *ts.Server) *Registry {
	r := &Registry{
		tools:    make(map[string]Tool),
		insecure: insecure,
		tsServer: tsServer,
	}

	// Phase 1 & 2: Core Tools
	r.Register(NewClusterStatusTool(db))
	r.Register(NewListTablesTool(db))
	r.Register(NewTableSchemaTool(db))
	r.Register(NewExplainQueryTool(db))
	r.Register(NewTableStatsTool(db))
	r.Register(NewListIndexesTool(db))

	// Batch 1: Performance Analysis Tools
	r.Register(NewSlowQueriesTool(db))
	r.Register(NewActiveQueriesTool(db))
	r.Register(NewWorkloadActivityTool(db))
	r.Register(NewQueryStatisticsTool(db))
	r.Register(NewHotRangesTool(db))
	r.Register(NewIndexUsageTool(db))

	// Batch 2: Health & Diagnostics Tools
	r.Register(NewNodeStatusTool(db))
	r.Register(NewReplicationStatusTool(db))
	r.Register(NewRangeStatusTool(db))
	r.Register(NewUnderReplicatedRangesTool(db))
	r.Register(NewLeaseStatusTool(db))

	// Batch 3: Availability & Operations Tools
	r.Register(NewJobsStatusTool(db))
	r.Register(NewJobsHistoryTool(db))
	r.Register(NewTransactionStatisticsTool(db))
	r.Register(NewSessionInfoTool(db))
	r.Register(NewCapacityTool(db))
	r.Register(NewNetworkLatencyTool(db))

	// Batch 4: Schema & Configuration Tools
	r.Register(NewZoneConfigsTool(db))
	r.Register(NewConstraintsTool(db))
	r.Register(NewClusterSettingsTool(db))
	r.Register(NewSequenceInfoTool(db))
	r.Register(NewTableLocalitiesTool(db))

	// Batch 5: Monitoring & Metrics Tools
	r.Register(NewMetricsSummaryTool(db, tsServer))
	r.Register(NewListMetricsTool(db, insecure))
	r.Register(NewTimeSeriesMetricsTool(db, tsServer))
	r.Register(NewRunningJobsTool(db, tsServer))
	r.Register(NewRaftStatusTool(db))
	r.Register(NewChangefeedStatusTool(db))
	r.Register(NewBackupStatusTool(db))

	// Batch 6: Security & Permissions Tools
	r.Register(NewUserGrantsTool(db))
	r.Register(NewRoleMembershipsTool(db))
	r.Register(NewTablePrivilegesTool(db))
	r.Register(NewAuditLogTool(db))
	r.Register(NewAuthenticationMethodsTool(db))

	// Batch 7: Data Distribution Tools
	r.Register(NewTableRangesTool(db))
	r.Register(NewPartitionInfoTool(db))
	r.Register(NewSplitPointsTool(db))
	r.Register(NewLocalityDistributionTool(db))
	r.Register(NewDataSkewTool(db))

	// Batch 8: Advanced Schema Tools
	r.Register(NewViewDefinitionsTool(db))
	r.Register(NewMaterializedViewsTool(db))
	r.Register(NewEnumTypesTool(db))
	r.Register(NewTableDependenciesTool(db))

	// Batch 9: Troubleshooting Tools
	r.Register(NewStatementDiagnosticsTool(db))
	r.Register(NewVersionMismatchTool(db))
	r.Register(NewClusterEventsLogTool(db))
	r.Register(NewLogsTool())

	// Batch 10: Cost Estimation Tools
	r.Register(NewQueryCostTool(db))
	r.Register(NewOptimizerHintsTool(db))

	// Batch 11: Advanced Analysis Tools
	r.Register(NewQueryInsightsTool(db))
	r.Register(NewSessionAnalysisTool(db))
	r.Register(NewTransactionRetryAnalysisTool(db))
	r.Register(NewContentionAnalysisTool(db))
	r.Register(NewMemoryAnalysisTool(db))
	r.Register(NewSchemaChangeProgressTool(db))
	r.Register(NewGossipNetworkTool(db))
	r.Register(NewLeasePreferenceAnalysisTool(db))

	// Validation Tools
	r.Register(NewValidateSQLTool())

	return r
}

// Register adds a tool to the registry
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// GetToolDescription returns the description of a tool by name
func (r *Registry) GetToolDescription(name string) string {
	tool, exists := r.tools[name]
	if !exists {
		return ""
	}
	return tool.Description()
}

// GetToolActiveDescription returns the active/conversational description of a tool by name
func (r *Registry) GetToolActiveDescription(name string) string {
	tool, exists := r.tools[name]
	if !exists {
		return ""
	}
	return tool.ActiveDescription()
}

// Execute runs a tool by name with the given arguments
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) (*ToolResult, error) {
	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	data, err := tool.Execute(ctx, args)
	if err != nil {
		return &ToolResult{
			ToolName: name,
			Success:  false,
			Error:    err.Error(),
		}, nil
	}

	return &ToolResult{
		ToolName: name,
		Success:  true,
		Data:     data,
	}, nil
}

// GetResponsesAPIToolDefinitions returns all tools in Responses API function calling format
func (r *Registry) GetResponsesAPIToolDefinitions() []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam
	for _, tool := range r.tools {
		// Convert parameters map to FunctionDefinitionParam
		params := tool.Parameters()

		// Create function tool using the Responses API format
		funcTool := responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:       tool.Name(),
				Parameters: params,
			},
		}

		tools = append(tools, funcTool)
	}
	return tools
}
