import assert from "node:assert/strict";
import test from "node:test";
import { graphResultLine, graphResultLines, isGraphOp } from "./graphResult.js";

test("graph lines report folder and size from existing fields, color only when sent", () => {
  assert.equal(isGraphOp("set_folder_colors"), true);
  assert.equal(isGraphOp("set_graph_node_size"), true);
  assert.equal(isGraphOp("create_note"), false);
  assert.equal(graphResultLine({ type: "set_folder_colors", folder: "work", include_children: true }), "Graph · work · direct subfolders");
  assert.equal(graphResultLine({ type: "set_graph_node_size", node_size_multiplier: 1.5 }), "Graph · 1.50x");
  assert.equal(graphResultLine({ type: "set_folder_colors", folder: "work", color: "#7b6cf6" }), "Graph · work · #7b6cf6");
  assert.equal(graphResultLine({ type: "create_note", folder: "work" }), undefined);
  assert.deepEqual(graphResultLines([
    { type: "set_folder_colors", folder: "work" },
    { type: "create_note", title: "skip" },
    { type: "set_graph_node_size", node_size_multiplier: 1.25 },
  ]), ["Graph · work", "Graph · 1.25x"]);
});
