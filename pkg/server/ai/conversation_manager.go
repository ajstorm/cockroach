// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/security/username"
	"github.com/cockroachdb/cockroach/pkg/sql/isql"
	"github.com/cockroachdb/cockroach/pkg/sql/sessiondata"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
)

// Message represents a single message in a conversation.
type Message struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	Role           string    `json:"role"` // "user", "assistant", "tool"
	Content        *string   `json:"content,omitempty"`
	ToolCalls      *string   `json:"tool_calls,omitempty"` // JSON string
	ToolCallID     *string   `json:"tool_call_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	TokenCount     *int      `json:"token_count,omitempty"`
}

// Conversation represents an AI conversation session.
type Conversation struct {
	ID            uuid.UUID `json:"id"`
	UserName      string    `json:"user_name"`
	ClusterID     uuid.UUID `json:"cluster_id"`
	CreatedAt     time.Time `json:"created_at"`
	LastMessageAt time.Time `json:"last_message_at"`
	Title         *string   `json:"title,omitempty"`
	Messages      []Message `json:"messages,omitempty"`
}

// ConversationManager handles storage and retrieval of AI conversations.
type ConversationManager struct {
	db              isql.DB
	maxContextTokens int
}

// NewConversationManager creates a new conversation manager.
func NewConversationManager(db isql.DB, maxContextTokens int) *ConversationManager {
	return &ConversationManager{
		db:              db,
		maxContextTokens: maxContextTokens,
	}
}

// CreateConversation creates a new conversation.
func (cm *ConversationManager) CreateConversation(
	ctx context.Context, userName string, clusterID uuid.UUID, title *string,
) (*Conversation, error) {
	conversationID := uuid.MakeV4()
	now := time.Now()

	err := cm.db.Txn(ctx, func(ctx context.Context, txn isql.Txn) error {
		// Convert title pointer to tree.Datum (NULL or string value)
		var titleDatum tree.Datum = tree.DNull
		if title != nil {
			titleDatum = tree.NewDString(*title)
		}

		_, err := txn.ExecEx(ctx, "create-conversation", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`INSERT INTO system.ai_conversations (id, user_name, cluster_id, created_at, last_message_at, title)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			conversationID, userName, clusterID, now, now, titleDatum,
		)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	return &Conversation{
		ID:            conversationID,
		UserName:      userName,
		ClusterID:     clusterID,
		CreatedAt:     now,
		LastMessageAt: now,
		Title:         title,
		Messages:      []Message{},
	}, nil
}

