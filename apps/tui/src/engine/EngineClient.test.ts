import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  consumeDiagnosticChunk,
  EngineClient,
  firstTerminalEvent,
  isTerminalEngineEvent,
  parseEngineLine,
  readEngineEvents,
} from "./EngineClient.js";
import { fixturePath, loadFixture } from "./fixtures.js";
import type { EngineEvent } from "../protocol/types.js";

const fixtureEngine = `
const readline = require("node:readline");
const rl = readline.createInterface({ input: process.stdin });
const emit = (event) => process.stdout.write(JSON.stringify({ version: 1, ...event }) + "\\n");
rl.on("line", (line) => {
  const req = JSON.parse(line);
  const id = req.requestId;
  switch (req.type) {
    case "engine.hello":
      process.stderr.write("ollama: pulling llama3\\npartial");
      emit({ requestId: id, type: "engine.ready", message: "ready" });
      break;
    case "session.submit":
      emit({ requestId: id, type: "turn.started", turnId: "t1" });
      emit({ requestId: id, type: "activity", activity: { phase: "working", message: "thinking" } });
      emit({ requestId: id, type: "response", message: "done" });
      if (req.input === "plan") {
        emit({ requestId: id, type: "plan.ready", planId: "p1" });
        emit({ requestId: id, type: "turn.completed", turnId: "t1" });
      } else if (req.input === "fail") {
        emit({ requestId: id, type: "turn.failed", turnId: "t1", error: "boom" });
      } else if (req.input === "error") {
        emit({ requestId: id, type: "error", error: "rejected" });
      } else {
        emit({ requestId: id, type: "turn.completed", turnId: "t1", message: "done" });
      }
      break;
    case "plan.approve":
      emit({ requestId: id, type: "activity", activity: { phase: "executing", message: "writing" } });
      emit({ requestId: id, type: "plan.approved", planId: req.planId, message: "applied" });
      emit({ requestId: id, type: "plan.ready", planId: "p2" });
      break;
    case "session.reset":
      emit({ requestId: id, type: "session.reset", message: "Session history, pending plan, and pending question cleared" });
      break;
    case "provider.oauth.start":
      emit({ requestId: id, type: "provider.oauth.started", turnId: "oauth-1" });
      emit({ requestId: id, type: "provider.oauth.progress", turnId: "oauth-1", message: "open url" });
      emit({ requestId: id, type: "provider.connected", provider: "xai", model: "grok" });
      break;
    default:
      emit({ requestId: id, type: "error", error: "unsupported" });
  }
});
`;

async function withClient<T>(run: (client: EngineClient, seen: EngineEvent[]) => Promise<T>): Promise<T> {
  const client = new EngineClient(process.execPath, ["-e", fixtureEngine]);
  const seen: EngineEvent[] = [];
  client.on("event", (event: EngineEvent) => seen.push(event));
  try {
    return await run(client, seen);
  } finally {
    client.dispose();
  }
}

function typesThrough(seen: EngineEvent[], type: string): string[] {
  const types = seen.map((event) => event.type);
  const index = types.indexOf(type);
  assert.notEqual(index, -1, `missing ${type} in ${types.join(", ")}`);
  return types.slice(0, index + 1);
}

test("submit ignores turn.started, activity, and response, then settles on turn.completed", async () => {
  await withClient(async (client, seen) => {
    const result = await client.submit("hello");
    assert.equal(result.type, "turn.completed");
    assert.deepEqual(typesThrough(seen, "turn.completed"), ["turn.started", "activity", "response", "turn.completed"]);
  });
});

test("submit settles on plan.ready before the following turn.completed", async () => {
  await withClient(async (client, seen) => {
    const result = await client.submit("plan");
    assert.equal(result.type, "plan.ready");
    assert.equal(result.planId, "p1");
    assert.deepEqual(typesThrough(seen, "plan.ready"), ["turn.started", "activity", "response", "plan.ready"]);
  });
});

test("submit settles on turn.failed without treating it as a protocol error", async () => {
  await withClient(async (client) => {
    const result = await client.submit("fail");
    assert.equal(result.type, "turn.failed");
    assert.equal(result.error, "boom");
  });
});

test("submit rejects when the engine emits error", async () => {
  await withClient(async (client) => {
    await assert.rejects(client.submit("error"), { message: "rejected" });
  });
});

test("approve ignores activity and settles on plan.approved, not a later plan.ready", async () => {
  await withClient(async (client, seen) => {
    const result = await client.approve("p1");
    assert.equal(result.type, "plan.approved");
    assert.deepEqual(typesThrough(seen, "plan.approved"), ["activity", "plan.approved"]);
  });
});

test("oauth ignores started and progress even when they carry requestId", async () => {
  await withClient(async (client, seen) => {
    const result = await client.startOAuth("xai-oauth");
    assert.equal(result.type, "provider.connected");
    assert.deepEqual(seen.map((event) => event.type), [
      "provider.oauth.started",
      "provider.oauth.progress",
      "provider.connected",
    ]);
  });
});

test("stderr warnings become diagnostic lines and stay off JSON stdout", async () => {
  await withClient(async (client) => {
    const saw = new Promise<string>((resolve) => client.once("diagnostic", resolve));
    await client.hello();
    assert.equal(await saw, "ollama: pulling llama3");
    const leftover = consumeDiagnosticChunk("partial", " line\n");
    assert.deepEqual(leftover.lines, ["partial line"]);
    assert.equal(leftover.pending, "");
  });
});

