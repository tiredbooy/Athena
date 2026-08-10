export const PROTOCOL_VERSION = 1 as const;

export type RequestType =
  | "engine.hello"
  | "session.submit"
  | "session.cancel"
  | "plan.approve"
  | "plan.reject"
  | "model.list"
  | "model.select"
  | "provider.list"
  | "provider.connect"
  | "provider.oauth.start";

export type ProviderPreset = {
  id: string;
  label: string;
  detail: string;
  type: string;
  auth: "api_key" | "oauth" | "none";
  name?: string;
  base_url?: string;
  api_key_env?: string;
  chat_model?: string;
  available: boolean;
  unavailable?: string;
};

export type ProviderConnection = {
  name: string;
  type: string;
  base_url: string;
  api_key_env?: string;
  api_key?: string;
  chat_model: string;
};

export type ModelOption = {
  providerId: string;
  providerName: string;
  model: string;
  current: boolean;
};

export type Action = {
  type: string;
  id?: string;
  depends_on?: string[];
  note_id?: number;
  title?: string;
  content?: string;
  folder?: string;
  include_children?: boolean;
  node_size_multiplier?: number;
  paths?: string[];
  folders?: string[];
  new_folder?: string;
};

export type Activity = {
  phase: string;
  message: string;
  provider?: string;
  model?: string;
  path?: string;
  run_id?: string;
  step?: number;
  tool?: string;
  target?: string;
  state?: string;
};

export type EngineRequest = {
  version: typeof PROTOCOL_VERSION;
  requestId: string;
  type: RequestType;
  input?: string;
  turnId?: string;
  planId?: string;
  providerId?: string;
  model?: string;
  connection?: ProviderConnection;
};

export type EngineEvent = {
  version: typeof PROTOCOL_VERSION;
  requestId?: string;
  type: string;
  turnId?: string;
  planId?: string;
  message?: string;
  error?: string;
  provider?: string;
  model?: string;
  models?: ModelOption[];
  activity?: Activity;
  actions?: Action[];
  presets?: ProviderPreset[];
};

export function isEngineEvent(value: unknown): value is EngineEvent {
  if (typeof value !== "object" || value === null) return false;
  const event = value as Record<string, unknown>;
  return event.version === PROTOCOL_VERSION && typeof event.type === "string";
}
