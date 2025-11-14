// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import { Conversation, ChatRequest, StreamEvent } from "./types";
import { chatStreamManager } from "./chatStreamManager";

const API_BASE = "/api/v2/ai";

export async function fetchConversations(userName: string): Promise<Conversation[]> {
  const response = await fetch(`${API_BASE}/conversations?user_name=${encodeURIComponent(userName)}`);
  if (!response.ok) {
    throw new Error(`Failed to fetch conversations: ${response.statusText}`);
  }
  return response.json();
}

export async function fetchConversation(conversationId: string): Promise<Conversation> {
  const response = await fetch(`${API_BASE}/conversation?id=${encodeURIComponent(conversationId)}`);
  if (!response.ok) {
    throw new Error(`Failed to fetch conversation: ${response.statusText}`);
  }
  return response.json();
}

export async function deleteConversation(conversationId: string): Promise<void> {
  const response = await fetch(`${API_BASE}/conversation/delete?id=${encodeURIComponent(conversationId)}`, {
    method: "DELETE",
  });
  if (!response.ok) {
    throw new Error(`Failed to delete conversation: ${response.statusText}`);
  }
}

export function streamChat(request: ChatRequest, onEvent: (event: StreamEvent) => void): Promise<void> {
  // Check if there's already an active stream for this conversation
  if (chatStreamManager.hasActiveStream(request.conversation_id)) {
    // Stream already exists, just attach callback and wait for it to complete
    chatStreamManager.attachStream(request.conversation_id, onEvent);
    return Promise.resolve();
  }

  // Attach to stream manager for new stream
  chatStreamManager.attachStream(request.conversation_id, onEvent, request.userMessage);

  return new Promise(async (resolve, reject) => {
    try {
      const abortSignal = chatStreamManager.getAbortSignal(request.conversation_id);

      const response = await fetch(`${API_BASE}/chat/stream`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(request),
        signal: abortSignal,
      });

      if (!response.ok) {
        // Try to read the error message from the response body
        const errorText = await response.text();
        const error = new Error(errorText || `Chat request failed: ${response.statusText}`);
        chatStreamManager.emitEvent(request.conversation_id, {
          type: "error",
          error: error.message,
        });
        reject(error);
        return;
      }

      const reader = response.body?.getReader();
      if (!reader) {
        const error = new Error("Response body is not readable");
        chatStreamManager.emitEvent(request.conversation_id, {
          type: "error",
          error: error.message,
        });
        reject(error);
        return;
      }

      const decoder = new TextDecoder();
      let buffer = "";

      const processStream = async () => {
        while (true) {
          const { done, value } = await reader.read();
          if (done) {
            resolve();
            break;
          }

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop() || ""; // Keep incomplete line in buffer

          for (const line of lines) {
            if (line.startsWith("data: ")) {
              const data = line.slice(6); // Remove "data: " prefix
              try {
                const event: StreamEvent = JSON.parse(data);
                // Emit through manager instead of calling directly
                chatStreamManager.emitEvent(request.conversation_id, event);
              } catch (e) {
                console.error("Failed to parse SSE event:", e, data);
              }
            }
          }
        }
      };

      await processStream();
    } catch (err) {
      // Check if this was an abort
      if (err instanceof Error && err.name === "AbortError") {
        resolve(); // Graceful abort, not an error
      } else {
        chatStreamManager.emitEvent(request.conversation_id, {
          type: "error",
          error: err instanceof Error ? err.message : String(err),
        });
        reject(err);
      }
    }
  });
}
