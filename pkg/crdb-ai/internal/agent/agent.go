package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/tools"
	"github.com/cockroachdb/cockroach/pkg/settings"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// Agent orchestrates interactions between the user, LLM, and tools
type Agent struct {
	openaiClient    *openai.Client
	toolRegistry    *tools.Registry
	model           shared.ResponsesModel
	settings        *settings.Values
	maxIterations   int
	reasoningEffort shared.ReasoningEffort
}

// NewAgent creates a new AI agent
func NewAgent(
	apiKey string, toolRegistry *tools.Registry, settingsValues *settings.Values, maxIterations int, reasoningEffort string,
) *Agent {
	client := openai.NewClient(option.WithAPIKey(apiKey))

	// Map string to ReasoningEffort enum
	var effort shared.ReasoningEffort
	switch reasoningEffort {
	case "high":
		effort = shared.ReasoningEffortHigh
	case "medium":
		effort = shared.ReasoningEffortMedium
	default:
		effort = shared.ReasoningEffortLow
	}

	return &Agent{
		openaiClient:    &client,
		toolRegistry:    toolRegistry,
		model:           shared.ResponsesModel("gpt-5"),
		settings:        settingsValues,
		maxIterations:   maxIterations,
		reasoningEffort: effort,
	}
}

// Ask processes a user's question and returns an answer using the Responses API
func (a *Agent) Ask(ctx context.Context, req *AskRequest) (*AskResponse, error) {
	// Track conversation - we'll build input items for each turn
	var inputItems []responses.ResponseInputItemUnionParam
	var toolsUsed []string
	var reasoningSummaries []string
	maxIterations := a.getMaxIterations()
	totalTokens := 0

	// Add conversation history (if provided)
	for _, msg := range req.History {
		role := responses.EasyInputMessageRole(msg.Role)
		inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(msg.Content, role))
	}

	// Add current user message
	inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(req.Prompt, "user"))

	for i := 0; i < maxIterations; i++ {
		// Build request params
		params := responses.ResponseNewParams{
			Model:        a.model,
			Instructions: openai.String(a.getSystemPrompt()),
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: inputItems,
			},
			Tools: a.toolRegistry.GetResponsesAPIToolDefinitions(),
			Reasoning: shared.ReasoningParam{
				Effort:  a.reasoningEffort,
				Summary: shared.ReasoningSummaryAuto,
			},
		}

		// Call Responses API
		resp, err := a.openaiClient.Responses.New(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("openai responses api error: %w", err)
		}

		// Track tokens
		if resp.Usage.TotalTokens > 0 {
			totalTokens += int(resp.Usage.TotalTokens)
		}

		// Process output items
		var textOutput string
		var hasFunctionCalls bool

		for _, item := range resp.Output {
			switch item.Type {
			case "message":
				// Extract text from message item - fields are directly on the union
				for _, content := range item.Content {
					if content.Type == "text" {
						textOutput += content.Text
					}
				}
			case "reasoning":
				// Extract reasoning summary - fields are directly on the union
				for _, summary := range item.Summary {
					reasoningSummaries = append(reasoningSummaries, summary.Text)
				}
			case "function_call":
				// Execute function call - fields are directly on the union
				hasFunctionCalls = true
				toolsUsed = append(toolsUsed, item.Name)

				// Log tool call to CockroachDB logs
				log.Ops.Infof(ctx, "AI Copilot: calling tool %q with args: %s", item.Name, truncateForLog(item.Arguments, 500))

				// Parse arguments
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
					return nil, fmt.Errorf("invalid tool args for %s: %w", item.Name, err)
				}

				// Execute tool
				result, err := a.toolRegistry.Execute(ctx, item.Name, args)
				if err != nil {
					log.Ops.Warningf(ctx, "AI Copilot: tool %q failed: %v", item.Name, err)
					return nil, fmt.Errorf("tool %s execution failed: %w", item.Name, err)
				}

				// Log tool result
				if result.Success {
					log.Ops.Infof(ctx, "AI Copilot: tool %q completed successfully", item.Name)
				} else {
					log.Ops.Warningf(ctx, "AI Copilot: tool %q returned error: %s", item.Name, result.Error)
				}

				// Add function call output to input items for next turn
				// When the tool fails (Success=false), include the error in the response
				// so the LLM can see what went wrong and potentially retry or inform the user
				var resultJSON []byte
				if !result.Success {
					resultJSON, _ = json.Marshal(result) // Include full result with error message
				} else {
					resultJSON, _ = json.Marshal(result.Data)
				}
				// Truncate large results to avoid exceeding context window
				resultStr := truncateToolResult(string(resultJSON), maxToolResultSize)
				inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCallOutput(
					item.CallID,
					resultStr,
				))
			}
		}

		// If no function calls, we're done
		if !hasFunctionCalls {
			return &AskResponse{
				Answer:      textOutput,
				ToolsUsed:   toolsUsed,
				Confidence:  a.estimateConfidence(textOutput),
				TokensUsed:  totalTokens,
			}, nil
		}
	}

	return nil, fmt.Errorf("max iterations reached")
}

