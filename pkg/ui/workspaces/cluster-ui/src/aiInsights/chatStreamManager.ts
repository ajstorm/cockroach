// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import { StreamEvent, Message } from "./types";

type StreamCallback = (event: StreamEvent) => void;

interface ActiveStream {
  conversationId: string | undefined;
  abortController: AbortController;
  callbacks: Set<StreamCallback>;
  queuedEvents: StreamEvent[];
  // Store UI state for when component remounts
  userMessage: Message | null;
  streamingContent: string;
  thinkingStatus: string;
}

/**
 * Singleton service that manages chat streaming connections.
 * This ensures streams continue even when navigating away from the chat page.
 */
class ChatStreamManager {
  private activeStreams: Map<string, ActiveStream> = new Map();
  private streamIdCounter = 0;

  /**
   * Start a new stream or attach to an existing one.
   * Returns a stream ID that can be used to detach later.
   */
  attachStream(
    conversationId: string | undefined,
    callback: StreamCallback,
    userMessage?: Message,
  ): string {
    const streamId = `stream_${this.streamIdCounter++}`;
    const key = conversationId || "__new__";

    const existing = this.activeStreams.get(key);
    if (existing) {
      // Attach to existing stream
      existing.callbacks.add(callback);

      // Replay any events that arrived while component was unmounted
      existing.queuedEvents.forEach((event) => callback(event));
      existing.queuedEvents = [];

      return streamId;
    }

    // Create new stream tracking
    const stream: ActiveStream = {
      conversationId,
      abortController: new AbortController(),
      callbacks: new Set([callback]),
      queuedEvents: [],
      userMessage: userMessage || null,
      streamingContent: "",
      thinkingStatus: "",
    };

    this.activeStreams.set(key, stream);
    return streamId;
  }

  /**
   * Get the persisted state for an active stream (for component remount).
   */
  getStreamState(conversationId: string | undefined): {
    userMessage: Message | null;
    streamingContent: string;
    thinkingStatus: string;
    isActive: boolean;
  } | null {
    const key = conversationId || "__new__";
    const stream = this.activeStreams.get(key);

    if (!stream) return null;

    return {
      userMessage: stream.userMessage,
      streamingContent: stream.streamingContent,
      thinkingStatus: stream.thinkingStatus,
      isActive: true,
    };
  }

  /**
   * Detach a callback from a stream.
   * If this is the last callback, the stream will continue but queue messages.
   */
  detachStream(conversationId: string | undefined, callback: StreamCallback) {
    const key = conversationId || "__new__";
    const stream = this.activeStreams.get(key);

    if (stream) {
      stream.callbacks.delete(callback);
      // Don't abort the stream even if no callbacks remain - let it complete
    }
  }

  /**
   * Emit an event to all attached callbacks, or queue it if none are attached.
   */
  emitEvent(conversationId: string | undefined, event: StreamEvent) {
    const key = conversationId || "__new__";
    const stream = this.activeStreams.get(key);

    if (!stream) return;

    // Update persisted state based on event type
    switch (event.type) {
      case "token":
        if (event.content) {
          stream.streamingContent += event.content;
        }
        break;
      case "thinking":
        stream.thinkingStatus = "💭 Thinking...";
        break;
      case "analyzing":
        stream.thinkingStatus = "🔬 Analyzing results...";
        break;
      case "tool_use":
        stream.thinkingStatus = `🔍 ${event.tool_description || "Using tool"}...`;
        break;
      case "progress":
        if (event.content) {
          stream.thinkingStatus = `⏳ ${event.content}`;
        }
        break;
      case "done":
      case "error":
        stream.thinkingStatus = "";
        break;
    }

    if (stream.callbacks.size > 0) {
      // Send to all active callbacks
      stream.callbacks.forEach((callback) => {
        try {
          callback(event);
        } catch (err) {
          console.error("Error in stream callback:", err);
        }
      });
    } else {
      // Queue for when component remounts
      stream.queuedEvents.push(event);
    }

    // Clean up completed streams
    if (event.type === "done" || event.type === "error") {
      this.activeStreams.delete(key);
    }
  }

  /**
   * Get the abort signal for a conversation's stream.
   */
  getAbortSignal(conversationId: string | undefined): AbortSignal | undefined {
    const key = conversationId || "__new__";
    return this.activeStreams.get(key)?.abortController.signal;
  }

  /**
   * Abort a specific stream.
   */
  abortStream(conversationId: string | undefined) {
    const key = conversationId || "__new__";
    const stream = this.activeStreams.get(key);

    if (stream) {
      stream.abortController.abort();
      this.activeStreams.delete(key);
    }
  }

  /**
   * Check if a stream is currently active for a conversation.
   */
  hasActiveStream(conversationId: string | undefined): boolean {
    const key = conversationId || "__new__";
    return this.activeStreams.has(key);
  }
}

// Export singleton instance
export const chatStreamManager = new ChatStreamManager();
