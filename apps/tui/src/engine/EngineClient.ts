import { EventEmitter } from "node:events";
import { createInterface, type Interface } from "node:readline";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import {
  PROTOCOL_VERSION,
  type EngineEvent,
  type EngineRequest,
  type ProviderConnection,
  type RequestType,
  isEngineEvent,
} from "../protocol/types.js";

type PendingRequest = {
  type: RequestType;
  resolve: (event: EngineEvent) => void;
  reject: (error: Error) => void;
};

// Progress events reuse the request's requestId. The RPC is unfinished until
// the engine emits a result for that request type.
const TERMINAL_EVENT_TYPES: Record<RequestType, ReadonlySet<string>> = {
  "engine.hello": new Set(["engine.ready", "error"]),
  "session.submit": new Set(["turn.completed", "plan.ready", "turn.failed", "turn.cancelled", "error"]),
  "session.cancel": new Set(["turn.cancellation_requested", "error"]),
  "session.reset": new Set(["session.reset", "error"]),
  "plan.approve": new Set(["plan.approved", "error", "turn.failed"]),
  "plan.reject": new Set(["plan.rejected", "error"]),
  "model.list": new Set(["model.options", "error"]),
  "model.select": new Set(["model.selected", "error"]),
  "provider.list": new Set(["provider.presets", "error"]),
  "provider.connect": new Set(["provider.connected", "error"]),
  "provider.oauth.start": new Set(["provider.connected", "provider.oauth.cancelled", "error"]),
};

export function isTerminalEngineEvent(requestType: RequestType, eventType: string): boolean {
  return TERMINAL_EVENT_TYPES[requestType].has(eventType);
}

export type ParsedEngineLine =
  | { ok: true; event: EngineEvent }
  | { ok: false; error: Error };

export function parseEngineLine(line: string): ParsedEngineLine {
  let value: unknown;
  try {
    value = JSON.parse(line);
  } catch {
    return { ok: false, error: new Error("engine emitted invalid JSON") };
  }
  if (!isEngineEvent(value)) {
    return { ok: false, error: new Error("engine emitted an unsupported event") };
  }
  return { ok: true, event: value };
}

export function readEngineEvents(ndjson: string): { events: EngineEvent[]; errors: Error[] } {
  const events: EngineEvent[] = [];
  const errors: Error[] = [];
  for (const line of ndjson.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const parsed = parseEngineLine(trimmed);
    if (parsed.ok) events.push(parsed.event);
    else errors.push(parsed.error);
  }
  return { events, errors };
}

export function firstTerminalEvent(requestType: RequestType, events: EngineEvent[]): EngineEvent | undefined {
  return events.find((event) => isTerminalEngineEvent(requestType, event.type));
}

export function consumeDiagnosticChunk(pending: string, chunk: string): { pending: string; lines: string[] } {
  const parts = (pending + chunk).split(/\r?\n/);
  const rest = parts.pop() ?? "";
  const lines = parts.map((line) => line.trim()).filter((line) => line.length > 0);
  return { pending: rest, lines };
}

export class EngineClient extends EventEmitter {
  private process!: ChildProcessWithoutNullStreams;
  private lines!: Interface;
  private readonly pending = new Map<string, PendingRequest>();
  private sequence = 0;
  private closed = false;
  private disposed = false;
  private stderrPending = "";
  private readonly command: string;
  private readonly args: string[];

  constructor(command: string, args: string[] = []) {
    super();
    this.command = command;
    this.args = args;
    this.attach();
  }

  get isDisposed(): boolean {
    return this.disposed;
  }

  hello(): Promise<EngineEvent> {
    return this.request("engine.hello");
  }

  submit(input: string, turnId?: string): Promise<EngineEvent> {
    return this.request("session.submit", { input, turnId });
  }

  cancel(turnId: string): Promise<EngineEvent> {
    return this.request("session.cancel", { turnId });
  }

