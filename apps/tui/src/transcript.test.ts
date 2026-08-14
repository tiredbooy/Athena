import assert from "node:assert/strict";
import test from "node:test";
import {
  buildTranscriptRows,
  selectedText,
  selectionPointAt,
  windowTranscript,
  wrapMessage,
  type TranscriptMessage,
} from "./transcript.js";

test("windowTranscript follows the newest rows and scrolls toward older rows", () => {
  const messages: TranscriptMessage[] = [
    { id: 1, role: "user", text: "first short message" },
    { id: 2, role: "assistant", text: "this response wraps across several terminal rows" },
  ];
  const rows = buildTranscriptRows(messages, 12);
  const bottom = windowTranscript(rows, 4, 0);
  const older = windowTranscript(rows, 4, 3);

  assert.equal(bottom.rows.length, 4);
  assert.equal(bottom.offset, 0);
  assert.notDeepEqual(older.rows, bottom.rows);
  assert.equal(older.offset, Math.min(3, older.maxOffset));
});

test("selection after scrolling retains original message offsets", () => {
  const messages: TranscriptMessage[] = [{ id: 1, role: "assistant", text: "alpha beta gamma" }];
  const rows = buildTranscriptRows(messages, 5);
  const visible = windowTranscript(rows, 2, 1).rows;
  const anchor = selectionPointAt(4, 10, visible);
  const focus = selectionPointAt(5, 15, visible);

  assert.deepEqual(anchor, { message: 0, offset: 6 });
  assert.deepEqual(focus, { message: 0, offset: 16 });
  assert.equal(selectedText(messages, anchor && focus ? { anchor, focus } : undefined), "beta gamma");
});

test("spacer rows never become copy-selection points", () => {
  const rows = buildTranscriptRows([{ id: 1, role: "user", text: "hello" }], 20);
  assert.equal(selectionPointAt(5, 10, rows), undefined);
});

test("wrapMessage preserves source offsets across explicit newlines", () => {
  assert.deepEqual(wrapMessage("abcd ef\nxy", 5), [
    { text: "abcd", start: 0 },
    { text: "ef", start: 5 },
    { text: "xy", start: 8 },
  ]);
});
