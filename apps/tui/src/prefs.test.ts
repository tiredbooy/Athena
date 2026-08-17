import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  loadLastModel,
  loadRemoteVaultWarned,
  loadThemeName,
  loadVimMode,
  parseThemeName,
  saveLastModel,
  saveRemoteVaultWarned,
  saveThemeName,
  saveVimMode,
  shouldRestoreModel,
} from "./prefs.js";

test("prefs persist theme, provider, and model together", () => {
  const path = join(mkdtempSync(join(tmpdir(), "athena-ui-")), "ui.json");
  assert.equal(loadThemeName(path), "midnight");
  saveThemeName("ocean", path);
  saveLastModel({ providerId: "xai-oauth", model: "grok-4" }, path);
  assert.equal(loadThemeName(path), "ocean");
  assert.deepEqual(loadLastModel(path), { providerId: "xai-oauth", model: "grok-4" });
  writeFileSync(path, JSON.stringify({ theme: "solarized", extra: true }));
  assert.equal(loadThemeName(path), "midnight");
  assert.equal(parseThemeName("system"), "system");
  assert.equal(parseThemeName(1), undefined);
});

test("model restore runs only when hello disagrees with the saved choice", () => {
  const saved = { providerId: "xai-oauth", model: "grok-4" };
  assert.equal(shouldRestoreModel(saved, { model: "llama3.2:latest" }), true);
  assert.equal(shouldRestoreModel(saved, { model: "grok-4" }), false);
  assert.equal(shouldRestoreModel(undefined, { model: "grok-4" }), false);
  assert.equal(shouldRestoreModel({ providerId: "ollama", model: "" }, { model: "llama3" }), false);
});

test("first remote-connect warning is remembered in ui.json", () => {
  const path = join(mkdtempSync(join(tmpdir(), "athena-ui-")), "ui.json");
  assert.equal(loadRemoteVaultWarned(path), false);
  saveRemoteVaultWarned(path);
  assert.equal(loadRemoteVaultWarned(path), true);
  saveThemeName("ocean", path);
  assert.equal(loadRemoteVaultWarned(path), true);
});

test("vim mode is off by default and persists when enabled", () => {
  const path = join(mkdtempSync(join(tmpdir(), "athena-ui-")), "ui.json");
  assert.equal(loadVimMode(path), false);
  saveVimMode(true, path);
  assert.equal(loadVimMode(path), true);
  saveVimMode(false, path);
  assert.equal(loadVimMode(path), false);
});