  reset(): Promise<EngineEvent> {
    return this.request("session.reset");
  }

  approve(planId: string): Promise<EngineEvent> {
    return this.request("plan.approve", { planId });
  }

  reject(planId: string): Promise<EngineEvent> {
    return this.request("plan.reject", { planId });
  }

  models(): Promise<EngineEvent> {
    return this.request("model.list");
  }

  selectModel(providerId: string, model: string): Promise<EngineEvent> {
    return this.request("model.select", { providerId, model });
  }

  providers(): Promise<EngineEvent> {
    return this.request("provider.list");
  }

  connect(connection: ProviderConnection): Promise<EngineEvent> {
    return this.request("provider.connect", { connection });
  }

  startOAuth(providerId: string): Promise<EngineEvent> {
    return this.request("provider.oauth.start", { providerId });
  }

  reconnect(): void {
    if (this.disposed) throw new Error("engine client is disposed");
    if (!this.closed) {
      this.shutdown(new Error("engine client reconnecting"));
      this.process.kill();
    }
    this.attach();
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.shutdown(new Error("engine client disposed"));
    this.process.kill();
  }

  private request(type: RequestType, fields: Omit<EngineRequest, "version" | "requestId" | "type"> = {}): Promise<EngineEvent> {
    if (this.closed) return Promise.reject(new Error("engine client is closed"));
    const requestId = `r${++this.sequence}`;
    const request: EngineRequest = { version: PROTOCOL_VERSION, requestId, type, ...fields };
    return new Promise((resolve, reject) => {
      this.pending.set(requestId, { type, resolve, reject });
      this.process.stdin.write(`${JSON.stringify(request)}\n`, (error) => {
        if (error) {
          this.pending.delete(requestId);
          reject(new Error(`send engine request: ${error.message}`));
        }
      });
    });
  }

  private attach(): void {
    this.closed = false;
    this.stderrPending = "";
    const child = spawn(this.command, this.args, { stdio: ["pipe", "pipe", "pipe"] });
    this.process = child;
    this.lines = createInterface({ input: child.stdout });
    this.lines.on("line", (line) => {
      if (this.process !== child) return;
      this.handleLine(line);
    });
    child.stdin.on("error", (error) => {
      if (this.process !== child) return;
      this.shutdown(new Error(`engine input failed: ${error.message}`));
    });
    child.stderr.on("data", (chunk: Buffer) => {
      if (this.process !== child) return;
      const next = consumeDiagnosticChunk(this.stderrPending, chunk.toString());
      this.stderrPending = next.pending;
      for (const line of next.lines) this.emit("diagnostic", line);
    });
    child.once("error", (error) => {
      if (this.process !== child) return;
      this.shutdown(new Error(`engine process failed: ${error.message}`));
    });
    child.once("exit", (code, signal) => {
      if (this.process !== child) return;
      const reason = signal ? `signal ${signal}` : `exit code ${code ?? "unknown"}`;
      this.shutdown(new Error(`engine stopped (${reason})`));
    });
  }

  private handleLine(line: string): void {
    const parsed = parseEngineLine(line);
    if (!parsed.ok) {
      this.emit("protocolError", parsed.error);
      return;
    }
    const event = parsed.event;
    this.emit("event", event);
    if (!event.requestId) return;
    const pending = this.pending.get(event.requestId);
    if (!pending || !isTerminalEngineEvent(pending.type, event.type)) return;
    this.pending.delete(event.requestId);
    if (event.type === "error") pending.reject(new Error(event.error ?? "engine request failed"));
    else pending.resolve(event);
  }

  private shutdown(error: Error): void {
    if (this.closed) return;
    this.closed = true;
    const leftover = this.stderrPending.trim();
    this.stderrPending = "";
    if (leftover) this.emit("diagnostic", leftover);
    this.lines.close();
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
    if (!this.disposed) this.emit("close", error);
  }
}