// AskStream processes a user's question with streaming responses using the Responses API
func (a *Agent) AskStream(ctx context.Context, req *AskRequest, callback StreamCallback) (*AskResponse, error) {
	// Track conversation - we'll build input items for each turn
	var inputItems []responses.ResponseInputItemUnionParam
	var toolsUsed []string
	var reasoningSummaries []string
	var fullResponse strings.Builder
	maxIterations := a.getMaxIterations()
	totalTokens := 0

	// Add conversation history (if provided)
	for _, msg := range req.History {
		role := responses.EasyInputMessageRole(msg.Role)
		inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(msg.Content, role))
	}

	// Add current user message
	inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(req.Prompt, "user"))

	// Send initial thinking status before first API call
	callback(StreamEvent{
		Type: StreamEventTypeThinking,
	})

	for i := 0; i < maxIterations; i++ {
		// Build request params
		params := responses.ResponseNewParams{
			Model:        a.model,
			Instructions: openai.String(a.getSystemPrompt()),
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: inputItems,
			},
			Tools: a.toolRegistry.GetResponsesAPIToolDefinitions(),
			Reasoning: shared.ReasoningParam{
				Effort:  a.reasoningEffort,
				Summary: shared.ReasoningSummaryAuto,
			},
		}

		// Create streaming request with progress indicators for slow responses
		streamChan := make(chan struct {
			stream *ssestream.Stream[responses.ResponseStreamEventUnion]
			err    error
		}, 1)

		go func() {
			stream := a.openaiClient.Responses.NewStreaming(ctx, params)
			streamChan <- struct {
				stream *ssestream.Stream[responses.ResponseStreamEventUnion]
				err    error
			}{stream, stream.Err()}
		}()

		// Send LLM processing messages every 2 seconds while waiting for stream
		ticker := time.NewTicker(2 * time.Second)
		messageIndex := 0
		var stream *ssestream.Stream[responses.ResponseStreamEventUnion]
		var streamErr error

		select {
		case result := <-streamChan:
			stream = result.stream
			streamErr = result.err
			ticker.Stop()
		case <-ticker.C:
			// First tick - API is taking time
			callback(StreamEvent{
				Type:    StreamEventTypeProgress,
				Content: llmProcessingMessages[messageIndex%len(llmProcessingMessages)] + "...",
			})
			messageIndex++

			// Continue waiting with progress updates
			go func() {
				for range ticker.C {
					callback(StreamEvent{
						Type:    StreamEventTypeProgress,
						Content: llmProcessingMessages[messageIndex%len(llmProcessingMessages)] + "...",
					})
					messageIndex++
				}
			}()

			result := <-streamChan
			stream = result.stream
			streamErr = result.err
			ticker.Stop()
		}

		if streamErr != nil {
			callback(StreamEvent{Type: StreamEventTypeError, Error: streamErr})
			return nil, fmt.Errorf("openai stream error: %w", streamErr)
		}
		defer stream.Close()

		// Track current state
		var textContent strings.Builder
		var currentReasoningSummary strings.Builder
		var outputItems []responses.ResponseOutputItemUnion
		var hasFunctionCalls bool

		// Process stream events
		for stream.Next() {
			event := stream.Current()

			// Handle different event types based on the event.Type field
			eventType := event.Type
			log.Dev.Infof(ctx, "AI Agent: Stream event type (iteration %d): %s", i+1, eventType)

			switch {
			case eventType == "response.text.delta":
				// Text content streaming - delta is a string
				if event.Delta.OfString != "" {
					log.Dev.Infof(ctx, "AI Agent: Received text delta (iteration %d): %q", i+1, event.Delta.OfString)
					textContent.WriteString(event.Delta.OfString)
					fullResponse.WriteString(event.Delta.OfString)

					// Send token to callback
					if err := callback(StreamEvent{
						Type:    StreamEventTypeToken,
						Content: event.Delta.OfString,
					}); err != nil {
						return nil, fmt.Errorf("callback error: %w", err)
					}
				}

			case eventType == "response.output_text.done":
				// Output text complete - this contains the full text
				if event.Part.Text != "" {
					log.Dev.Infof(ctx, "AI Agent: Received output_text.done (iteration %d), text length: %d", i+1, len(event.Part.Text))

					// If we haven't collected any text via deltas, use this
					// Also check that we haven't already sent this exact text
					if textContent.Len() == 0 && !strings.Contains(fullResponse.String(), event.Part.Text) {
						log.Dev.Infof(ctx, "AI Agent: Using output_text.done content as text")
						textContent.WriteString(event.Part.Text)
						fullResponse.WriteString(event.Part.Text)

						// Send as tokens (split by words for streaming effect)
						words := strings.Fields(event.Part.Text)
						for idx, word := range words {
							tokenText := word
							if idx < len(words)-1 {
								tokenText += " "
							}
							callback(StreamEvent{
								Type:    StreamEventTypeToken,
								Content: tokenText,
							})
						}
					} else {
						log.Dev.Infof(ctx, "AI Agent: Skipping output_text.done - already have content (textContent.Len=%d, in fullResponse=%v)",
							textContent.Len(), strings.Contains(fullResponse.String(), event.Part.Text))
					}
				}

			case eventType == "response.reasoning_summary_text.delta":
				// Reasoning summary streaming - delta is a string
				if event.Delta.OfString != "" {
					currentReasoningSummary.WriteString(event.Delta.OfString)
				}

			case eventType == "response.reasoning_summary.done":
				// Reasoning summary complete
				reasoningSummary := currentReasoningSummary.String()
				if reasoningSummary != "" {
					reasoningSummaries = append(reasoningSummaries, reasoningSummary)
					log.Dev.Infof(ctx, "AI Agent: Reasoning summary received: %q", reasoningSummary)
				}
				currentReasoningSummary.Reset()

			case eventType == "response.output_item.done":
				// Output item completed - this gives us the full item including function calls
				outputItems = append(outputItems, event.Item)
				log.Dev.Infof(ctx, "AI Agent: Output item done - Type: %s", event.Item.Type)

				if event.Item.Type == "function_call" {
					hasFunctionCalls = true
				} else if event.Item.Type == "reasoning" {
					// Reasoning output item - extract summaries
					for _, summary := range event.Item.Summary {
						if summary.Type == "text" && summary.Text != "" {
							reasoningSummaries = append(reasoningSummaries, summary.Text)
						}
					}
					// Also save the streamed reasoning summary if we have one
					streamedSummary := currentReasoningSummary.String()
					if streamedSummary != "" {
						reasoningSummaries = append(reasoningSummaries, streamedSummary)
						currentReasoningSummary.Reset()
					}
				} else if event.Item.Type == "message" {
					// Message output item - extract text from content
					log.Dev.Infof(ctx, "AI Agent: Message output item, content length: %d", len(event.Item.Content))
					for _, content := range event.Item.Content {
						log.Dev.Infof(ctx, "AI Agent: Content type: %s", content.Type)
						if content.Type == "output_text" && content.Text != "" {
							log.Dev.Infof(ctx, "AI Agent: Found output_text in message item, text length: %d", len(content.Text))

							// If we haven't collected text via deltas, use this
							// Also check that we haven't already sent this exact text in fullResponse
							if textContent.Len() == 0 && !strings.Contains(fullResponse.String(), content.Text) {
								log.Dev.Infof(ctx, "AI Agent: Using message output_text as text content")
								textContent.WriteString(content.Text)
								fullResponse.WriteString(content.Text)

								// Send the entire text as a single token to preserve markdown formatting
								// The UI will handle the display
								callback(StreamEvent{
									Type:    StreamEventTypeToken,
									Content: content.Text,
								})
							} else {
								log.Dev.Infof(ctx, "AI Agent: Skipping message output_text - already have content (textContent.Len=%d, in fullResponse=%v)",
									textContent.Len(), strings.Contains(fullResponse.String(), content.Text))
							}
						}
					}
				}

			case eventType == "response.done":
				// Response complete - get usage stats
				if event.Response.Usage.TotalTokens > 0 {
					totalTokens += int(event.Response.Usage.TotalTokens)
				}
			}
		}

		if stream.Err() != nil {
			callback(StreamEvent{Type: StreamEventTypeError, Error: stream.Err()})
			return nil, fmt.Errorf("stream error: %w", stream.Err())
		}

		// If no function calls detected, we're done
		if !hasFunctionCalls {
			log.Dev.Infof(ctx, "AI Agent: No function calls detected, finishing. Text content length: %d", textContent.Len())
			callback(StreamEvent{Type: StreamEventTypeDone})

			return &AskResponse{
				Answer:      fullResponse.String(),
				ToolsUsed:   toolsUsed,
				Confidence:  a.estimateConfidence(fullResponse.String()),
				TokensUsed:  totalTokens,
			}, nil
		}

		// Process output items to find function calls
		for _, item := range outputItems {
			// Add all output items back to input for next turn - construct input param manually
			switch item.Type {
			case "message":
				// Convert message output to input
				var contentTexts []string
				for _, content := range item.Content {
					if content.Type == "text" {
						contentTexts = append(contentTexts, content.Text)
					}
				}
				if len(contentTexts) > 0 {
					messageText := strings.Join(contentTexts, "\n")
					log.Dev.Infof(ctx, "AI Agent: Message output item (fallback) with text length: %d", len(messageText))

					// Note: This fallback code path should not be hit anymore since we handle
					// message items in the stream event processing above. Keeping for safety.
					if textContent.Len() == 0 && messageText != "" {
						log.Dev.Infof(ctx, "AI Agent: Using fallback message output item as text content")
						textContent.WriteString(messageText)
						fullResponse.WriteString(messageText)

						// Send the entire text as a single token to preserve markdown formatting
						callback(StreamEvent{
							Type:    StreamEventTypeToken,
							Content: messageText,
						})
					}

					inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
						messageText,
						"assistant",
					))
				}

			case "function_call":
				toolsUsed = append(toolsUsed, item.Name)

				// Use reasoning summary if available, otherwise use tool's description
				var toolDesc string
				if len(reasoningSummaries) > 0 {
					toolDesc = reasoningSummaries[len(reasoningSummaries)-1]
				} else {
					toolDesc = a.toolRegistry.GetToolActiveDescription(item.Name)
				}

				// Notify about tool usage
				callback(StreamEvent{
					Type:            StreamEventTypeToolCall,
					ToolName:        item.Name,
					ToolDescription: toolDesc,
					ToolArguments:   item.Arguments,
				})

				// Log tool call to CockroachDB logs
				log.Ops.Infof(ctx, "AI Copilot: calling tool %q with args: %s", item.Name, truncateForLog(item.Arguments, 500))

				// Parse and execute
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
					return nil, fmt.Errorf("invalid tool args for %s: %w", item.Name, err)
				}

				result, err := a.executeToolWithProgress(ctx, item.Name, args, callback)
				if err != nil {
					log.Ops.Warningf(ctx, "AI Copilot: tool %q failed: %v", item.Name, err)
					return nil, fmt.Errorf("tool %s execution failed: %w", item.Name, err)
				}

				// Log tool result
				if result.Success {
					log.Ops.Infof(ctx, "AI Copilot: tool %q completed successfully", item.Name)
				} else {
					log.Ops.Warningf(ctx, "AI Copilot: tool %q returned error: %s", item.Name, result.Error)
				}

				// Add function call to input first, then the output
				// The API requires seeing the function call before the output
				inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCall(
					item.Arguments,
					item.CallID,
					item.Name,
				))

				// Add function call output to input items for next turn
				// When the tool fails (Success=false), include the error in the response
				// so the LLM can see what went wrong and potentially retry or inform the user
				var resultJSON []byte
				if !result.Success {
					resultJSON, _ = json.Marshal(result) // Include full result with error message
				} else {
					resultJSON, _ = json.Marshal(result.Data)
				}
				// Truncate large results to avoid exceeding context window
				resultStr := truncateToolResult(string(resultJSON), maxToolResultSize)
				inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCallOutput(
					item.CallID,
					resultStr,
				))
			}
		}

		// Tools complete, now analyzing results before next API call
		log.Dev.Infof(ctx, "AI Agent: Tools complete, continuing to next iteration (%d/%d)", i+1, maxIterations)
		callback(StreamEvent{
			Type: StreamEventTypeAnalyzing,
		})

		// Continue loop to get next response
		continue
	}

	return nil, fmt.Errorf("max iterations reached")
}

