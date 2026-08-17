import { canFoldActivity } from "./ui/activity.js";
import type { TranscriptMessage, TranscriptRow } from "./transcript.js";

export type ScrollbackTurn = {
  message: number;
  id: number;
  row: number;
  activityIds: number[];
};

// A turn starts at a user message and owns the activity and replies until
// the next user line. Navigation pins that user row to the top of the pane.
export function userTurns(messages: TranscriptMessage[], rows: TranscriptRow[]): ScrollbackTurn[] {
  const turns: ScrollbackTurn[] = [];
  messages.forEach((message, index) => {
    if (message.role !== "user") return;
    const row = rows.findIndex((entry) => entry.kind === "content" && entry.message === index && entry.firstLine);
    if (row < 0) return;
    const nextUser = messages.findIndex((entry, later) => later > index && entry.role === "user");
    const end = nextUser < 0 ? messages.length : nextUser;
    const activityIds = messages
      .slice(index, end)
      .filter((entry) => entry.role === "activity" && entry.activity && canFoldActivity(entry.activity))
      .map((entry) => entry.id);
    turns.push({ message: index, id: message.id, row, activityIds });
  });
  return turns;
}

export function viewportTopRow(rowCount: number, height: number, offset: number): number {
  const safeHeight = Math.max(1, height);
  const maxOffset = Math.max(0, rowCount - safeHeight);
  const safeOffset = Math.max(0, Math.min(maxOffset, offset));
  return Math.max(0, rowCount - safeOffset - safeHeight);
}

export function offsetForRow(rowIndex: number, rowCount: number, height: number): number {
  const safeHeight = Math.max(1, height);
  const maxOffset = Math.max(0, rowCount - safeHeight);
  return Math.max(0, Math.min(maxOffset, rowCount - safeHeight - rowIndex));
}

export function turnIndexAtRow(turns: ScrollbackTurn[], topRow: number): number {
  if (turns.length === 0) return -1;
  let index = 0;
  for (let i = 0; i < turns.length; i++) {
    if (turns[i].row <= topRow) index = i;
    else break;
  }
  return index;
}

export function previousTurnOffset(turns: ScrollbackTurn[], rowCount: number, height: number, offset: number): number {
  if (turns.length === 0) return offset;
  const current = turnIndexAtRow(turns, viewportTopRow(rowCount, height, offset));
  const pin = offsetForRow(turns[current].row, rowCount, height);
  if (offset < pin) return pin;
  if (current === 0) return pin;
  return offsetForRow(turns[current - 1].row, rowCount, height);
}

export function nextTurnOffset(turns: ScrollbackTurn[], rowCount: number, height: number, offset: number): number {
  if (turns.length === 0) return 0;
  const current = turnIndexAtRow(turns, viewportTopRow(rowCount, height, offset));
  if (current + 1 >= turns.length) return 0;
  return offsetForRow(turns[current + 1].row, rowCount, height);
}

export function foldableActivityInView(
  turns: ScrollbackTurn[],
  rowCount: number,
  height: number,
  offset: number,
  fallback?: number,
): number | undefined {
  const current = turnIndexAtRow(turns, viewportTopRow(rowCount, height, offset));
  const ids = current >= 0 ? turns[current]?.activityIds ?? [] : [];
  return ids[ids.length - 1] ?? fallback;
}
