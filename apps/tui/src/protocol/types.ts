export const PROTOCOL_VERSION = 1 as const;

export type RequestType =
  | "engine.hello"
  | "session.submit"
  | "session.cancel"
  | "plan.approve"
  | "plan.reject";

export type Action = {
  type: string;
  id?: string;
  depends_on?: string[];
  note_id?: number;
  title?: string;
  content?: string;
  folder?: string;
  paths?: string[];
};

export type Activity = {
  phase: string;
  message: string;
  provider?: string;
  model?: string;
  path?: string;
};

export type EngineRequest = {
  version: typeof PROTOCOL_VERSION;
  requestId: string;
  type: RequestType;
  input?: string;
  turnId?: string;
  planId?: string;
};

export type EngineEvent = {
  version: typeof PROTOCOL_VERSION;
  requestId?: string;
  type: string;
  turnId?: string;
  planId?: string;
  message?: string;
  error?: string;
  activity?: Activity;
  actions?: Action[];
};

export function isEngineEvent(value: unknown): value is EngineEvent {
  if (typeof value !== "object" || value === null) return false;
  const event = value as Record<string, unknown>;
  return event.version === PROTOCOL_VERSION && typeof event.type === "string";
}
