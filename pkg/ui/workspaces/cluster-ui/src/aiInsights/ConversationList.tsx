// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import React from "react";
import { Conversation } from "./types";
import { Button } from "../button";
import styles from "./ConversationList.module.scss";

export interface ConversationListProps {
  conversations: Conversation[];
  currentConversationId?: string;
  onSelectConversation: (conversationId: string) => void;
  onNewConversation: () => void;
  onDeleteConversation: (conversationId: string) => void;
  isStreaming?: boolean;
}

export const ConversationList: React.FC<ConversationListProps> = ({
  conversations,
  currentConversationId,
  onSelectConversation,
  onNewConversation,
  onDeleteConversation,
  isStreaming = false,
}) => {
  const formatDate = (dateStr: string) => {
    const date = new Date(dateStr);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return "Just now";
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    if (diffDays < 7) return `${diffDays}d ago`;
    return date.toLocaleDateString();
  };

  const handleDelete = (e: React.MouseEvent, conversationId: string) => {
    e.stopPropagation();
    if (confirm("Are you sure you want to delete this conversation?")) {
      onDeleteConversation(conversationId);
    }
  };

  return (
    <div className={styles.conversationList}>
      <div className={styles.newButtonContainer}>
        <Button type="primary" onClick={onNewConversation} disabled={isStreaming}>
          + New Conversation
        </Button>
      </div>
      <div className={styles.conversations}>
        {conversations.length === 0 ? (
          <div className={styles.emptyState}>No conversations yet</div>
        ) : (
          conversations.map((conv) => (
            <div
              key={conv.id}
              className={`${styles.conversationItem} ${
                conv.id === currentConversationId ? styles.active : ""
              }`}
              onClick={() => onSelectConversation(conv.id)}
            >
              <div className={styles.conversationHeader}>
                <div className={styles.conversationTitle}>
                  {conv.title || "Untitled"}
                </div>
                <button
                  className={styles.deleteButton}
                  onClick={(e) => handleDelete(e, conv.id)}
                  title="Delete conversation"
                >
                  ×
                </button>
              </div>
              <div className={styles.conversationTime}>
                {formatDate(conv.last_message_at)}
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
};
