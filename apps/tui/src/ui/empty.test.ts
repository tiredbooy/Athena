import assert from "node:assert/strict";
import test from "node:test";
import { reduceEngineClose, reduceEvent, initialSessionState } from "../engine/session.js";
import { resolveEmptyState } from "./empty.js";
import { helpCommandLines, helpPanelRows, helpShortcutLines, isLocalCommand } from "./help.js";
import { commands } from "./palette.js";

test("empty session after hello is the vault start card", () => {
  const ready = reduceEvent(initialSessionState(), {
    version: 1,
    type: "engine.ready",
    provider: "ollama",
    model: "llama3.2:latest",
  });
  const state = resolveEmptyState({
    status: ready.status,
    connected: ready.connected,
    hasModel: ready.hasModel,
    messageCount: ready.messages.length,
  });
  assert.equal(state?.kind, "vault");
  assert.match(state?.action ?? "", /\/doctor/);
});

test("engine-down has copy and a restart action even after transcript rows exist", () => {
  const closed = reduceEngineClose(initialSessionState(), new Error("engine stopped (exit code 1)"));
  const empty = resolveEmptyState({
    status: closed.status,
    connected: closed.connected,
    hasModel: closed.hasModel,
    messageCount: closed.messages.length,
  });
  assert.equal(closed.connected, false);
  assert.equal(empty?.kind, "engine-down");
  assert.match(empty?.action ?? "", /reconnect|quit/i);

  const afterTalk = resolveEmptyState({
    status: "error",
    connected: false,
    hasModel: true,
    messageCount: 3,
  });
  assert.equal(afterTalk?.kind, "engine-down");
});

test("ready without a model is the no-models card", () => {
  const ready = reduceEvent(initialSessionState(), { version: 1, type: "engine.ready" });
  assert.equal(ready.connected, true);
  assert.equal(ready.hasModel, false);
  const state = resolveEmptyState({
    status: ready.status,
    connected: ready.connected,
    hasModel: ready.hasModel,
    messageCount: 0,
  });
  assert.equal(state?.kind, "no-models");
  assert.match(state?.action ?? "", /\/connect/);
});

test("connecting is the start card before hello", () => {
  const start = initialSessionState();
  const state = resolveEmptyState({
    status: start.status,
    connected: start.connected,
    hasModel: start.hasModel,
    messageCount: 0,
  });
  assert.equal(state?.kind, "connecting");
});

test("help overlay lists shortcuts and every palette command", () => {
  assert.ok(helpShortcutLines.length >= 3);
  assert.ok(helpShortcutLines.some((line) => line.includes("Esc cancel turn")));
  assert.deepEqual(
    helpCommandLines().map((line) => line.split(/\s+/)[0]),
    commands.map((command) => command.name),
  );
  assert.ok(helpPanelRows() > helpShortcutLines.length + commands.length);
  assert.equal(isLocalCommand("/help"), true);
  assert.equal(isLocalCommand("/models"), false);
});
