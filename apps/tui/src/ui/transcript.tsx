import React from "react";
import { Text } from "ink";
import type { MarkdownStyle } from "../markdown.js";
import { useTheme, type Theme } from "../theme.js";
import {
  orderedSelection,
  selectedText,
  selectionPointAt,
  type Selection,
  type TranscriptContentRow,
  type TranscriptMessage,
  type TranscriptRow,
} from "../transcript.js";

export function TranscriptLine({ row, selection }: { row: TranscriptRow; selection?: Selection }): React.ReactElement {
  const theme = useTheme();
  if (row.kind === "spacer") return <Text> </Text>;
  const isUser = row.role === "user";
  const isError = row.role === "error";
  const isActivity = row.role === "activity";
  const isDiagnostic = row.role === "diagnostic";
  const prefix = row.firstLine
    ? isUser ? "you    " : isError ? "error  " : isDiagnostic ? "engine " : isActivity ? activityPrefix(row.activity?.kind) : "athena "
    : "       ";
  const failed = (row.activity?.state ?? "").toLowerCase() === "failed";
  const color = isUser ? theme.accent : isError ? theme.error : isDiagnostic ? theme.rail : isActivity ? failed ? theme.error : row.activity?.running ? theme.spinner : theme.muted : theme.success;
  const body = isError || isActivity || isDiagnostic ? color : theme.text;
  return <Text color={body} wrap="truncate-end"><Text color={color} bold>{prefix}</Text>{renderSelectedLine(row, selection, body, theme)}</Text>;
}

function activityPrefix(kind?: string): string {
  if (kind === "search") return "search ";
  if (kind === "read") return "read   ";
  if (kind === "write") return "write  ";
  return "work   ";
}

function renderSelectedLine(row: TranscriptContentRow, selection: Selection | undefined, color: string, theme: Theme): React.ReactNode {
  const range = orderedSelection(selection);
  const selected = range && row.message >= range.start.message && row.message <= range.end.message;
  const selectedStart = selected ? Math.max(0, (row.message === range.start.message ? range.start.offset : 0) - row.start) : -1;
  const selectedEnd = selected ? Math.min(row.text.length, (row.message === range.end.message ? range.end.offset : Number.MAX_SAFE_INTEGER) - row.start) : -1;
  if (!row.marks?.length) {
    if (selectedEnd <= selectedStart) return <Text color={color}>{row.text}</Text>;
    return <><Text color={color}>{row.text.slice(0, selectedStart)}</Text><Text color={color} inverse>{row.text.slice(selectedStart, selectedEnd)}</Text><Text color={color}>{row.text.slice(selectedEnd)}</Text></>;
  }
  const pieces: React.ReactNode[] = [];
  for (const mark of row.marks) {
    const style = markdownStyle(theme, mark.style);
    for (const [from, to, inverse] of splitSelected(mark.start, mark.end, selectedStart, selectedEnd)) {
      pieces.push(<Text key={`${from}-${to}-${inverse}`} color={style.color} bold={style.bold} inverse={inverse}>{row.text.slice(from, to)}</Text>);
    }
  }
  return <>{pieces}</>;
}

function splitSelected(start: number, end: number, selectedStart: number, selectedEnd: number): Array<[number, number, boolean]> {
  if (selectedEnd <= selectedStart || end <= selectedStart || start >= selectedEnd) return [[start, end, false]];
  const parts: Array<[number, number, boolean]> = [];
  if (start < selectedStart) parts.push([start, selectedStart, false]);
  parts.push([Math.max(start, selectedStart), Math.min(end, selectedEnd), true]);
  if (end > selectedEnd) parts.push([selectedEnd, end, false]);
  return parts;
}

function markdownStyle(theme: Theme, style: MarkdownStyle): { color: string; bold?: boolean } {
  switch (style) {
    case "heading":
      return { color: theme.accent, bold: true };
    case "list":
      return { color: theme.accent };
    case "code":
      return { color: theme.rail };
    case "codeFence":
    case "marker":
      return { color: theme.muted };
    case "bold":
      return { color: theme.text, bold: true };
    default:
      return { color: theme.text };
  }
}

export function handleMouseInput(
  chunk: string,
  state: { messages: TranscriptMessage[]; rows: TranscriptRow[]; selection?: Selection },
  setSelection: (selection?: Selection) => void,
  onCopy: (text: string) => void,
  onScroll: (delta: number) => void,
  onToggleActivity?: (id: number) => void,
): void {
  const mouseEvent = /\u001b?\[<(\d+);(\d+);(\d+)([mM])/g;
  let match: RegExpExecArray | null;
  while ((match = mouseEvent.exec(chunk)) !== null) {
    const button = Number(match[1]);
    const column = Number(match[2]);
    const row = Number(match[3]);
    const kind = match[4];
    if ((button & 64) !== 0 && kind === "M") {
      onScroll((button & 1) === 0 ? 3 : -3);
      continue;
    }
    const point = selectionPointAt(row, column, state.rows);
    const leftButton = (button & 3) === 0;
    if (kind === "M" && leftButton && point) {
      const nextSelection = (button & 32) !== 0 && state.selection
        ? { anchor: state.selection.anchor, focus: point }
        : { anchor: point, focus: point };
      state.selection = nextSelection;
      setSelection(nextSelection);
    }
    if (kind === "m" && state.selection) {
      const text = selectedText(state.messages, state.selection);
      if (text.length > 0) onCopy(text);
      else {
        const clicked = state.messages[state.selection.focus.message];
        if (clicked?.role === "activity") onToggleActivity?.(clicked.id);
      }
      state.selection = undefined;
      setSelection(undefined);
    }
  }
}
