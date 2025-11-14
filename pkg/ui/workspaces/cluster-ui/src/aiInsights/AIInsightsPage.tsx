// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import React, { useState, useEffect, useRef } from "react";
import { Conversation, Message, StreamEvent } from "./types";
import { fetchConversations, fetchConversation, deleteConversation, streamChat } from "./api";
import { chatStreamManager } from "./chatStreamManager";
import { ConversationList } from "./ConversationList";
import { ChatMessage } from "./ChatMessage";
import { ChatInput } from "./ChatInput";
import { MarkdownRenderer, ProgressiveThinkingStatus } from "./MarkdownRenderer";
import styles from "./AIInsightsPage.module.scss";

// Helper function to create friendly tool usage messages
function getFriendlyToolMessage(toolName: string, description: string): string {
  // Use the description if available (it contains the reasoning summary)
  if (description && description.trim()) {
    return `🔍 ${description}`;
  }

  // Fallback: convert snake_case tool name to friendly message
  const friendlyName = toolName
    .replace(/get_|list_|check_|analyze_/g, "")
    .replace(/_/g, " ");
  return `🔍 Checking ${friendlyName}...`;
}

export const AIInsightsPage: React.FC = () => {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [currentConversation, setCurrentConversation] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const [streamingConversationId, setStreamingConversationId] = useState<string | undefined>(undefined);
  const [streamingMessage, setStreamingMessage] = useState("");
  const [thinkingStatus, setThinkingStatus] = useState<string>("");
  const [error, setError] = useState<string | null>(null);

  // Add render counter to track re-renders
  const renderCount = useRef(0);
  useEffect(() => {
    renderCount.current++;
    console.log(`[RENDER ${renderCount.current}] Component rendered. State:`, {
      isStreaming,
      thinkingStatus,
      streamingMessage: streamingMessage.substring(0, 50),
      messagesCount: messages.length,
      hasActiveStream: chatStreamManager.hasActiveStream(currentConversation?.id)
    });
  });
  const [reasoningEffort, setReasoningEffort] = useState<string>(() => {
    // Load from localStorage
    try {
      return localStorage.getItem("chatcrdb_reasoning_effort") || "low";
    } catch {
      return "low";
    }
  });
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const hasStreamingContentRef = useRef(false);
  const lastThinkingStatusRef = useRef<string>("");

  // TODO: Get actual username from session
  const userName = "admin";

  // Save reasoning effort to localStorage when it changes
  useEffect(() => {
    try {
      localStorage.setItem("chatcrdb_reasoning_effort", reasoningEffort);
    } catch {
      // Ignore errors
    }
  }, [reasoningEffort]);

  // Restore active conversation from localStorage
  useEffect(() => {
    loadConversations();

    // Restore the last active conversation
    try {
      const lastConvId = localStorage.getItem("chatcrdb_active_conversation");
      if (lastConvId) {
        loadConversation(lastConvId);
      }
    } catch {
      // Ignore errors
    }
  }, []);

  // Check for active stream on component mount or when current conversation changes
  useEffect(() => {
    const streamState = chatStreamManager.getStreamState(currentConversation?.id);
    if (streamState && streamState.isActive) {
      // Restore the streaming state
      setIsStreaming(true);
      setStreamingConversationId(currentConversation?.id);
      setStreamingMessage(streamState.streamingContent);
      setThinkingStatus(streamState.thinkingStatus);

      // Add the user message if we have it and messages are empty
      if (streamState.userMessage && messages.length === 0) {
        setMessages([streamState.userMessage]);
      }

      // Re-attach to receive remaining events
      chatStreamManager.attachStream(currentConversation?.id, handleStreamEvent);
    }
  }, [currentConversation?.id]);

  useEffect(() => {
    scrollToBottomIfNeeded();
  }, [messages, streamingMessage]);

  // Cleanup: detach from stream manager when component unmounts
  useEffect(() => {
    return () => {
      if (isStreaming) {
        chatStreamManager.detachStream(currentConversation?.id, handleStreamEvent);
      }
    };
  }, [isStreaming, currentConversation?.id]);

  // Only auto-scroll if user is already at/near the bottom
  const scrollToBottomIfNeeded = () => {
    const container = messagesContainerRef.current;
    if (!container) return;

    const threshold = 100; // pixels from bottom
    const isNearBottom =
      container.scrollHeight - container.scrollTop - container.clientHeight < threshold;

    if (isNearBottom) {
      messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  };

  const loadConversations = async () => {
    try {
      const convs = await fetchConversations(userName);
      setConversations(convs);
    } catch (err) {
      // Don't show error for empty conversations list
      const errorMsg = (err as Error).message;
      if (!errorMsg.includes("Internal Server Error")) {
        setError(`Failed to load conversations: ${errorMsg}`);
      }
    }
  };

  const loadConversation = async (conversationId: string) => {
    try {
      const conv = await fetchConversation(conversationId);
      setCurrentConversation(conv);
      setMessages(conv.messages || []);
      setError(null);

      // Save as active conversation
      try {
        localStorage.setItem("chatcrdb_active_conversation", conversationId);
      } catch {
        // Ignore errors
      }
    } catch (err) {
      setError(`Failed to load conversation: ${(err as Error).message}`);
    }
  };

  const handleNewConversation = () => {
    setCurrentConversation(null);
    setMessages([]);
    setError(null);

    // Clear active conversation
    try {
      localStorage.removeItem("chatcrdb_active_conversation");
    } catch {
      // Ignore errors
    }
  };

  const handleCancelStream = () => {
    if (currentConversation?.id) {
      chatStreamManager.abortStream(currentConversation.id);
    } else {
      chatStreamManager.abortStream(undefined);
    }
    setIsStreaming(false);
    setStreamingConversationId(undefined);
    setStreamingMessage("");
    setThinkingStatus("");
  };

  const handleDeleteConversation = async (conversationId: string) => {
    try {
      await deleteConversation(conversationId);
      await loadConversations();
      if (currentConversation?.id === conversationId) {
        handleNewConversation();
      }
    } catch (err) {
      setError(`Failed to delete conversation: ${(err as Error).message}`);
    }
  };

  const handleSendMessage = async (message: string) => {
    console.log("[SEND MESSAGE] Starting. Initializing state...");
    setIsStreaming(true);
    setStreamingConversationId(currentConversation?.id);
    setStreamingMessage("");
    setThinkingStatus("");  // Clear any previous thinking status
    setError(null);
    hasStreamingContentRef.current = false; // Reset for new message
    lastThinkingStatusRef.current = ""; // Reset thinking status tracking
    console.log("[SEND MESSAGE] State initialized:", {
      isStreaming: true,
      streamingConversationId: currentConversation?.id,
      streamingMessage: "",
      thinkingStatus: "",
      hasStreamingContentRef: false,
      lastThinkingStatusRef: ""
    });

    // Add user message to UI immediately
    const userMessage: Message = {
      id: crypto.randomUUID(),
      role: "user",
      content: message,
      created_at: new Date().toISOString(),
    };
    setMessages((prev) => [...prev, userMessage]);

    // Scroll to bottom immediately so user can see the thinking indicator
    setTimeout(() => {
      messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }, 0);

    // Start the stream - streamChat will handle attachment to manager
    streamChat({
      conversation_id: currentConversation?.id,
      message,
      user_name: userName,
      reasoning_effort: reasoningEffort,
      userMessage,  // Pass user message for persistence
    }, handleStreamEvent)
      .then(() => {
        // Stream completed successfully - ensure we're not stuck in streaming state
        // The "done" event should have already handled this, but just in case
        setIsStreaming(false);
      })
      .catch((err) => {
        setError(`Chat error: ${(err as Error).message}`);
        setIsStreaming(false);
      });
  };

  const handleStreamEvent = (event: StreamEvent) => {
    console.log("Received event:", event, "hasContent:", hasStreamingContentRef.current); // Debug logging
    switch (event.type) {
      case "conversation_created":
        // New conversation created, update current conversation ID and streaming conversation ID
        if (event.conversation_id) {
          setCurrentConversation((prev) => ({
            ...prev,
            id: event.conversation_id,
            user_name: userName,
            cluster_id: "", // Will be set by backend
            created_at: new Date().toISOString(),
            last_message_at: new Date().toISOString(),
          }));
          setStreamingConversationId(event.conversation_id);
          loadConversations(); // Refresh conversation list
        }
        break;

      case "token":
        // Append token to streaming message
        console.log("Received token:", event.content, "current hasContent:", hasStreamingContentRef.current);
        if (event.content) {
          setStreamingMessage((prev) => {
            const newContent = prev + event.content;
            // Mark that we have content and clear thinking status immediately
            // to prevent showing the same content twice (reasoning summary + actual response)
            if (newContent.trim()) {
              console.log("Setting hasStreamingContentRef to true and clearing thinking status");
              hasStreamingContentRef.current = true;
              setThinkingStatus("");
              lastThinkingStatusRef.current = "";  // Also clear the last status ref
            }
            return newContent;
          });
        }
        break;

      case "tool_use":
        // Show friendly tool usage status (only if we haven't started streaming content)
        console.log(`[TOOL_USE EVENT] tool: ${event.tool_name}, hasContent:`, hasStreamingContentRef.current);
        if (!hasStreamingContentRef.current) {
          const friendlyMessage = getFriendlyToolMessage(event.tool_name || "", event.tool_description || "");
          // Only update if the message is different to avoid re-rendering
          if (friendlyMessage !== lastThinkingStatusRef.current) {
            console.log("[TOOL_USE EVENT] Calling setThinkingStatus with:", friendlyMessage);
            lastThinkingStatusRef.current = friendlyMessage;
            setThinkingStatus((prev) => {
              console.log("[TOOL_USE EVENT] setThinkingStatus setter called. prev:", prev, "new:", friendlyMessage);
              return friendlyMessage;
            });
          } else {
            console.log("[TOOL_USE EVENT] Skipping - same as current");
          }
        } else {
          console.log("[TOOL_USE EVENT] Skipping - already have content");
        }
        break;

      case "thinking":
        // Only update if no streaming content yet AND we don't have a detailed status already
        console.log("[THINKING EVENT] hasContent:", hasStreamingContentRef.current, "lastStatus:", lastThinkingStatusRef.current);
        if (!hasStreamingContentRef.current && !lastThinkingStatusRef.current) {
          console.log("[THINKING EVENT] Calling setThinkingStatus('💭 Thinking...')");
          lastThinkingStatusRef.current = "💭 Thinking...";
          setThinkingStatus((prev) => {
            console.log("[THINKING EVENT] setThinkingStatus setter called. prev:", prev, "new:", "💭 Thinking...");
            return "💭 Thinking...";
          });
        } else {
          console.log("[THINKING EVENT] Skipping - already have content or detailed status");
        }
        break;

      case "analyzing":
        // Only update if no streaming content yet AND we don't have a detailed status already
        console.log("Analyzing event, hasContent:", hasStreamingContentRef.current, "lastStatus:", lastThinkingStatusRef.current);
        if (!hasStreamingContentRef.current && !lastThinkingStatusRef.current) {
          console.log("Setting thinking status to: Analyzing...");
          lastThinkingStatusRef.current = "🔬 Analyzing results...";
          setThinkingStatus("🔬 Analyzing results...");
        } else {
          console.log("Skipping analyzing status - already have content or detailed status");
        }
        break;

      case "progress":
        // Show progress message for long-running tools (only if no content yet AND no detailed status)
        console.log("[PROGRESS EVENT] content:", event.content, "hasContent:", hasStreamingContentRef.current, "lastStatus:", lastThinkingStatusRef.current);
        if (event.content && !hasStreamingContentRef.current && !lastThinkingStatusRef.current) {
          console.log("[PROGRESS EVENT] Setting thinking status to:", event.content);
          const progressMsg = `⏳ ${event.content}`;
          lastThinkingStatusRef.current = progressMsg;
          setThinkingStatus(progressMsg);
        } else {
          console.log("[PROGRESS EVENT] Skipping - already have content or detailed status");
        }
        break;

      case "done":
        // Finalize the assistant message using the functional form to ensure we get the latest state
        setStreamingMessage((currentContent) => {
          if (currentContent.trim()) {
            const assistantMessage: Message = {
              id: crypto.randomUUID(),
              role: "assistant",
              content: currentContent,
              created_at: new Date().toISOString(),
            };
            // Only add if the last message isn't already an assistant message with the same content
            // This prevents duplicates if the "done" event fires twice
            setMessages((prev) => {
              const lastMsg = prev[prev.length - 1];
              if (lastMsg && lastMsg.role === "assistant" && lastMsg.content === currentContent) {
                console.log("Skipping duplicate assistant message");
                return prev; // Don't add duplicate
              }
              return [...prev, assistantMessage];
            });
          }
          return ""; // Clear the streaming message
        });
        setThinkingStatus(""); // Clear thinking status
        setIsStreaming(false);
        setStreamingConversationId(undefined);
        loadConversations(); // Refresh conversation list with updated timestamp (but don't reload current conversation to avoid duplicates)
        break;

      case "error":
        setError(event.error || "Unknown error occurred");
        setStreamingMessage("");
        setIsStreaming(false);
        setStreamingConversationId(undefined);
        break;
    }
  };

  return (
    <div className={styles.container}>
      <div className={styles.sidebar}>
        <ConversationList
          conversations={conversations}
          currentConversationId={currentConversation?.id}
          onSelectConversation={loadConversation}
          onNewConversation={handleNewConversation}
          onDeleteConversation={handleDeleteConversation}
          isStreaming={isStreaming}
        />
      </div>
      <div className={styles.main}>
        <div className={styles.header}>
          <div className={styles.headerLeft}>
            <h1>CockroachDB Copilot</h1>
            <div className={styles.disclaimer}>AI can make mistakes. Double check all findings before taking subsequent action.</div>
          </div>
          <div className={styles.headerRight}>
            <label className={styles.reasoningControl}>
              <span className={styles.reasoningLabel}>Reasoning:</span>
              <select
                className={styles.reasoningSelect}
                value={reasoningEffort}
                onChange={(e) => setReasoningEffort(e.target.value)}
                disabled={isStreaming}
              >
                <option value="low">Fast</option>
                <option value="medium">Balanced</option>
                <option value="high">Thorough</option>
              </select>
            </label>
          </div>
        </div>
        {error && <div className={styles.error}>{error}</div>}
        <div ref={messagesContainerRef} className={styles.messagesContainer}>
          {messages.map((msg) => (
            <ChatMessage key={msg.id} message={msg} />
          ))}
          {isStreaming &&
           ((streamingConversationId !== undefined && streamingConversationId === currentConversation?.id) ||
            (streamingConversationId === undefined && currentConversation === null && streamingMessage.trim())) &&
           thinkingStatus && !streamingMessage.trim() && (
            <div className={styles.thinkingStatus}>
              <div className={styles.thinkingIndicator}>
                <span className={styles.spinner}></span>
                <ProgressiveThinkingStatus content={thinkingStatus} />
              </div>
            </div>
          )}
          {isStreaming &&
           ((streamingConversationId !== undefined && streamingConversationId === currentConversation?.id) ||
            (streamingConversationId === undefined && currentConversation === null && streamingMessage.trim())) &&
           streamingMessage.trim() && (
            <ChatMessage
              message={{
                id: "streaming",
                role: "assistant",
                content: streamingMessage,
                created_at: new Date().toISOString(),
              }}
            />
          )}
          <div ref={messagesEndRef} />
        </div>
        <div className={styles.inputContainer}>
          <ChatInput
            onSendMessage={handleSendMessage}
            onCancel={handleCancelStream}
            disabled={false}
            isStreaming={isStreaming}
          />
        </div>
      </div>
    </div>
  );
};
