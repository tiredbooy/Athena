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
  resolve: (event: EngineEvent) => void;
  reject: (error: Error) => void;
};

export class EngineClient extends EventEmitter {
  private readonly process: ChildProcessWithoutNullStreams;
  private readonly lines: Interface;
  private readonly pending = new Map<string, PendingRequest>();
  private sequence = 0;
  private closed = false;

  constructor(command: string, args: string[] = []) {
    super();
    this.process = spawn(command, args, { stdio: ["pipe", "pipe", "pipe"] });
    this.lines = createInterface({ input: this.process.stdout });
    this.lines.on("line", (line) => this.handleLine(line));
    this.process.stdin.on("error", (error) => this.close(new Error(`engine input failed: ${error.message}`)));
    this.process.stderr.on("data", (chunk: Buffer) => this.emit("diagnostic", chunk.toString()));
    this.process.once("error", (error) => this.close(new Error(`engine process failed: ${error.message}`)));
    this.process.once("exit", (code, signal) => {
      const reason = signal ? `signal ${signal}` : `exit code ${code ?? "unknown"}`;
      this.close(new Error(`engine stopped (${reason})`));
    });
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

  approve(planId: string): Promise<EngineEvent> {
    return this.request("plan.approve", { planId });
  }

  reject(planId: string): Promise<EngineEvent> {
    return this.request("plan.reject", { planId });
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

  dispose(): void {
    this.close(new Error("engine client disposed"));
    this.process.kill();
  }

  private request(type: RequestType, fields: Omit<EngineRequest, "version" | "requestId" | "type"> = {}): Promise<EngineEvent> {
    if (this.closed) return Promise.reject(new Error("engine client is closed"));
    const requestId = `r${++this.sequence}`;
    const request: EngineRequest = { version: PROTOCOL_VERSION, requestId, type, ...fields };
    return new Promise((resolve, reject) => {
      this.pending.set(requestId, { resolve, reject });
      this.process.stdin.write(`${JSON.stringify(request)}\n`, (error) => {
        if (error) {
          this.pending.delete(requestId);
          reject(new Error(`send engine request: ${error.message}`));
        }
      });
    });
  }

  private handleLine(line: string): void {
    let value: unknown;
    try {
      value = JSON.parse(line);
    } catch {
      this.emit("protocolError", new Error("engine emitted invalid JSON"));
      return;
    }
    if (!isEngineEvent(value)) {
      this.emit("protocolError", new Error("engine emitted an unsupported event"));
      return;
    }
    const event = value as EngineEvent;
    this.emit("event", event);
    if (!event.requestId) return;
    const pending = this.pending.get(event.requestId);
    if (!pending) return;
    this.pending.delete(event.requestId);
    if (event.type === "error") pending.reject(new Error(event.error ?? "engine request failed"));
    else pending.resolve(event);
  }

  private close(error: Error): void {
    if (this.closed) return;
    this.closed = true;
    this.lines.close();
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
    this.emit("close", error);
  }
}
