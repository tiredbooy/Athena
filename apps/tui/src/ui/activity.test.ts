import assert from "node:assert/strict";
import test from "node:test";
import type { Activity } from "../protocol/types.js";
import {
  activityKey,
  activityTitle,
  applyActivity,
  finishActivityBlocks,
  isWorkActivity,
  lastFoldableActivityId,
  toggleActivityFold,
  workKind,
} from "./activity.js";

test("search, read, and write phases become work blocks; provider wait does not", () => {
  assert.equal(workKind({ phase: "searching", message: "Searching the vault" }), "search");
  assert.equal(workKind({ phase: "reading", message: "Reading work/plan.md", path: "work/plan.md" }), "read");
  assert.equal(workKind({ phase: "executing", message: "Creating note" }), "write");
  assert.equal(workKind({ phase: "working", message: "Coloring", tool: "set_folder_colors" }), "write");
  assert.equal(isWorkActivity({ phase: "provider_wait", message: "Generating a response" }), false);
  assert.equal(isWorkActivity({ phase: "working", message: "Using Ollama" }), false);
});

test("activity titles prefer tool and target over raw prose", () => {
  assert.equal(activityTitle({ phase: "reading", message: "Reading work/plan.md", path: "work/plan.md" }), "Read · work/plan.md");
  assert.equal(activityTitle({ phase: "searching", message: "Searching notes about rumera" }), "Searching notes about rumera");
  assert.equal(activityTitle({ phase: "reading", message: "Reading a note", tool: "get_note", target: "notes/foo.md", state: "started" }), "Get Note · notes/foo.md");
  assert.equal(activityTitle({ phase: "executing", message: "Coloring", tool: "set_folder_colors", target: "work" }), "Graph · work");
});

test("the same work key updates in place; a new step stays in the transcript", () => {
  const read: Activity = { phase: "reading", message: "Reading work/plan.md", path: "work/plan.md", state: "started" };
  const first = applyActivity([], read);
  assert.equal(first.length, 1);
  assert.equal(first[0].role, "activity");
  assert.equal(first[0].activity?.running, true);
  assert.match(first[0].text, /Read · work\/plan\.md/);

  const done = applyActivity(first, { ...read, state: "succeeded" });
  assert.equal(done.length, 1);
  assert.equal(done[0].id, first[0].id);
  assert.equal(done[0].activity?.running, false);

  const write = applyActivity(done, { phase: "executing", message: "Creating note \"Rumera\"" });
  assert.equal(write.length, 2);
  assert.equal(write[0].activity?.running, false);
  assert.equal(write[1].activity?.kind, "write");
  assert.notEqual(activityKey(read), activityKey({ phase: "executing", message: "Creating note \"Rumera\"" }));
});

test("finished blocks stay after the turn and remain foldable", () => {
  const started: Activity = { phase: "reading", message: "Reading work/plan.md", path: "work/plan.md" };
  const live = applyActivity([], started);
  assert.equal(live[0].activity?.folded, false);
  const finished = finishActivityBlocks(live);
  assert.equal(finished[0].activity?.running, false);
  assert.equal(finished[0].activity?.folded, true);
  assert.match(finished[0].text, /^▸ /);

  const id = lastFoldableActivityId(finished);
  const expanded = toggleActivityFold(finished, id!);
  assert.equal(expanded[0].activity?.folded, false);
  assert.match(expanded[0].text, /^▾ /);
  assert.match(expanded[0].text, /Reading work\/plan\.md/);
});
