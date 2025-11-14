// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import React from "react";
import { Message } from "./types";
import { MarkdownRenderer } from "./MarkdownRenderer";
import styles from "./ChatMessage.module.scss";

export interface ChatMessageProps {
  message: Message;
}

export const ChatMessage: React.FC<ChatMessageProps> = ({ message }) => {
  const isUser = message.role === "user";

  return (
    <div className={`${styles.message} ${isUser ? styles.user : styles.assistant}`}>
      <div className={styles.messageContent}>
        <div className={styles.messageRole}>
          {isUser ? "You" : "CockroachDB Copilot"}
        </div>
        <div className={styles.messageText}>
          {isUser ? (
            // User messages are plain text
            message.content
          ) : (
            // AI assistant messages support markdown with SQL highlighting
            <MarkdownRenderer content={message.content} />
          )}
        </div>
        <div className={styles.messageTime}>
          {new Date(message.created_at).toLocaleTimeString()}
        </div>
      </div>
    </div>
  );
};