test("reset settles on session.reset", async () => {
  await withClient(async (client) => {
    const result = await client.reset();
    assert.equal(result.type, "session.reset");
  });
});

test("hello still settles on the single engine.ready event", async () => {
  await withClient(async (client) => {
    const result = await client.hello();
    assert.equal(result.type, "engine.ready");
  });
});

test("readEngineEvents keeps fixture NDJSON in order and finds the terminal event", () => {
  const { events, errors } = readEngineEvents(loadFixture("submit-complete.ndjson"));
  assert.equal(errors.length, 0);
  assert.deepEqual(events.map((event) => event.type), [
    "turn.started",
    "activity",
    "activity",
    "activity",
    "activity",
    "activity",
    "response",
    "turn.completed",
  ]);
  assert.equal(firstTerminalEvent("session.submit", events)?.type, "turn.completed");
});

test("plan.ready is the submit terminal even when turn.completed follows", () => {
  const { events } = readEngineEvents(loadFixture("submit-plan.ndjson"));
  assert.equal(firstTerminalEvent("session.submit", events)?.type, "plan.ready");
  assert.equal(firstTerminalEvent("session.submit", events)?.planId, "p1");
});

test("approve fixture settles on plan.approved, not a later event", () => {
  const { events } = readEngineEvents(loadFixture("plan-approve.ndjson"));
  assert.equal(firstTerminalEvent("plan.approve", events)?.type, "plan.approved");
});

test("invalid fixture lines become protocol errors, not events", () => {
  const { events, errors } = readEngineEvents(loadFixture("invalid-lines.ndjson"));
  assert.deepEqual(events.map((event) => event.type), ["engine.ready"]);
  assert.equal(errors.length, 2);
  assert.equal(errors[0]?.message, "engine emitted invalid JSON");
  assert.equal(errors[1]?.message, "engine emitted an unsupported event");
  assert.equal(parseEngineLine("not json").ok, false);
});

test("EngineClient handleLine replays submit-complete NDJSON onto the live client", async () => {
  const replay = `
const fs = require("node:fs");
const readline = require("node:readline");
const fixture = fs.readFileSync(${JSON.stringify(fixturePath("submit-complete.ndjson"))}, "utf8");
const rl = readline.createInterface({ input: process.stdin });
rl.on("line", (line) => {
  const req = JSON.parse(line);
  for (const raw of fixture.split(/\\r?\\n/)) {
    if (!raw.trim()) continue;
    const event = JSON.parse(raw);
    event.requestId = req.requestId;
    process.stdout.write(JSON.stringify(event) + "\\n");
  }
});
`;
  const client = new EngineClient(process.execPath, ["-e", replay]);
  const seen: EngineEvent[] = [];
  client.on("event", (event: EngineEvent) => seen.push(event));
  try {
    const result = await client.submit("hello");
    assert.equal(result.type, "turn.completed");
    assert.deepEqual(seen.map((event) => event.type), [
      "turn.started",
      "activity",
      "activity",
      "activity",
      "activity",
      "activity",
      "response",
      "turn.completed",
    ]);
  } finally {
    client.dispose();
  }
});

test("reconnect hellos a new child after the first process exits", async () => {
  const stamp = join(mkdtempSync(join(tmpdir(), "athena-engine-")), "spawns");
  writeFileSync(stamp, "0");
  const crashOnce = `
const fs = require("node:fs");
const readline = require("node:readline");
const n = Number(fs.readFileSync(process.argv[1], "utf8"));
fs.writeFileSync(process.argv[1], String(n + 1));
const rl = readline.createInterface({ input: process.stdin });
const emit = (event) => process.stdout.write(JSON.stringify({ version: 1, ...event }) + "\\n");
rl.on("line", (line) => {
  const req = JSON.parse(line);
  emit({ requestId: req.requestId, type: "engine.ready", message: "ready-" + (n + 1) });
  if (n === 0) process.exit(2);
});
`;
  const client = new EngineClient(process.execPath, ["-e", crashOnce, stamp]);
  const closed = new Promise<Error>((resolve) => client.once("close", resolve));
  try {
    const first = await client.hello();
    assert.equal(first.message, "ready-1");
    assert.match((await closed).message, /exit code 2/);
    client.reconnect();
    const second = await client.hello();
    assert.equal(second.message, "ready-2");
    assert.equal(client.isDisposed, false);
  } finally {
    client.dispose();
  }
});

test("isTerminalEngineEvent matches the request that produced the stream", () => {
  assert.equal(isTerminalEngineEvent("session.submit", "turn.started"), false);
  assert.equal(isTerminalEngineEvent("session.submit", "activity"), false);
  assert.equal(isTerminalEngineEvent("session.submit", "response"), false);
  assert.equal(isTerminalEngineEvent("session.submit", "turn.completed"), true);
  assert.equal(isTerminalEngineEvent("session.submit", "plan.ready"), true);
  assert.equal(isTerminalEngineEvent("plan.approve", "activity"), false);
  assert.equal(isTerminalEngineEvent("plan.approve", "plan.approved"), true);
  assert.equal(isTerminalEngineEvent("plan.approve", "plan.ready"), false);
  assert.equal(isTerminalEngineEvent("provider.oauth.start", "provider.oauth.started"), false);
  assert.equal(isTerminalEngineEvent("provider.oauth.start", "provider.connected"), true);
  assert.equal(isTerminalEngineEvent("session.reset", "session.reset"), true);
  assert.equal(isTerminalEngineEvent("session.reset", "error"), true);
});