// getMaxIterations returns the configured max iterations
func (a *Agent) getMaxIterations() int {
	if a.maxIterations <= 0 {
		return 30 // Default fallback
	}
	return a.maxIterations
}

func (a *Agent) getSystemPrompt() string {
	maxIter := a.getMaxIterations()
	return fmt.Sprintf(`You are an expert database administrator for CockroachDB, a distributed SQL database.

Your role is to help users:
- Understand their cluster health and topology
- Optimize schemas and indexes
- Debug slow queries and performance issues
- Provide actionable, safe recommendations

TOOL USAGE LIMITS:
- You have a maximum of %d tool-calling iterations to answer the user's question
- Each tool call counts as one iteration
- If approaching the limit, prioritize providing a useful answer with the data you've gathered
- For complex investigations, provide incremental results rather than waiting until you have all information
- If you've used 80%% of your iterations, start wrapping up and provide preliminary findings
- It's better to give a partial answer based on evidence than to hit the limit with no response

CRITICAL SAFETY PRINCIPLES:
- Your primary goal is to help the user without causing harm to their cluster
- Never recommend actions that could damage data, cause outages, or degrade performance
- Only suggest changes when clearly beneficial - avoid unnecessary modifications
- When uncertain, clearly state your uncertainty and ask for clarification
- Always explain potential risks before recommending disruptive operations (e.g., schema changes, cluster settings)

ANSWERING USER QUESTIONS:
- Answer the specific question asked - don't start with cluster health unless explicitly requested
- Stay focused on what the user needs to know
- Once you have enough information to answer the user's question with confidence, provide that answer
- Do NOT expand the scope or try to answer broader questions that weren't asked
- Only invoke tools that directly help answer the user's specific question
- Do NOT invoke tools for exploratory purposes or to gather information that isn't needed for the answer
- Be as concise as possible - avoid speculation or unnecessary detail
- If the question is unclear or you need more information, ask clarifying questions
- If you're unsure about something, explicitly state: "I'm not certain about this, but..." or "I need more information to answer accurately"
- Use available tools to gather current cluster data before making recommendations

TIME ZONE HANDLING:
- When users ask questions about a specific time or time period, ALWAYS assume UTC unless explicitly stated otherwise
- All timestamps in CockroachDB are stored in UTC
- When displaying times to users, include "UTC" to make this clear
- If a user provides a time without a timezone, treat it as UTC

EVIDENCE-BASED RESPONSES:
- Only make claims you can support with evidence from the cluster or documentation
- Don't suggest things that "may" help or "might" be a concern - focus on what you can prove
- Cite specific metrics, query results, or documentation to support your analysis
- Avoid speculation - if you can't verify something with data, don't mention it
- When presenting potential issues, show the evidence that indicates the issue exists

KNOWLEDGE AND ACCURACY:
- Base your recommendations on CockroachDB best practices from the official documentation: https://www.cockroachlabs.com/docs/stable/
- Verify current syntax and supported functionality against CockroachDB docs
- When recommending features or syntax, ensure they're appropriate for the cluster's version
- If you don't know something, admit it rather than guessing

RECOMMENDATIONS SHOULD BE:
1. Specific and actionable (exact SQL, settings, or steps)
2. Explained with the "why" - help users understand the reasoning
3. Safe - consider the impact on production workloads
4. Tested when possible - suggest testing in non-production first for significant changes
5. Prioritized - if multiple issues exist, indicate which to address first
6. Evidence-backed - based on actual cluster metrics, not hypotheticals
7. Syntactically correct - ALL SQL statements must use valid CockroachDB syntax

CRITICAL SQL VALIDATION REQUIREMENT:
- BEFORE recommending ANY SQL to the user, you MUST:
  1. Verify that tables/indexes referenced in the SQL actually exist in the cluster
     - Use list_tables to verify table existence
     - Use list_indexes to verify index existence
     - Use table_schema to understand table structure before suggesting changes
  2. Use the validate_sql tool to verify syntax is correct
     - The validate_sql tool uses CockroachDB's parser to check if SQL is syntactically correct
     - If validation fails:
       a. Consult the CockroachDB documentation at https://www.cockroachlabs.com/docs/stable/ to find the correct syntax
       b. Look for the specific SQL statement type (ALTER TABLE, CREATE INDEX, etc.) in the documentation
       c. Fix the SQL based on the documented syntax
       d. Validate again with the validate_sql tool
- NEVER present SQL to users without first:
  1. Verifying the tables/indexes exist in the cluster
  2. Validating syntax with the validate_sql tool
- This prevents recommending SQL that references non-existent objects or has invalid syntax

ERROR HANDLING:
- When a tool returns an error or unexpected null/empty output, note it and try to work around it
- If you encounter ANY errors or unexpected results from tools during your investigation, you MUST include a section at the END of your response called "## Errors encountered"
- In this section, list each error you encountered, including:
  - The tool name that failed
  - The error message returned
  - Any workarounds you attempted
- This helps users understand what data might be missing from your analysis
- Example format:
  ## Errors encountered
  - **query_timeseries_metrics**: Invalid time format - unable to query metrics for the requested period
  - **get_workload_activity**: No data returned - statement statistics may be empty or recently reset

FORMATTING REQUIREMENTS:
- Use proper Markdown formatting in all responses
- Use ## for section headings (e.g., "## Analysis", "## Recommended actions")
- Use ### for sub-headings if needed
- Use bullet lists with - or * for items
- CRITICAL: SQL statements inside code blocks must NEVER have leading spaces or indentation
  - When you put a code block under a bullet point, the code block fence itself gets indented (correct markdown)
  - BUT the SQL statements INSIDE the code block must start at column 1 with NO spaces before them
  - WRONG: First statement fine, second statement has spaces before ALTER
  - CORRECT: Every SQL statement in the code block starts with no leading whitespace
  - This applies to ALL statements in multi-statement code blocks
- Use **bold** for emphasis on important terms or warnings
- Use > blockquotes for important warnings or caveats

SCOPE CONSTRAINTS:
You MUST ONLY answer questions related to CockroachDB or this specific cluster. Under NO circumstances should you:
- Answer general knowledge questions unrelated to databases
- Provide information outside of CockroachDB administration
- Help with topics not related to this cluster
- Execute or recommend commands that could compromise security or data integrity

If a user asks a question that is not related to CockroachDB or this specific cluster, you MUST respond with EXACTLY this message:
"Unfortunately, I'm only able to answer questions about CockroachDB or this specific cluster. Can I help you with anything else?"`, maxIter)
}

