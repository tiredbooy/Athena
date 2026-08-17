import assert from "node:assert/strict";
import test from "node:test";
import {
  foldableActivityInView,
  nextTurnOffset,
  offsetForRow,
  previousTurnOffset,
  userTurns,
} from "./scrollback.js";
import { buildTranscriptRows, type TranscriptMessage } from "./transcript.js";

function sample(): TranscriptMessage[] {
  return [
    { id: 1, role: "user", text: "first" },
    { id: 2, role: "activity", text: "Read · a.md", activity: { key: "a", kind: "read", phase: "reading", title: "Read · a.md", detail: "Reading a.md", running: false, folded: true } },
    { id: 3, role: "assistant", text: "one" },
    { id: 4, role: "user", text: "second" },
    { id: 5, role: "activity", text: "Read · b.md", activity: { key: "b", kind: "read", phase: "reading", title: "Read · b.md", detail: "Reading b.md", running: false, folded: true } },
    { id: 6, role: "assistant", text: "two" },
    { id: 7, role: "user", text: "third" },
    { id: 8, role: "assistant", text: "three" },
  ];
}

test("userTurns start at each user line and own later activity until the next user", () => {
  const messages = sample();
  const rows = buildTranscriptRows(messages, 40);
  const turns = userTurns(messages, rows);
  assert.equal(turns.length, 3);
  assert.deepEqual(turns.map((turn) => turn.id), [1, 4, 7]);
  assert.deepEqual(turns[0]?.activityIds, [2]);
  assert.deepEqual(turns[1]?.activityIds, [5]);
  assert.deepEqual(turns[2]?.activityIds, []);
  assert.ok((turns[0]?.row ?? 1) < (turns[1]?.row ?? 0));
});

test("previous turn from the live tail pins the latest user prompt, then older ones", () => {
  const messages = sample();
  const rows = buildTranscriptRows(messages, 40);
  const turns = userTurns(messages, rows);
  const height = 4;
  const latest = previousTurnOffset(turns, rows.length, height, 0);
  const older = previousTurnOffset(turns, rows.length, height, latest);
  const oldest = previousTurnOffset(turns, rows.length, height, older);
  assert.ok(latest >= 0);
  assert.ok(older > latest);
  assert.ok(oldest >= older);
  assert.equal(previousTurnOffset(turns, rows.length, height, oldest), oldest);
});

test("next turn walks toward the live tail and then follows newest output", () => {
  const messages = sample();
  const rows = buildTranscriptRows(messages, 40);
  const turns = userTurns(messages, rows);
  const height = 4;
  const oldest = previousTurnOffset(
    turns,
    rows.length,
    height,
    previousTurnOffset(turns, rows.length, height, previousTurnOffset(turns, rows.length, height, 0)),
  );
  const second = nextTurnOffset(turns, rows.length, height, oldest);
  const live = nextTurnOffset(turns, rows.length, height, nextTurnOffset(turns, rows.length, height, second));
  assert.ok(second < oldest);
  assert.equal(live, 0);
});

test("Ctrl+O style fold prefers the activity on the visible turn", () => {
  const messages = sample();
  const rows = buildTranscriptRows(messages, 40);
  const turns = userTurns(messages, rows);
  const height = 4;
  const first = offsetForRow(turns[0]!.row, rows.length, height);
  const second = offsetForRow(turns[1]!.row, rows.length, height);
  assert.equal(foldableActivityInView(turns, rows.length, height, first, 99), 2);
  assert.equal(foldableActivityInView(turns, rows.length, height, second, 99), 5);
  assert.equal(foldableActivityInView(turns, rows.length, height, 0, 99), 99);
});
