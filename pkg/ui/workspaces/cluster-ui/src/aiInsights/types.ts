// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

export interface Message {
  id: string;
  role: "user" | "assistant" | "tool";
  content: string;
  created_at: string;
  token_count?: number;
}

export interface Conversation {
  id: string;
  user_name: string;
  cluster_id: string;
  created_at: string;
  last_message_at: string;
  title?: string;
  messages?: Message[];
}

export interface StreamEvent {
  type: "token" | "tool_use" | "thinking" | "analyzing" | "progress" | "conversation_created" | "done" | "error";
  content?: string;
  tool_name?: string;
  tool_description?: string;
  tool_arguments?: string;
  conversation_id?: string;
  error?: string;
}

export interface ChatRequest {
  conversation_id?: string;
  message: string;
  user_name: string;
  reasoning_effort?: string;  // "low", "medium", or "high"
  userMessage?: Message;  // For stream persistence
}
