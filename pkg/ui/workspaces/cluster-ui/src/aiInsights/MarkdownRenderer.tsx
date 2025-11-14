// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

// TODO(ai-insights): Replace this custom markdown parser with a standard library
// like react-markdown once Bazel's npm_translate_lock supports pnpm lockfile v9+.
// Currently blocked because Bazel only supports lockfile v6.1 but pnpm v10 generates v9.
// Consider react-markdown + remark-gfm + rehype-highlight as replacements.

import React, { useState, useEffect } from "react";
import { SqlBox, SqlBoxSize } from "../sql/box";
import styles from "./MarkdownRenderer.module.scss";

export interface MarkdownRendererProps {
  content: string;
}

export interface ProgressiveThinkingStatusProps {
  content: string;
}

// Detect if a line looks like SQL (even if it has list markers)
function looksLikeSQL(line: string): boolean {
  // Remove common list markers before checking
  const cleaned = line.trim().replace(/^[•\-*]\s+/, '').toUpperCase();

  // SQL keywords that are very specific and unlikely to be prose
  const definiteSQLKeywords = [
    'SELECT ', 'INSERT ', 'UPDATE ', 'DELETE ', 'CREATE ', 'ALTER ', 'DROP ',
    'SHOW ', 'EXPLAIN ', 'GRANT ', 'REVOKE ', 'BEGIN ', 'COMMIT ',
    'ROLLBACK ', 'CONFIGURE ZONE'
  ];

  // Check for definite SQL keywords (with space after to avoid false matches in prose)
  if (definiteSQLKeywords.some(keyword => cleaned.startsWith(keyword))) {
    return true;
  }

  // Special case: WITH is only SQL if followed by typical CTE patterns
  if (cleaned.startsWith('WITH ')) {
    // Check if it looks like a CTE: "WITH table_name AS (" or "WITH RECURSIVE"
    return /^WITH\s+(\w+\s+AS\s*\(|RECURSIVE\s+)/.test(cleaned);
  }

  // Special case: SET is only SQL if followed by typical SQL patterns
  if (cleaned.startsWith('SET ')) {
    // Check if it looks like a SQL SET statement (has = or TO)
    return /^SET\s+\w+\s*(=|TO)\s*/.test(cleaned);
  }

  return false;
}

interface SqlBlockProps {
  content: string;
}

const SqlBlock: React.FC<SqlBlockProps> = ({ content }) => {
  const [copied, setCopied] = useState(false);

  // Normalize indentation by removing common leading whitespace
  const normalizeSQL = (sql: string): string => {
    const lines = sql.split('\n');

    // Find minimum indentation (excluding empty lines)
    const indents = lines
      .filter(line => line.trim().length > 0)
      .map(line => line.match(/^ */)?.[0].length || 0);

    if (indents.length === 0) return sql;

    const minIndent = Math.min(...indents);

    // Remove the common indentation from all lines
    return lines
      .map(line => line.substring(minIndent))
      .join('\n')
      .trim();
  };

  const normalizedContent = normalizeSQL(content);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(normalizedContent);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error("Failed to copy:", err);
    }
  };

  return (
    <div className={styles.sqlBlock}>
      <div className={styles.codeHeader}>
        <span className={styles.language}>SQL</span>
        <button
          className={styles.copyButton}
          onClick={handleCopy}
          aria-label="Copy SQL"
        >
          {copied ? "✓ Copied" : "Copy"}
        </button>
      </div>
      <SqlBox value={normalizedContent} size={SqlBoxSize.CUSTOM} format={true} />
    </div>
  );
};

// Format text with basic inline markdown
function formatText(text: string): React.ReactNode[] {
  const parts: React.ReactNode[] = [];
  let lastIndex = 0;

  // Match **bold** text
  const boldRegex = /\*\*(.*?)\*\*/g;
  let match;

  while ((match = boldRegex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      parts.push(text.substring(lastIndex, match.index));
    }
    parts.push(<strong key={match.index}>{match[1]}</strong>);
    lastIndex = match.index + match[0].length;
  }

  if (lastIndex < text.length) {
    parts.push(text.substring(lastIndex));
  }

  return parts.length > 0 ? parts : [text];
}

// Render nested list structure based on indentation
function renderNestedList(
  items: Array<{text: string, indent: number}>,
  keyBase: number,
  isNumbered: boolean
): React.ReactNode {
  if (items.length === 0) return null;

  const ListTag = isNumbered ? 'ol' : 'ul';
  const className = isNumbered ? styles.numberedList : styles.bulletList;

  // Group items by indentation level
  const result: React.ReactNode[] = [];
  let i = 0;

  while (i < items.length) {
    const currentIndent = items[i].indent;
    const itemContent: React.ReactNode[] = [formatText(items[i].text)];
    let j = i + 1;

    // Check if there are nested items (higher indent)
    const nestedItems: Array<{text: string, indent: number}> = [];
    while (j < items.length && items[j].indent > currentIndent) {
      nestedItems.push(items[j]);
      j++;
    }

    // If we found nested items, render them as a nested list
    if (nestedItems.length > 0) {
      itemContent.push(
        renderNestedList(nestedItems, keyBase + i + 1000, isNumbered)
      );
    }

    result.push(
      <li key={i}>
        {itemContent}
      </li>
    );

    i = j;
  }

  return (
    <ListTag key={keyBase} className={className}>
      {result}
    </ListTag>
  );
}

