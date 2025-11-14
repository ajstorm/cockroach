// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import React, { useState, useEffect, KeyboardEvent, useRef } from "react";
import { Button } from "../button";
import styles from "./ChatInput.module.scss";

const DRAFT_MESSAGE_KEY = "chatcrdb_draft_message";
const PROMPT_HISTORY_KEY = "chatcrdb_prompt_history";
const MAX_HISTORY_SIZE = 50;

// Seed questions for new users or to supplement history
const SEED_QUESTIONS = [
  "What is the state of my cluster?",
  "How many nodes are running?",
  "What version of CockroachDB is this cluster running?",
  "Are there any unavailable ranges?",
  "Show me slow queries",
  "What is the current cluster health?",
  "What tables exist in my database?",
  "What are the most active queries?",
  "Is there any data skew in my cluster?",
  "What is the replication status?",
  "Show me recent errors",
  "What is the CPU usage?",
  "What is the memory usage?",
  "Are there any under-replicated ranges?",
  "What is the cluster topology?",
  "Show me the largest tables by size",
  "Which queries are using the most memory?",
  "Are there any long-running transactions?",
  "What is the disk usage across nodes?",
  "Show me connection statistics",
  "Are there any failed replication jobs?",
  "What indexes should I add to improve performance?",
  "Show me query contention issues",
  "What is the write throughput?",
  "What is the read throughput?",
];

// Fuzzy match scoring - returns a score between 0 and 1
function fuzzyMatch(pattern: string, text: string): number {
  if (!pattern) return 0;
  if (pattern === text) return 1;

  const patternLower = pattern.toLowerCase();
  const textLower = text.toLowerCase();

  // Exact substring match gets high score
  if (textLower.includes(patternLower)) {
    // Boost score if match is at the beginning
    const index = textLower.indexOf(patternLower);
    const positionBonus = index === 0 ? 0.3 : 0;
    return 0.7 + positionBonus;
  }

  // Character-by-character fuzzy match
  let patternIdx = 0;
  let score = 0;
  let consecutiveMatches = 0;

  for (let i = 0; i < textLower.length && patternIdx < patternLower.length; i++) {
    if (textLower[i] === patternLower[patternIdx]) {
      score += 1 + consecutiveMatches * 0.5; // Bonus for consecutive matches
      consecutiveMatches++;
      patternIdx++;
    } else {
      consecutiveMatches = 0;
    }
  }

  // Only consider it a match if all pattern characters were found
  if (patternIdx !== patternLower.length) return 0;

  // Normalize score by pattern length
  return Math.min(1, score / (patternLower.length * 2));
}

// Get smart suggestions based on current input
function getSuggestions(input: string, history: string[], maxSuggestions = 5): string[] {
  const trimmedInput = input.trim();

  // Don't show suggestions for very short input or if input is too long
  if (trimmedInput.length < 2 || trimmedInput.length > 100) {
    return [];
  }

  // Combine history and seed questions, prioritizing history
  // History items get a small score boost to prefer user's own patterns
  const historyScored = history.map(item => ({
    text: item,
    score: fuzzyMatch(trimmedInput, item) * 1.2, // 20% boost for history
    source: 'history' as const
  }));

  const seedScored = SEED_QUESTIONS.map(item => ({
    text: item,
    score: fuzzyMatch(trimmedInput, item),
    source: 'seed' as const
  }));

  // Combine, deduplicate, filter, and sort
  const allScored = [...historyScored, ...seedScored];
  const seen = new Set<string>();
  const unique = allScored.filter(item => {
    const lower = item.text.toLowerCase();
    if (seen.has(lower)) return false;
    seen.add(lower);
    return true;
  });

  const filtered = unique
    .filter(item => item.score > 0.3) // Minimum threshold
    .sort((a, b) => b.score - a.score); // Sort by score descending

  return filtered.slice(0, maxSuggestions).map(item => item.text);
}

export interface ChatInputProps {
  onSendMessage: (message: string) => void;
  onCancel?: () => void;
  disabled?: boolean;
  isStreaming?: boolean;
}

