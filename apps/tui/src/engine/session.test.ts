import assert from "node:assert/strict";
import test from "node:test";
import { firstTerminalEvent, readEngineEvents } from "./EngineClient.js";
import { loadFixture } from "./fixtures.js";
import {
  beginReconnect,
  initialSessionState,
  reconnectExplanation,
  reduceDiagnostic,
  reduceEvent,
  type SessionState,
} from "./session.js";

function foldFixture(name: string, start = initialSessionState()): SessionState {
  const { events, errors } = readEngineEvents(loadFixture(name));
  assert.equal(errors.length, 0, errors.map((error) => error.message).join("; "));
  return events.reduce(reduceEvent, start);
}

test("hello-ready fixture marks the session ready and names the model", () => {
  const state = foldFixture("hello-ready.ndjson");
  assert.equal(state.status, "ready");
  assert.equal(state.activity, "Ready");
  assert.equal(state.connected, true);
  assert.equal(state.hasModel, true);
  assert.equal(state.modelName, "ollama · llama3.2:latest");
});

test("submit-complete fixture becomes work blocks plus one assistant reply", () => {
  const state = foldFixture("submit-complete.ndjson");
  assert.equal(state.status, "ready");
  assert.equal(state.turnID, undefined);
  assert.equal(state.modelName, "ollama · llama3.2:latest");

  const activities = state.messages.filter((message) => message.role === "activity");
  assert.equal(activities.length, 2);
  assert.equal(activities[0]?.activity?.kind, "search");
  assert.equal(activities[1]?.activity?.kind, "read");
  assert.ok(activities.every((message) => message.activity && !message.activity.running));
  assert.match(activities[0]?.text ?? "", /Search|rumera/i);
  assert.match(activities[1]?.text ?? "", /Get Note|work\/plan\.md/);

  const replies = state.messages.filter((message) => message.role === "assistant");
  assert.equal(replies.length, 1);
  assert.equal(replies[0]?.text, "Rumera is tracked in work/plan.md.");
  assert.equal(state.messages.some((message) => message.role === "activity" && message.text.includes("provider_wait")), false);
});

test("submit-plan fixture focuses a waiting plan card before turn.completed", () => {
  const { events } = readEngineEvents(loadFixture("submit-plan.ndjson"));
  assert.equal(firstTerminalEvent("session.submit", events)?.type, "plan.ready");

  const state = events.reduce(reduceEvent, initialSessionState());
  assert.equal(state.status, "approval");
  assert.equal(state.reviewing, true);
  assert.equal(state.planDecision, "waiting");
  assert.equal(state.plan?.id, "p1");
  assert.deepEqual(state.plan?.actions, [{ type: "create_note", title: "Rumera", folder: "work" }]);
  assert.equal(state.turnID, undefined);
  assert.equal(state.activity, "Waiting for your approval");
});

test("submit-failed fixture records the engine error in the transcript", () => {
  const state = foldFixture("submit-failed.ndjson");
  assert.equal(state.status, "error");
  assert.equal(state.turnID, undefined);
  assert.equal(state.error, "ollama is not running");
  assert.ok(state.messages.some((message) => message.role === "error" && message.text === "ollama is not running"));
});

test("session.reset clears the pane, plan, and turn without leaving a fake history", () => {
  const afterPlan = foldFixture("submit-plan.ndjson");
  const state = foldFixture("session-reset.ndjson", afterPlan);
  assert.equal(state.messages.length, 0);
  assert.equal(state.plan, undefined);
  assert.equal(state.reviewing, false);
  assert.equal(state.turnID, undefined);
  assert.equal(state.error, undefined);
  assert.equal(state.status, "ready");
  assert.match(state.activity, /cleared|reset/i);
});

test("plan-approve fixture keeps the write block and clears the card", () => {
  const afterPlan = foldFixture("submit-plan.ndjson");
  const state = foldFixture("plan-approve.ndjson", afterPlan);
  assert.equal(state.plan, undefined);
  assert.equal(state.reviewing, false);
  assert.equal(state.status, "ready");
  assert.equal(state.activity, "Changes applied");
  assert.ok(state.messages.some((message) => message.role === "activity" && message.activity?.kind === "write"));
  assert.ok(state.messages.some((message) => message.role === "assistant" && message.text === "Created work/rumera.md"));
});

