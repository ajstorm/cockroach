// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/agent"
	"github.com/cockroachdb/cockroach/pkg/crdb-ai/internal/tools"
	"github.com/cockroachdb/cockroach/pkg/settings"
	"github.com/cockroachdb/cockroach/pkg/sql/isql"
	"github.com/cockroachdb/cockroach/pkg/ts"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ChatRequest represents an incoming chat request.
type ChatRequest struct {
	ConversationID   *string `json:"conversation_id"`   // Optional for new conversations
	Message          string  `json:"message"`
	UserName         string  `json:"user_name"`
	ReasoningEffort  *string `json:"reasoning_effort"`  // Optional: "low", "medium", "high"
}

// ChatStreamEvent represents a chunk of the streaming response.
type ChatStreamEvent struct {
	Type            string  `json:"type"` // "token", "tool_use", "thinking", "done", "error"
	Content         *string `json:"content,omitempty"`
	ToolName        *string `json:"tool_name,omitempty"`
	ToolDescription *string `json:"tool_description,omitempty"`
	ToolArguments   *string `json:"tool_arguments,omitempty"`
	ConversationID  *string `json:"conversation_id,omitempty"`
	Error           *string `json:"error,omitempty"`
}

// AIHandler handles HTTP requests for AI chat functionality.
type AIHandler struct {
	convManager *ConversationManager
	db          isql.DB
	dbPool      *pgxpool.Pool
	insecure    bool
	sslCertsDir string
	settings    *settings.Values
	clusterID   uuid.UUID
	tsServer    *ts.Server
}

// NewAIHandler creates a new AI HTTP handler.
func NewAIHandler(
	db isql.DB,
	insecure bool,
	sslCertsDir string,
	settings *settings.Values,
	clusterID uuid.UUID,
	tsServer *ts.Server,
) *AIHandler {
	return &AIHandler{
		convManager: NewConversationManager(db, 200000), // Conservative limit to stay under API limits
		db:          db,
		dbPool:      nil, // Created lazily on first use
		insecure:    insecure,
		sslCertsDir: sslCertsDir,
		settings:    settings,
		clusterID:   clusterID,
		tsServer:    tsServer,
	}
}

// getOrCreateDBPool returns the database pool, creating it lazily if needed.
func (h *AIHandler) getOrCreateDBPool(ctx context.Context) (*pgxpool.Pool, error) {
	if h.dbPool != nil {
		return h.dbPool, nil
	}

	// Create the pool
	pool, err := CreateDBPool(ctx, h.insecure, h.sslCertsDir)
	if err != nil {
		return nil, err
	}

	h.dbPool = pool
	return pool, nil
}

// HandleChatStream handles streaming chat requests using Server-Sent Events.
func (h *AIHandler) HandleChatStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Read OpenAI API key from cluster setting dynamically
	openAIKey := OpenAIAPIKey.Get(h.settings)
	if openAIKey == "" {
		http.Error(w, "OpenAI API key not configured. Please set the 'ai.openai_api_key' cluster setting.", http.StatusServiceUnavailable)
		return
	}

	// Note: dbPool can be nil - the AI will work for general questions but won't be able
	// to execute SQL queries. The tool registry will handle nil dbPool gracefully.

	// Parse request
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if req.UserName == "" {
		http.Error(w, "user_name is required", http.StatusBadRequest)
		return
	}

	// Log the incoming user prompt
	log.Ops.Infof(ctx, "AI Copilot: received prompt from user %q: %s", req.UserName, truncateForLog(req.Message, 500))

	// Disable gzip compression for SSE streaming
	// Must be set BEFORE any other headers or writes
	w.Header().Set("Content-Encoding", "identity")

	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Get or create conversation
	var conv *Conversation
	var err error
	if req.ConversationID != nil {
		convID, err := uuid.FromString(*req.ConversationID)
		if err != nil {
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:  "error",
				Error: stringPtr(fmt.Sprintf("invalid conversation_id: %v", err)),
			})
			return
		}
		conv, err = h.convManager.GetConversation(ctx, convID)
		if err != nil {
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:  "error",
				Error: stringPtr(fmt.Sprintf("failed to get conversation: %v", err)),
			})
			return
		}
	} else {
		// Create new conversation with first message as title
		title := req.Message
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		conv, err = h.convManager.CreateConversation(ctx, req.UserName, h.clusterID, &title)
		if err != nil {
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:  "error",
				Error: stringPtr(fmt.Sprintf("failed to create conversation: %v", err)),
			})
			return
		}
		// Send conversation ID to client
		convIDStr := conv.ID.String()
		h.sendEvent(w, flusher, ChatStreamEvent{
			Type:           "conversation_created",
			ConversationID: &convIDStr,
		})
	}

	// Add user message to database
	userMsg := Message{
		ID:             uuid.MakeV4(),
		ConversationID: conv.ID,
		Role:           "user",
		Content:        &req.Message,
		CreatedAt:      time.Now(),
	}
	if err := h.convManager.AddMessage(ctx, userMsg); err != nil {
		h.sendEvent(w, flusher, ChatStreamEvent{
			Type:  "error",
			Error: stringPtr(fmt.Sprintf("failed to save user message: %v", err)),
		})
		return
	}

	// Prepare messages for AI agent (with context truncation)
	messages := append(conv.Messages, userMsg)
	truncatedMessages := h.convManager.TruncateContext(messages, h.convManager.maxContextTokens)

	// Get or create database pool for tools
	dbPool, err := h.getOrCreateDBPool(ctx)
	if err != nil {
		h.sendEvent(w, flusher, ChatStreamEvent{
			Type:  "error",
			Error: stringPtr(fmt.Sprintf("failed to connect to database: %v", err)),
		})
		return
	}

	// Create tool registry and agent
	toolRegistry := tools.NewRegistry(dbPool, h.insecure, h.tsServer)
	maxIterations := int(MaxIterations.Get(h.settings))

	// Parse reasoning effort from request, default to "low"
	reasoningEffort := "low"
	if req.ReasoningEffort != nil && *req.ReasoningEffort != "" {
		reasoningEffort = *req.ReasoningEffort
	}

	aiAgent := agent.NewAgent(openAIKey, toolRegistry, h.settings, maxIterations, reasoningEffort)

	// Convert messages to agent format
	agentMessages := make([]string, 0, len(truncatedMessages))
	for _, msg := range truncatedMessages {
		if msg.Content != nil {
			agentMessages = append(agentMessages, *msg.Content)
		}
	}

	// Stream response from AI
	h.streamAIResponse(ctx, w, flusher, aiAgent, req.Message, conv.ID)
}