export const ChatInput: React.FC<ChatInputProps> = ({
  onSendMessage,
  onCancel,
  disabled,
  isStreaming = false,
}) => {
  // Initialize from localStorage if available
  const [message, setMessage] = useState(() => {
    try {
      return localStorage.getItem(DRAFT_MESSAGE_KEY) || "";
    } catch {
      return "";
    }
  });

  // Load prompt history from localStorage
  const [promptHistory, setPromptHistory] = useState<string[]>(() => {
    try {
      const stored = localStorage.getItem(PROMPT_HISTORY_KEY);
      return stored ? JSON.parse(stored) : [];
    } catch {
      return [];
    }
  });

  const [historyIndex, setHistoryIndex] = useState(-1);
  const [tempMessage, setTempMessage] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Autocomplete state
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [selectedSuggestionIndex, setSelectedSuggestionIndex] = useState(-1);
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [inlineCompletion, setInlineCompletion] = useState("");
  const suggestionsRef = useRef<HTMLDivElement>(null);

  // Save to localStorage whenever message changes
  useEffect(() => {
    try {
      if (message) {
        localStorage.setItem(DRAFT_MESSAGE_KEY, message);
      } else {
        localStorage.removeItem(DRAFT_MESSAGE_KEY);
      }
    } catch {
      // Ignore localStorage errors (e.g., in private browsing mode)
    }
  }, [message]);

  // Update suggestions and inline completion when message changes
  useEffect(() => {
    // Don't show suggestions when navigating history
    if (historyIndex !== -1) {
      setShowSuggestions(false);
      setInlineCompletion("");
      return;
    }

    const trimmedMessage = message.trim();
    const newSuggestions = getSuggestions(message, promptHistory);
    setSuggestions(newSuggestions);
    setShowSuggestions(newSuggestions.length > 0);
    setSelectedSuggestionIndex(-1);

    // Set inline completion for the best match
    if (newSuggestions.length > 0 && trimmedMessage.length >= 2) {
      const bestMatch = newSuggestions[0];
      const lowerMessage = trimmedMessage.toLowerCase();
      const lowerMatch = bestMatch.toLowerCase();

      // Only show inline completion if the suggestion starts with what user typed
      if (lowerMatch.startsWith(lowerMessage)) {
        // Extract the completion part (what remains after the typed text)
        const completion = bestMatch.substring(trimmedMessage.length);
        setInlineCompletion(completion);
      } else {
        setInlineCompletion("");
      }
    } else {
      setInlineCompletion("");
    }
  }, [message, promptHistory, historyIndex]);

  // Close suggestions when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        suggestionsRef.current &&
        !suggestionsRef.current.contains(event.target as Node) &&
        textareaRef.current &&
        !textareaRef.current.contains(event.target as Node)
      ) {
        setShowSuggestions(false);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  // Add message to history
  const addToHistory = (msg: string) => {
    const newHistory = [msg, ...promptHistory.filter(m => m !== msg)].slice(0, MAX_HISTORY_SIZE);
    setPromptHistory(newHistory);
    try {
      localStorage.setItem(PROMPT_HISTORY_KEY, JSON.stringify(newHistory));
    } catch {
      // Ignore errors
    }
  };

  const handleSend = () => {
    const trimmed = message.trim();
    if (trimmed && !disabled) {
      addToHistory(trimmed);
      onSendMessage(trimmed);
      setMessage("");
      setHistoryIndex(-1);
      setTempMessage("");
      // Clear localStorage when message is sent
      try {
        localStorage.removeItem(DRAFT_MESSAGE_KEY);
      } catch {
        // Ignore errors
      }
    }
  };

  const handleCancel = () => {
    if (onCancel) {
      onCancel();
    }
  };

  const applySuggestion = (suggestion: string) => {
    setMessage(suggestion);
    setShowSuggestions(false);
    setSelectedSuggestionIndex(-1);
    setInlineCompletion("");
    textareaRef.current?.focus();
  };

  const acceptInlineCompletion = () => {
    if (inlineCompletion) {
      setMessage(message + inlineCompletion);
      setInlineCompletion("");
      setShowSuggestions(false);
      return true;
    }
    return false;
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // Handle Tab for inline completion or dropdown suggestion
    if (e.key === "Tab") {
      // First priority: inline completion if visible and no dropdown selection
      if (inlineCompletion && selectedSuggestionIndex === -1) {
        e.preventDefault();
        acceptInlineCompletion();
        return;
      }
      // Second priority: selected dropdown item
      if (showSuggestions && suggestions.length > 0) {
        e.preventDefault();
        const index = selectedSuggestionIndex === -1 ? 0 : selectedSuggestionIndex;
        applySuggestion(suggestions[index]);
        return;
      }
    }

    // Handle Enter
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      // If a suggestion is selected, apply it
      if (showSuggestions && selectedSuggestionIndex >= 0) {
        applySuggestion(suggestions[selectedSuggestionIndex]);
      } else {
        handleSend();
      }
      return;
    }

    // Handle Escape to close suggestions
    if (e.key === "Escape" && showSuggestions) {
      e.preventDefault();
      setShowSuggestions(false);
      setSelectedSuggestionIndex(-1);
      return;
    }

    // Navigate through suggestions with up/down arrows when suggestions are visible
    if (showSuggestions && suggestions.length > 0) {
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedSuggestionIndex(prev =>
          prev <= 0 ? suggestions.length - 1 : prev - 1
        );
        return;
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedSuggestionIndex(prev =>
          prev >= suggestions.length - 1 ? 0 : prev + 1
        );
        return;
      }
    }

    // Navigate through prompt history with up/down arrows (when no suggestions)
    if (!showSuggestions) {
      if (e.key === "ArrowUp") {
        e.preventDefault();
        if (promptHistory.length === 0) return;

        if (historyIndex === -1) {
          // First time pressing up - save current message
          setTempMessage(message);
          setHistoryIndex(0);
          setMessage(promptHistory[0]);
        } else if (historyIndex < promptHistory.length - 1) {
          // Navigate to older message
          const newIndex = historyIndex + 1;
          setHistoryIndex(newIndex);
          setMessage(promptHistory[newIndex]);
        }
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        if (historyIndex === -1) return;

        if (historyIndex === 0) {
          // Back to the original message
          setHistoryIndex(-1);
          setMessage(tempMessage);
          setTempMessage("");
        } else {
          // Navigate to newer message
          const newIndex = historyIndex - 1;
          setHistoryIndex(newIndex);
          setMessage(promptHistory[newIndex]);
        }
      }
    }
  };

  return (
    <div className={styles.chatInput}>
      <div className={styles.inputWrapper}>
        <div className={styles.textareaContainer}>
          <textarea
            ref={textareaRef}
            className={styles.textarea}
            value={message}
            onChange={(e) => {
              setMessage(e.target.value);
              // Reset history index when user types
              if (historyIndex !== -1) {
                setHistoryIndex(-1);
                setTempMessage("");
              }
            }}
            onKeyDown={handleKeyDown}
            placeholder="Ask about your cluster..."
            disabled={disabled}
            rows={3}
          />
          {inlineCompletion && (
            <div className={styles.ghostText} aria-hidden="true">
              <span className={styles.ghostTextInvisible}>{message}</span>
              <span className={styles.ghostTextVisible}>{inlineCompletion}</span>
            </div>
          )}
        </div>
        {showSuggestions && suggestions.length > 0 && (
          <div ref={suggestionsRef} className={styles.suggestionsDropdown}>
            {suggestions.map((suggestion, index) => (
              <div
                key={index}
                className={`${styles.suggestionItem} ${
                  index === selectedSuggestionIndex ? styles.selected : ""
                }`}
                onClick={() => applySuggestion(suggestion)}
                onMouseEnter={() => setSelectedSuggestionIndex(index)}
              >
                {suggestion}
              </div>
            ))}
          </div>
        )}
      </div>
      {isStreaming ? (
        <Button
          type="secondary"
          onClick={handleCancel}
        >
          Cancel
        </Button>
      ) : (
        <Button
          type="primary"
          onClick={handleSend}
          disabled={disabled || !message.trim()}
        >
          Send
        </Button>
      )}
    </div>
  );
};