test("oauth-connect fixture settles on provider.connected and records the reply", () => {
  const { events } = readEngineEvents(loadFixture("oauth-connect.ndjson"));
  assert.equal(firstTerminalEvent("provider.oauth.start", events)?.type, "provider.connected");

  const started: SessionState = {
    ...initialSessionState(),
    connectFlow: {
      step: "providers",
      presets: [],
      selectedIndex: 0,
      values: { name: "", type: "", base_url: "", chat_model: "" },
      oauthLines: [],
      fields: [],
    },
  };
  const state = events.reduce(reduceEvent, started);
  assert.equal(state.connectFlow, undefined);
  assert.equal(state.status, "ready");
  assert.equal(state.modelName, "xai · grok-3");
  assert.equal(state.activity, "Provider connected");
  assert.ok(state.messages.some((message) => message.role === "assistant" && message.text === "Connected to xAI"));
});

test("model-options fixture opens the picker on the current model", () => {
  const state = foldFixture("model-options.ndjson");
  assert.equal(state.status, "ready");
  assert.equal(state.activity, "Choose a model");
  assert.equal(state.modelFlow?.selectedIndex, 0);
  assert.equal(state.modelFlow?.options.length, 2);
  assert.equal(state.modelFlow?.options[0]?.current, true);
});

test("response.delta appends tokens without collapsing work blocks", () => {
  const { events } = readEngineEvents(loadFixture("response-delta.ndjson"));
  assert.equal(firstTerminalEvent("session.submit", events)?.type, "turn.completed");

  const afterDeltas = events.slice(0, 5).reduce(reduceEvent, initialSessionState());
  const live = afterDeltas.messages.filter((message) => message.role === "assistant");
  assert.equal(afterDeltas.status, "working");
  assert.equal(live.length, 1);
  assert.equal(live[0]?.text, "Rumera is tracked.");
  assert.equal(live[0]?.streaming, true);
  assert.equal(afterDeltas.messages.filter((message) => message.role === "activity").length, 1);

  const state = events.reduce(reduceEvent, initialSessionState());
  const replies = state.messages.filter((message) => message.role === "assistant");
  const activities = state.messages.filter((message) => message.role === "activity");
  assert.equal(state.status, "ready");
  assert.equal(replies.length, 1);
  assert.equal(replies[0]?.text, "Rumera is tracked.");
  assert.equal(replies[0]?.streaming, false);
  assert.equal(activities.length, 1);
  assert.equal(activities[0]?.activity?.running, false);
});

test("turn.completed keeps streamed text when no final response arrives", () => {
  const state = [
    { version: 1 as const, type: "turn.started", turnId: "t9" },
    { version: 1 as const, type: "response.delta", message: "partial" },
    { version: 1 as const, type: "turn.completed", turnId: "t9" },
  ].reduce(reduceEvent, initialSessionState());
  assert.equal(state.messages[0]?.text, "partial");
  assert.equal(state.messages[0]?.streaming, false);
});

test("plan.approved keeps graph folder and size lines in the transcript", () => {
  const waiting: SessionState = {
    ...initialSessionState(),
    plan: {
      id: "p-graph",
      actions: [
        { type: "set_folder_colors", folder: "work" },
        { type: "set_graph_node_size", node_size_multiplier: 1.5 },
      ],
    },
    status: "approval",
    reviewing: true,
  };
  const state = reduceEvent(waiting, { version: 1, type: "plan.approved", planId: "p-graph", message: "Changes applied" });
  const lines = state.messages.filter((message) => message.role === "assistant").map((message) => message.text);
  assert.deepEqual(lines, ["Graph · work", "Graph · 1.50x", "Changes applied"]);
  assert.equal(state.plan, undefined);
});

test("beginReconnect drops invalid plan/turn IDs and explains the new session", () => {
  const midTurn: SessionState = {
    ...initialSessionState(),
    connected: true,
    hasModel: true,
    status: "working",
    turnID: "t1",
    plan: { id: "p1", actions: [{ type: "create_note" }] },
    reviewing: true,
    messages: [{ id: 1, role: "user", text: "write it" }],
  };
  const next = beginReconnect(midTurn, new Error("engine stopped (exit code 1)"));
  assert.equal(next.connected, false);
  assert.equal(next.status, "starting");
  assert.equal(next.turnID, undefined);
  assert.equal(next.plan, undefined);
  assert.equal(next.reviewing, false);
  assert.equal(next.messages[0]?.text, "write it");
  assert.ok(next.messages.some((message) => message.text === reconnectExplanation));
});

test("duplicate diagnostic lines collapse", () => {
  const once = reduceDiagnostic(initialSessionState(), "ollama: pulling llama3");
  const twice = reduceDiagnostic(once, "ollama: pulling llama3");
  assert.equal(once.messages.length, 1);
  assert.equal(twice.messages.length, 1);
  assert.equal(twice.messages[0]?.role, "diagnostic");
});
