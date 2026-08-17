import assert from "node:assert/strict";
import test from "node:test";
import { fieldsFromPreset, modelPickerRows, modelSelectableCount, nextConnectField } from "./catalog.js";

test("connect fields come from the preset, not a type switch", () => {
  assert.deepEqual(fieldsFromPreset({ id: "codex", label: "Codex", detail: "", type: "openai_codex", auth: "oauth", available: true }), []);
  assert.deepEqual(fieldsFromPreset({ id: "ollama", label: "Ollama", detail: "", type: "ollama", auth: "none", available: true }), []);
  assert.deepEqual(fieldsFromPreset({
    id: "openai", label: "OpenAI", detail: "", type: "openai", auth: "api_key", available: true,
    name: "OpenAI", base_url: "https://api.openai.com/v1", chat_model: "gpt-5.4",
  }), ["api_key"]);
  assert.deepEqual(fieldsFromPreset({
    id: "custom", label: "Custom", detail: "", type: "openai_compatible", auth: "api_key", available: true,
    base_url: "http://localhost:1234/v1",
  }), ["name", "api_key", "chat_model"]);
  assert.deepEqual(fieldsFromPreset({
    id: "future", label: "Future", detail: "", type: "mystery", auth: "api_key", available: true,
    fields: ["chat_model", "api_key"],
  }), ["chat_model", "api_key"]);
  assert.equal(nextConnectField(["name", "api_key", "chat_model"], "api_key"), "chat_model");
  assert.equal(nextConnectField(["api_key"], "api_key"), undefined);
});

test("model picker groups connected providers and ends with connect-new", () => {
  const rows = modelPickerRows([
    { providerId: "ollama", providerName: "Ollama", model: "llama3", current: true },
    { providerId: "openai-codex", providerName: "OpenAI Codex", model: "gpt-5.4", current: false },
  ]);
  assert.deepEqual(rows.map((row) => row.kind), ["header", "model", "header", "model", "connect"]);
  assert.equal(modelSelectableCount([
    { providerId: "ollama", providerName: "Ollama", model: "llama3", current: true },
    { providerId: "openai-codex", providerName: "OpenAI Codex", model: "gpt-5.4", current: false },
  ]), 3);
  const connect = rows[rows.length - 1];
  assert.equal(connect.kind, "connect");
  assert.equal(connect.index, 2);
  const empty = modelPickerRows([]);
  assert.equal(empty[0]?.kind, "connect");
  assert.equal(modelSelectableCount([]), 1);
});
