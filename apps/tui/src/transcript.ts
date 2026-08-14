export type TranscriptMessage = { id: number; role: "user" | "assistant" | "error"; text: string };
export type WrappedLine = { text: string; start: number };
export type SelectionPoint = { message: number; offset: number };
export type Selection = { anchor: SelectionPoint; focus: SelectionPoint };

export type TranscriptContentRow = {
  kind: "content";
  key: string;
  message: number;
  role: TranscriptMessage["role"];
  text: string;
  start: number;
  firstLine: boolean;
};

export type TranscriptSpacerRow = { kind: "spacer"; key: string };
export type TranscriptRow = TranscriptContentRow | TranscriptSpacerRow;

export type TranscriptWindow = {
  rows: TranscriptRow[];
  offset: number;
  maxOffset: number;
};

// The transcript, renderer, scroller, and mouse selector all consume these
// same source-aware rows. Keeping one representation prevents a scrolled line
// from being copied from the wrong message or source offset.
export function buildTranscriptRows(messages: TranscriptMessage[], width: number): TranscriptRow[] {
  const rows: TranscriptRow[] = [];
  messages.forEach((message, messageIndex) => {
    wrapMessage(message.text, width).forEach((line, lineIndex) => {
      rows.push({
        kind: "content",
        key: `${message.id}:${lineIndex}`,
        message: messageIndex,
        role: message.role,
        text: line.text,
        start: line.start,
        firstLine: lineIndex === 0,
      });
    });
    rows.push({ kind: "spacer", key: `${message.id}:spacer` });
  });
  return rows;
}

// offset is measured from the bottom: zero follows the newest output, while a
// larger value moves toward older rows. The returned slice never exceeds the
// real viewport height, so Ink has nothing to shrink or overlap.
export function windowTranscript(rows: TranscriptRow[], height: number, offset: number): TranscriptWindow {
  const safeHeight = Math.max(1, height);
  const maxOffset = Math.max(0, rows.length - safeHeight);
  const safeOffset = Math.max(0, Math.min(maxOffset, offset));
  const end = rows.length - safeOffset;
  const start = Math.max(0, end - safeHeight);
  return { rows: rows.slice(start, end), offset: safeOffset, maxOffset };
}

export function selectionPointAt(
  screenRow: number,
  column: number,
  rows: TranscriptRow[],
  firstScreenRow = 4,
  contentStartColumn = 10,
): SelectionPoint | undefined {
  const row = rows[screenRow - firstScreenRow];
  if (!row || row.kind !== "content") return undefined;
  const offset = Math.max(0, Math.min(row.text.length, column - contentStartColumn));
  return { message: row.message, offset: row.start + offset };
}

export function selectedText(messages: TranscriptMessage[], selection?: Selection): string {
  const range = orderedSelection(selection);
  if (!range) return "";
  const chunks: string[] = [];
  for (let index = range.start.message; index <= range.end.message; index++) {
    const message = messages[index]?.text ?? "";
    const start = index === range.start.message ? range.start.offset : 0;
    const end = index === range.end.message ? range.end.offset : message.length;
    chunks.push(message.slice(start, end));
  }
  return chunks.join("\n").trim();
}

export function orderedSelection(selection?: Selection): { start: SelectionPoint; end: SelectionPoint } | undefined {
  if (!selection) return undefined;
  const before = selection.anchor.message < selection.focus.message
    || (selection.anchor.message === selection.focus.message && selection.anchor.offset <= selection.focus.offset);
  return before ? { start: selection.anchor, end: selection.focus } : { start: selection.focus, end: selection.anchor };
}

export function wrapMessage(text: string, width: number): WrappedLine[] {
  const lines: WrappedLine[] = [];
  let offset = 0;
  for (const sourceLine of text.split("\n")) {
    let remaining = sourceLine;
    let lineStart = offset;
    if (remaining.length === 0) lines.push({ text: "", start: lineStart });
    while (remaining.length > 0) {
      if (remaining.length <= width) {
        lines.push({ text: remaining, start: lineStart });
        break;
      }
      let breakAt = remaining.lastIndexOf(" ", width);
      if (breakAt <= 0) breakAt = width;
      const line = remaining.slice(0, breakAt);
      lines.push({ text: line, start: lineStart });
      const consumed = breakAt < remaining.length && remaining[breakAt] === " " ? breakAt + 1 : breakAt;
      remaining = remaining.slice(consumed);
      lineStart += consumed;
    }
    offset += sourceLine.length + 1;
  }
  return lines;
}