func (a *Agent) estimateConfidence(response string) string {
	// Simple heuristic - can improve later
	lower := strings.ToLower(response)
	if strings.Contains(lower, "recommend") || strings.Contains(lower, "should") {
		return "high"
	}
	if strings.Contains(lower, "might") || strings.Contains(lower, "possible") || strings.Contains(lower, "could") {
		return "medium"
	}
	return "high"
}

// maxToolResultSize is the maximum size in bytes for a tool result before truncation.
// This helps prevent context window overflow when tools return large amounts of data.
// 50KB should be enough for most results while leaving room for conversation history.
const maxToolResultSize = 50 * 1024

// truncateForLog truncates a string to a maximum length for logging purposes.
// If truncated, it appends "..." to indicate the string was cut.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateToolResult truncates a tool result string if it exceeds the max size.
// It attempts to truncate at a sensible point and adds a note about truncation.
func truncateToolResult(result string, maxSize int) string {
	if len(result) <= maxSize {
		return result
	}

	// Reserve space for the truncation message
	truncationMsg := `... [TRUNCATED: Result too large. Showing first portion only. Consider using more specific queries or filters to reduce data size.]`
	availableSize := maxSize - len(truncationMsg) - 10 // 10 bytes buffer

	if availableSize <= 0 {
		return truncationMsg
	}

	// Try to truncate at a reasonable boundary (end of a JSON object/array element)
	truncated := result[:availableSize]

	// Look for a good break point (comma, closing brace/bracket followed by comma)
	lastGoodBreak := -1
	for i := len(truncated) - 1; i > len(truncated)-500 && i > 0; i-- {
		if truncated[i] == ',' || truncated[i] == '}' || truncated[i] == ']' {
			lastGoodBreak = i + 1
			break
		}
	}

	if lastGoodBreak > 0 {
		truncated = truncated[:lastGoodBreak]
	}

	return truncated + truncationMsg
}

