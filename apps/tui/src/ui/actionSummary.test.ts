import assert from "node:assert/strict";
import test from "node:test";
import { actionSummary } from "./actionSummary.js";

test("engine summary wins over local field assembly", () => {
  assert.equal(actionSummary({ type: "create_note", title: "Rumera", summary: "Create Rumera in work/" }), "Create Rumera in work/");
});

test("without a summary, the card prints type and present targets only", () => {
  assert.equal(actionSummary({ type: "create_note", title: "Rumera" }), "create note → Rumera");
  assert.equal(actionSummary({ type: "move_folder", folder: "work/old", new_folder: "work" }), "move folder → work/old · parent work");
  assert.equal(actionSummary({ type: "set_folder_colors", folder: "work", include_children: true }), "Graph · work · direct subfolders");
  assert.equal(actionSummary({ type: "set_graph_node_size", node_size_multiplier: 1.5 }), "Graph · 1.50x");
  assert.equal(actionSummary({ type: "set_folder_colors", folder: "work", color: "#7b6cf6" }), "Graph · work · #7b6cf6");
  assert.equal(actionSummary({ type: "unknown_future_op" }), "unknown future op");
});
