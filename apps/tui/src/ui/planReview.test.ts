import assert from "node:assert/strict";
import test from "node:test";
import { composerApprovesPlan, reviewKeyAction } from "./planReview.js";

test("focused review keys: Y/Enter approve, N/R reject, Esc back", () => {
  assert.equal(reviewKeyAction("y", {}), "approve");
  assert.equal(reviewKeyAction("Y", {}), "approve");
  assert.equal(reviewKeyAction("", { return: true }), "approve");
  assert.equal(reviewKeyAction("n", {}), "reject");
  assert.equal(reviewKeyAction("R", {}), "reject");
  assert.equal(reviewKeyAction("", { escape: true }), "back");
  assert.equal(reviewKeyAction("yes", {}), "none");
  assert.equal(reviewKeyAction("ok", {}), "none");
});

test("composer text never counts as a plan approval", () => {
  assert.equal(composerApprovesPlan("yes"), false);
  assert.equal(composerApprovesPlan("y"), false);
  assert.equal(composerApprovesPlan("/confirm"), false);
});