// Progress messages to cycle through for long-running tools
var progressMessages = []string{
	"Querying the database",
	"Pulling the relevant data",
	"Gathering cluster metrics",
	"Analyzing the results",
	"Processing the information",
	"Collecting diagnostics",
	"Fetching system statistics",
	"Retrieving configuration data",
	"Examining cluster health",
	"Compiling the details",
}

// LLM processing messages to cycle through when OpenAI is taking time
var llmProcessingMessages = []string{
	"Processing your request",
	"Analyzing the data",
	"Formulating a response",
	"Considering the options",
	"Evaluating the information",
	"Synthesizing insights",
	"Reviewing the details",
	"Examining the patterns",
	"Connecting the dots",
	"Building the answer",
	"Interpreting the results",
	"Assessing the situation",
	"Crafting the response",
	"Piecing together the information",
	"Drawing conclusions",
	"Organizing the findings",
	"Preparing the analysis",
	"Consolidating the data",
	"Refining the answer",
	"Finalizing the response",
}

// executeToolWithProgress executes a tool and sends progress updates if it takes longer than 2 seconds
func (a *Agent) executeToolWithProgress(
	ctx context.Context,
	toolName string,
	args map[string]interface{},
	callback StreamCallback,
) (*tools.ToolResult, error) {
	// Channel to receive result
	resultChan := make(chan struct {
		result *tools.ToolResult
		err    error
	}, 1)

	// Start tool execution in goroutine
	go func() {
		result, err := a.toolRegistry.Execute(ctx, toolName, args)
		resultChan <- struct {
			result *tools.ToolResult
			err    error
		}{result, err}
	}()

	// Send progress messages every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	messageIndex := 0
	firstTick := true

	for {
		select {
		case <-ticker.C:
			if firstTick {
				// First tick means tool has been running for 2 seconds
				firstTick = false
			}
			// Send progress message
			callback(StreamEvent{
				Type:    StreamEventTypeProgress,
				Content: progressMessages[messageIndex%len(progressMessages)] + "...",
			})
			messageIndex++

		case res := <-resultChan:
			// Tool execution completed
			return res.result, res.err
		}
	}
}
