export type EmptyKind = "connecting" | "vault" | "engine-down" | "no-models";

export type EmptyState = {
  kind: EmptyKind;
  title: string;
  body: string;
  action: string;
};

const connecting: EmptyState = {
  kind: "connecting",
  title: "ENGINE",
  body: "Connecting to the local engine…",
  action: "Athena will be ready in this pane",
};

const vault: EmptyState = {
  kind: "vault",
  title: "VAULT",
  body: "This session is empty. Athena reads and writes your local vault — ask it to find, create, or organize notes.",
  action: "Type a request · /doctor inspects the vault · /help keys",
};

const engineDown: EmptyState = {
  kind: "engine-down",
  title: "ENGINE",
  body: "The local engine is not connected. If the process died, Athena retries on its own.",
  action: "Wait for reconnect · Ctrl+C quit",
};

const noModels: EmptyState = {
  kind: "no-models",
  title: "MODELS",
  body: "No model is connected yet. Athena needs a provider before it can answer.",
  action: "/connect or /models",
};

// Hello does not report note counts, so "vault" is the empty-session start
// card — not a claim that the filesystem is empty.
export function resolveEmptyState(input: {
  status: string;
  connected: boolean;
  hasModel: boolean;
  messageCount: number;
}): EmptyState | undefined {
  if (!input.connected && input.status === "error") return engineDown;
  if (input.messageCount > 0) return undefined;
  if (!input.connected) return connecting;
  if (!input.hasModel) return noModels;
  return vault;
}