// AddMessage adds a message to a conversation and updates last_message_at.
func (cm *ConversationManager) AddMessage(
	ctx context.Context, msg Message,
) error {
	return cm.db.Txn(ctx, func(ctx context.Context, txn isql.Txn) error {
		// Convert nullable fields to tree.Datum
		var contentDatum, toolCallsDatum, toolCallIDDatum, tokenCountDatum tree.Datum
		contentDatum = tree.DNull
		if msg.Content != nil {
			contentDatum = tree.NewDString(*msg.Content)
		}
		toolCallsDatum = tree.DNull
		if msg.ToolCalls != nil {
			toolCallsDatum = tree.NewDString(*msg.ToolCalls)
		}
		toolCallIDDatum = tree.DNull
		if msg.ToolCallID != nil {
			toolCallIDDatum = tree.NewDString(*msg.ToolCallID)
		}
		tokenCountDatum = tree.DNull
		if msg.TokenCount != nil {
			tokenCountDatum = tree.NewDInt(tree.DInt(*msg.TokenCount))
		}

		// Insert the message
		_, err := txn.ExecEx(ctx, "add-message", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`INSERT INTO system.ai_messages
			 (id, conversation_id, role, content, tool_calls, tool_call_id, created_at, token_count)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			msg.ID, msg.ConversationID, msg.Role, contentDatum, toolCallsDatum, toolCallIDDatum, msg.CreatedAt, tokenCountDatum,
		)
		if err != nil {
			return fmt.Errorf("failed to insert message: %w", err)
		}

		// Update conversation's last_message_at
		_, err = txn.ExecEx(ctx, "update-conversation-timestamp", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`UPDATE system.ai_conversations SET last_message_at = $1 WHERE id = $2`,
			msg.CreatedAt, msg.ConversationID,
		)
		if err != nil {
			return fmt.Errorf("failed to update conversation timestamp: %w", err)
		}

		return nil
	})
}

// GetConversation retrieves a conversation with all its messages.
func (cm *ConversationManager) GetConversation(
	ctx context.Context, conversationID uuid.UUID,
) (*Conversation, error) {
	var conv Conversation
	var messages []Message

	err := cm.db.Txn(ctx, func(ctx context.Context, txn isql.Txn) error {
		// Get conversation metadata
		row, err := txn.QueryRowEx(ctx, "get-conversation", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`SELECT id, user_name, cluster_id, created_at, last_message_at, title
			 FROM system.ai_conversations WHERE id = $1`,
			conversationID,
		)
		if err != nil {
			return fmt.Errorf("failed to query conversation: %w", err)
		}
		if row == nil {
			return fmt.Errorf("conversation not found: %s", conversationID)
		}

		conv.ID = tree.MustBeDUuid(row[0]).UUID
		conv.UserName = string(tree.MustBeDString(row[1]))
		conv.ClusterID = tree.MustBeDUuid(row[2]).UUID
		conv.CreatedAt = tree.MustBeDTimestampTZ(row[3]).Time
		conv.LastMessageAt = tree.MustBeDTimestampTZ(row[4]).Time
		if row[5] != tree.DNull {
			title := string(tree.MustBeDString(row[5]))
			conv.Title = &title
		}

		// Get all messages
		rows, err := txn.QueryBufferedEx(ctx, "get-messages", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`SELECT id, conversation_id, role, content, tool_calls, tool_call_id, created_at, token_count
			 FROM system.ai_messages
			 WHERE conversation_id = $1
			 ORDER BY created_at ASC`,
			conversationID,
		)
		if err != nil {
			return fmt.Errorf("failed to query messages: %w", err)
		}

		for _, row := range rows {
			msg := Message{
				ID:             tree.MustBeDUuid(row[0]).UUID,
				ConversationID: tree.MustBeDUuid(row[1]).UUID,
				Role:           string(tree.MustBeDString(row[2])),
				CreatedAt:      tree.MustBeDTimestampTZ(row[6]).Time,
			}
			if row[3] != tree.DNull {
				content := string(tree.MustBeDString(row[3]))
				msg.Content = &content
			}
			if row[4] != tree.DNull {
				toolCalls := string(tree.MustBeDString(row[4]))
				msg.ToolCalls = &toolCalls
			}
			if row[5] != tree.DNull {
				toolCallID := string(tree.MustBeDString(row[5]))
				msg.ToolCallID = &toolCallID
			}
			if row[7] != tree.DNull {
				tokenCount := int(tree.MustBeDInt(row[7]))
				msg.TokenCount = &tokenCount
			}
			messages = append(messages, msg)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	conv.Messages = messages
	return &conv, nil
}

// ListUserConversations retrieves all conversations for a user, ordered by most recent.
func (cm *ConversationManager) ListUserConversations(
	ctx context.Context, userName string, limit int,
) ([]Conversation, error) {
	// Initialize as empty slice, not nil, so it JSON-encodes as [] not null
	conversations := make([]Conversation, 0)

	err := cm.db.Txn(ctx, func(ctx context.Context, txn isql.Txn) error {
		rows, err := txn.QueryBufferedEx(ctx, "list-conversations", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`SELECT id, user_name, cluster_id, created_at, last_message_at, title
			 FROM system.ai_conversations
			 WHERE user_name = $1
			 ORDER BY last_message_at DESC
			 LIMIT $2`,
			userName, limit,
		)
		if err != nil {
			return fmt.Errorf("failed to list conversations: %w", err)
		}

		for _, row := range rows {
			conv := Conversation{
				ID:            tree.MustBeDUuid(row[0]).UUID,
				UserName:      string(tree.MustBeDString(row[1])),
				ClusterID:     tree.MustBeDUuid(row[2]).UUID,
				CreatedAt:     tree.MustBeDTimestampTZ(row[3]).Time,
				LastMessageAt: tree.MustBeDTimestampTZ(row[4]).Time,
			}
			if row[5] != tree.DNull {
				title := string(tree.MustBeDString(row[5]))
				conv.Title = &title
			}
			conversations = append(conversations, conv)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return conversations, nil
}

// TruncateContext intelligently truncates conversation messages to fit within token budget.
// Strategy: Keep system prompt + recent N messages, summarize the rest.
func (cm *ConversationManager) TruncateContext(
	messages []Message, maxTokens int,
) []Message {
	if len(messages) == 0 {
		return messages
	}

	// Simple strategy: keep last 10 messages (about 5 exchanges)
	// This is conservative to avoid hitting token limits with large tool results
	// TODO: Implement smarter truncation with actual token counting and summarization
	keepCount := 10
	if len(messages) <= keepCount {
		return messages
	}

	// Keep the most recent messages
	return messages[len(messages)-keepCount:]
}

// DeleteConversation deletes a conversation and all its messages (CASCADE).
func (cm *ConversationManager) DeleteConversation(
	ctx context.Context, conversationID uuid.UUID,
) error {
	return cm.db.Txn(ctx, func(ctx context.Context, txn isql.Txn) error {
		_, err := txn.ExecEx(ctx, "delete-conversation", txn.KV(),
			sessiondata.InternalExecutorOverride{User: username.RootUserName()},
			`DELETE FROM system.ai_conversations WHERE id = $1`,
			conversationID,
		)
		if err != nil {
			return fmt.Errorf("failed to delete conversation: %w", err)
		}
		return nil
	})
}
