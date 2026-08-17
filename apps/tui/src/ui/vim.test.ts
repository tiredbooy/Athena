import assert from "node:assert/strict";
import test from "node:test";
import { vimMotion } from "./vim.js";

test("vim normal keys: i insert, j/k turns, g/G top/bottom", () => {
  assert.equal(vimMotion("i"), "insert");
  assert.equal(vimMotion("k"), "older");
  assert.equal(vimMotion("j"), "newer");
  assert.equal(vimMotion("g"), "top");
  assert.equal(vimMotion("G"), "bottom");
  assert.equal(vimMotion("x"), "none");
  assert.equal(vimMotion(""), "none");
});
