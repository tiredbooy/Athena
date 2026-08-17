import assert from "node:assert/strict";
import test from "node:test";
import { isLoopbackUrl, isRemoteProvider, needsRemoteVaultWarning, remoteVaultWarning } from "./remoteVault.js";

test("ollama and loopback APIs stay local; hosted APIs are remote", () => {
  assert.equal(isRemoteProvider({ type: "ollama", auth: "none" }), false);
  assert.equal(isLoopbackUrl("http://127.0.0.1:11434"), true);
  assert.equal(isRemoteProvider({ type: "openai_compatible", base_url: "http://localhost:1234/v1" }), false);
  assert.equal(isRemoteProvider({ type: "openai", auth: "api_key", base_url: "https://api.openai.com/v1" }), true);
  assert.equal(isRemoteProvider({ type: "openai_codex", auth: "oauth" }), true);
  assert.equal(isRemoteProvider({ type: "xai_oauth", auth: "oauth" }), true);
});

test("the first remote connect needs the vault-leaving warning", () => {
  assert.match(remoteVaultWarning, /inventory/i);
  assert.match(remoteVaultWarning, /search/i);
  assert.match(remoteVaultWarning, /get_note/i);
  assert.equal(needsRemoteVaultWarning({ type: "openai", auth: "api_key" }, false), true);
  assert.equal(needsRemoteVaultWarning({ type: "openai", auth: "api_key" }, true), false);
  assert.equal(needsRemoteVaultWarning({ type: "ollama", auth: "none" }, false), false);
});