// streamAIResponse streams the AI response token by token.
func (h *AIHandler) streamAIResponse(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	aiAgent *agent.Agent,
	userMessage string,
	conversationID uuid.UUID,
) {
	// Use streaming API with callback
	resp, err := aiAgent.AskStream(ctx, &agent.AskRequest{
		Prompt: userMessage,
	}, func(event agent.StreamEvent) error {
		switch event.Type {
		case agent.StreamEventTypeToken:
			// Stream each token as it arrives
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:    "token",
				Content: &event.Content,
			})
		case agent.StreamEventTypeToolCall:
			// Notify about tool usage with description and arguments
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:            "tool_use",
				ToolName:        &event.ToolName,
				ToolDescription: &event.ToolDescription,
				ToolArguments:   &event.ToolArguments,
			})
		case agent.StreamEventTypeThinking:
			// Notify that AI is thinking (before OpenAI call)
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type: "thinking",
			})
		case agent.StreamEventTypeAnalyzing:
			// Notify that AI is analyzing results (after tools, before next OpenAI call)
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type: "analyzing",
			})
		case agent.StreamEventTypeProgress:
			// Send progress message for long-running tool
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:    "progress",
				Content: &event.Content,
			})
		case agent.StreamEventTypeError:
			// Stream error event
			errStr := event.Error.Error()
			h.sendEvent(w, flusher, ChatStreamEvent{
				Type:  "error",
				Error: &errStr,
			})
		}
		return nil
	})

	if err != nil {
		h.sendEvent(w, flusher, ChatStreamEvent{
			Type:  "error",
			Error: stringPtr(fmt.Sprintf("AI error: %v", err)),
		})
		return
	}

	// Log AI response details
	log.Ops.Infof(ctx, "AI Copilot: response complete - tokens_used=%d, tools_used=%v, response=%s",
		resp.TokensUsed, resp.ToolsUsed, truncateForLog(resp.Answer, 1000))

	// Save assistant response to database
	assistantMsg := Message{
		ID:             uuid.MakeV4(),
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        &resp.Answer,
		CreatedAt:      time.Now(),
		TokenCount:     &resp.TokensUsed,
	}
	if err := h.convManager.AddMessage(ctx, assistantMsg); err != nil {
		log.Ops.Warningf(ctx, "failed to save assistant message: %v", err)
	}

	// Send done event
	h.sendEvent(w, flusher, ChatStreamEvent{
		Type: "done",
	})
}

// sendEvent sends an SSE event to the client.
func (h *AIHandler) sendEvent(w io.Writer, flusher http.Flusher, event ChatStreamEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Ops.Warningf(context.Background(), "failed to marshal event: %v", err)
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// HandleListConversations returns a list of user's conversations.
func (h *AIHandler) HandleListConversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userName := r.URL.Query().Get("user_name")
	if userName == "" {
		http.Error(w, "user_name parameter required", http.StatusBadRequest)
		return
	}

	conversations, err := h.convManager.ListUserConversations(ctx, userName, 50)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list conversations: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(conversations); err != nil {
		log.Ops.Warningf(ctx, "failed to encode conversations: %v", err)
	}
}

// HandleGetConversation returns a specific conversation with all messages.
func (h *AIHandler) HandleGetConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	convIDStr := r.URL.Query().Get("id")
	if convIDStr == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	convID, err := uuid.FromString(convIDStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid id: %v", err), http.StatusBadRequest)
		return
	}

	conversation, err := h.convManager.GetConversation(ctx, convID)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get conversation: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(conversation); err != nil {
		log.Ops.Warningf(ctx, "failed to encode conversation: %v", err)
	}
}

// HandleDeleteConversation deletes a conversation.
func (h *AIHandler) HandleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	convIDStr := r.URL.Query().Get("id")
	if convIDStr == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	convID, err := uuid.FromString(convIDStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid id: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.convManager.DeleteConversation(ctx, convID); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete conversation: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleChatCRDBEnabled returns whether the CockroachDB Copilot feature is enabled.
func (h *AIHandler) HandleChatCRDBEnabled(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if the cluster setting is enabled
	enabled := EnableAIInsights.Get(h.settings)

	response := struct {
		Enabled bool `json:"enabled"`
	}{
		Enabled: enabled,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Ops.Warningf(ctx, "failed to encode enabled response: %v", err)
		http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

// stringPtr returns a pointer to a string.
func stringPtr(s string) *string {
	return &s
}

// truncateForLog truncates a string to a maximum length for logging purposes.
// If truncated, it appends "..." to indicate the string was cut.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