export const MarkdownRenderer: React.FC<MarkdownRendererProps> = ({ content }) => {
  // Preprocess content to fix missing line breaks in AI reasoning summaries

  // Pattern 1: Split bold phrases that appear at end of lines after punctuation
  // "sentence.**Bold Heading**" -> "sentence.\n\n**Bold Heading**"
  let preprocessed = content.replace(/([.!?])(\*\*[^*]+\*\*)/g, '$1\n\n$2');

  // Pattern 2: "sentence.TitleCaseHeading" -> "sentence.\n\n**TitleCaseHeading**\n\n"
  // Detects when a sentence ends and immediately starts a title-case phrase (2-6 words)
  preprocessed = preprocessed.replace(/\.([A-Z][a-z]+(?:\s+[a-z]+){1,5})(?=\n|$)/g, '.\n\n**$1**\n\n');

  // Pattern 3: Add breaks after sentences that end mid-line without proper spacing
  // This helps break up long reasoning blocks into paragraphs
  preprocessed = preprocessed.replace(/([.!?])\s+([A-Z])/g, (match, punct, capital) => {
    // Check if the capital letter starts a title-case phrase (potential heading)
    const restOfLine = preprocessed.substring(preprocessed.indexOf(match) + match.length);
    const nextWords = restOfLine.match(/^[A-Z][a-z]+(?:\s+[a-z]+){1,4}/);
    if (nextWords) {
      return `${punct}\n\n**${capital}`;
    }
    return match;
  });

  const lines = preprocessed.split('\n');
  const elements: React.ReactNode[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();

    // Check for headings (with # markers)
    if (trimmed.match(/^#{1,6}\s+/)) {
      const level = (trimmed.match(/^(#{1,6})/)?.[1].length || 1);
      const text = trimmed.replace(/^#{1,6}\s+/, '');
      const HeadingTag = `h${Math.min(level, 6)}` as keyof JSX.IntrinsicElements;
      elements.push(
        <HeadingTag key={i} className={styles.heading}>
          {formatText(text)}
        </HeadingTag>
      );
      i++;
      continue;
    }

    // Check for bold-only lines that should be headings
    // Pattern: **text** with nothing else on the line
    const boldOnlyMatch = trimmed.match(/^\*\*(.+?)\*\*$/);
    if (boldOnlyMatch) {
      elements.push(
        <h3 key={i} className={styles.heading}>
          {boldOnlyMatch[1]}
        </h3>
      );
      i++;
      continue;
    }

    // Check for blockquotes
    if (trimmed.match(/^>\s+/)) {
      const quoteLines: string[] = [];
      const quoteStart = i;

      // Collect all consecutive blockquote lines
      while (i < lines.length) {
        const currentLine = lines[i];
        const quoteTrimmed = currentLine.trim();
        if (quoteTrimmed.match(/^>\s+/)) {
          quoteLines.push(quoteTrimmed.replace(/^>\s+/, ''));
          i++;
        } else if (quoteTrimmed === '') {
          // Allow empty lines within blockquote
          i++;
        } else {
          break;
        }
      }

      elements.push(
        <blockquote key={quoteStart} className={styles.blockquote}>
          {quoteLines.map((line, idx) => (
            <div key={idx}>{formatText(line)}</div>
          ))}
        </blockquote>
      );
      continue;
    }

    // Check for bullet lists
    if (trimmed.match(/^[-*]\s+/)) {
      const listStart = i;
      const listItems: Array<{text: string, indent: number}> = [];

      // Collect all consecutive list items with their indentation levels
      while (i < lines.length) {
        const currentLine = lines[i];
        const indentMatch = currentLine.match(/^(\s*)([-*])\s+(.+)$/);
        if (!indentMatch) break;

        const indent = indentMatch[1].length;
        const text = indentMatch[3];
        listItems.push({text, indent});
        i++;
      }

      // Render nested list structure
      elements.push(renderNestedList(listItems, listStart, false));
      continue;
    }

    // Check for numbered lists
    if (trimmed.match(/^\d+\.\s+/)) {
      const listStart = i;
      const listItems: Array<{text: string, indent: number}> = [];

      // Collect all consecutive numbered list items with their indentation levels
      while (i < lines.length) {
        const currentLine = lines[i];
        const indentMatch = currentLine.match(/^(\s*)(\d+)\.\s+(.+)$/);
        if (!indentMatch) break;

        const indent = indentMatch[1].length;
        const text = indentMatch[3];
        listItems.push({text, indent});
        i++;
      }

      // Render nested list structure
      elements.push(renderNestedList(listItems, listStart, true));
      continue;
    }

    // Check for code fences (```)
    if (trimmed.startsWith('```')) {
      const language = trimmed.substring(3).trim().toLowerCase();
      const codeLines: string[] = [];
      i++; // Skip the opening fence

      // Collect lines until closing fence
      while (i < lines.length) {
        const codeLine = lines[i];
        if (codeLine.trim().startsWith('```')) {
          i++; // Skip the closing fence
          break;
        }
        codeLines.push(codeLine);
        i++;
      }

      // If it's SQL, use SqlBlock, otherwise use regular code block
      if (language === 'sql' || language === '') {
        elements.push(<SqlBlock key={i} content={codeLines.join('\n').trim()} />);
      } else {
        // For non-SQL code blocks, render as preformatted text
        elements.push(
          <pre key={i} className={styles.codeBlock}>
            <code>{codeLines.join('\n')}</code>
          </pre>
        );
      }
      continue;
    }

    // Check for SQL statements (without code fences)
    if (looksLikeSQL(line)) {
      // Remove list markers from SQL content
      const cleanedLine = line.trim().replace(/^[•\-*]\s+/, '');
      const sqlLines: string[] = [cleanedLine];
      i++;
      // Collect continuation lines
      while (i < lines.length) {
        const nextLine = lines[i];
        const nextTrimmed = nextLine.trim();
        if (nextTrimmed === '' ||
            nextLine.startsWith('  ') ||
            nextLine.startsWith('\t') ||
            nextTrimmed.includes('FROM') ||
            nextTrimmed.includes('WHERE') ||
            nextTrimmed.includes('ORDER BY') ||
            nextTrimmed.includes('GROUP BY') ||
            nextTrimmed.includes('LIMIT') ||
            nextTrimmed.includes('JOIN') ||
            nextTrimmed.endsWith(';')) {
          sqlLines.push(nextLine);
          if (nextTrimmed.endsWith(';')) {
            i++;
            break;
          }
          i++;
        } else {
          break;
        }
      }
      elements.push(<SqlBlock key={i} content={sqlLines.join('\n').trim()} />);
      continue;
    }

    // Regular text
    if (trimmed !== '') {
      elements.push(
        <div key={i} className={styles.textBlock}>
          {formatText(line)}
        </div>
      );
    }
    i++;
  }

  return <div className={styles.markdownRenderer}>{elements}</div>;
};

// Progressive renderer for thinking status - displays paragraphs one at a time with delays
export const ProgressiveThinkingStatus: React.FC<ProgressiveThinkingStatusProps> = ({ content }) => {
  const [visibleContent, setVisibleContent] = useState("");

  useEffect(() => {
    // Split content into paragraphs (separated by bold headers on their own lines)
    // Pattern: Split on lines that are **text** (headers)
    const preprocessed = content.replace(/([.!?])(\*\*[^*]+\*\*)/g, '$1\n\n$2');
    const lines = preprocessed.split('\n');

    // Group lines into paragraphs (each starting with a bold header or regular text)
    const paragraphs: string[] = [];
    let currentParagraph: string[] = [];

    for (const line of lines) {
      const trimmed = line.trim();

      // Check if this is a bold-only line (header)
      const isBoldHeader = trimmed.match(/^\*\*(.+?)\*\*$/);

      if (isBoldHeader && currentParagraph.length > 0) {
        // Save previous paragraph and start new one with this header
        paragraphs.push(currentParagraph.join('\n').trim());
        currentParagraph = [line];
      } else {
        currentParagraph.push(line);
      }
    }

    // Don't forget the last paragraph
    if (currentParagraph.length > 0) {
      paragraphs.push(currentParagraph.join('\n').trim());
    }

    // Display paragraphs progressively with time delays, replacing previous ones
    let currentIndex = 0;

    const displayNextParagraph = () => {
      if (currentIndex >= paragraphs.length) return;

      const paragraph = paragraphs[currentIndex];
      setVisibleContent(paragraph); // Replace previous content instead of appending

      // Calculate delay based on word count (300 words per minute = 200ms per word)
      const wordCount = paragraph.split(/\s+/).length;
      const delayMs = Math.max(1000, (wordCount / 300) * 60 * 1000); // Min 1 second

      currentIndex++;

      if (currentIndex < paragraphs.length) {
        setTimeout(displayNextParagraph, delayMs);
      }
    };

    // Start displaying
    displayNextParagraph();
  }, [content]);

  // Render without bold-as-heading conversion (just bold text, not larger)
  // Use a simplified renderer that treats **text** as inline bold, not headings
  const renderWithoutHeadings = (text: string): React.ReactNode[] => {
    const parts: React.ReactNode[] = [];
    const lines = text.split('\n');

    return lines.map((line, idx) => {
      const trimmed = line.trim();
      if (!trimmed) return null;

      // Format bold text inline without making it a heading
      const formatted = formatText(line);
      return (
        <div key={idx} className={styles.textBlock}>
          {formatted}
        </div>
      );
    }).filter(Boolean);
  };

  return <div className={styles.markdownRenderer}>{renderWithoutHeadings(visibleContent)}</div>;
};
