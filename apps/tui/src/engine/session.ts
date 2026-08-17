import type { EngineEvent } from "../protocol/types.js";
import type { TranscriptMessage } from "../transcript.js";
import {
  applyActivity,
  finishActivityBlocks,
  formatActivity,
  isWorkActivity,
  shortModel,
} from "../ui/activity.js";
import { graphResultLines } from "../ui/graphResult.js";
import type { ConnectFlow, ModelFlow, Plan, PlanDecision } from "../ui/types.js";

export type SessionStatus = "starting" | "ready" | "working" | "approval" | "error";

export type SessionState = {
  messages: TranscriptMessage[];
  activity: string;
  modelName: string;
  status: SessionStatus;
  connected: boolean;
  hasModel: boolean;
  turnID?: string;
  plan?: Plan;
  planDecision: PlanDecision;
  reviewing: boolean;
  error?: string;
  connectFlow?: ConnectFlow;
  modelFlow?: ModelFlow;
};

// Engine events are the source of truth. App.tsx applies this state; it does
// not re-parse activity English or invent a second event story.
export function initialSessionState(): SessionState {
  return {
    messages: [],
    activity: "Connecting to the local engine…",
    modelName: "local model",
    status: "starting",
    connected: false,
    hasModel: false,
    planDecision: "waiting",
    reviewing: false,
  };
}

export function reduceEvent(state: SessionState, event: EngineEvent): SessionState {
  switch (event.type) {
    case "engine.ready":
      return withModel({ ...state, status: "ready", activity: "Ready", connected: true }, event.provider, event.model);
    case "turn.started":
      return { ...state, turnID: event.turnId, status: "working", error: undefined };
    case "activity": {
      if (!event.activity) return state;
      let next: SessionState = { ...state, activity: formatActivity(event.activity) };
      next = withModel(next, event.activity.provider, event.activity.model);
      if (isWorkActivity(event.activity)) next.messages = applyActivity(next.messages, event.activity);
      return next;
    }
    case "response.delta":
      if (!event.message) return state;
      return { ...state, messages: applyResponseDelta(state.messages, event.message), status: "working" };
    case "response": {
      const settled = finishActivityBlocks(state.messages);
      return {
        ...state,
        messages: finishAssistant(settled, event.message),
        status: "ready",
        activity: "Ready",
      };
    }
    case "plan.ready":
      return {
        ...state,
        messages: finishAssistant(finishActivityBlocks(state.messages)),
        plan: event.planId ? { id: event.planId, actions: event.actions ?? [] } : state.plan,
        planDecision: "waiting",
        reviewing: true,
        turnID: undefined,
        error: undefined,
        status: "approval",
        activity: "Waiting for your approval",
      };
    case "session.reset":
      return {
        ...state,
        messages: [],
        error: undefined,
        plan: undefined,
        planDecision: "waiting",
        reviewing: false,
        turnID: undefined,
        status: "ready",
        activity: event.message ?? "Session reset",
      };
    case "turn.completed":
      return { ...state, messages: finishAssistant(finishActivityBlocks(state.messages)), turnID: undefined };
    case "turn.cancelled":
      return {
        ...state,
        messages: finishAssistant(finishActivityBlocks(state.messages)),
        status: "ready",
        turnID: undefined,
        activity: "Turn cancelled",
      };
    case "turn.failed":
      return recordError(
        { ...state, messages: finishAssistant(finishActivityBlocks(state.messages)), turnID: undefined },
        event.error ?? "The engine could not complete that turn.",
        { status: "error" },
      );
    case "plan.approved":
    case "plan.rejected": {
      const reports = event.type === "plan.approved" ? graphResultLines(state.plan?.actions ?? []) : [];
      let next: SessionState = {
        ...state,
        plan: undefined,
        planDecision: "waiting",
        reviewing: false,
        status: "ready",
        activity: event.type === "plan.approved" ? "Changes applied" : "Changes discarded",
      };
      for (const line of reports) next = appendTranscript(next, "assistant", line);
      if (event.message) next = appendTranscript(next, "assistant", event.message);
      return next;
    }
    case "provider.presets":
      return {
        ...state,
        connectFlow: {
          step: "providers",
          presets: event.presets ?? [],
          selectedIndex: 0,
          values: { name: "", type: "", base_url: "", chat_model: "" },
          oauthLines: [],
          fields: [],
        },
        status: "ready",
        activity: "Choose a provider",
      };
    case "provider.oauth.started":
      return {
        ...state,
        turnID: event.turnId,
        status: "working",
        activity: "Waiting for provider sign-in",
        connectFlow: state.connectFlow ? { ...state.connectFlow, step: "oauth" } : state.connectFlow,
      };
    case "provider.oauth.progress":
      if (!event.message || !state.connectFlow) return state;
      return {
        ...state,
        connectFlow: {
          ...state.connectFlow,
          oauthLines: [...state.connectFlow.oauthLines, event.message].slice(-8),
        },
      };
    case "provider.oauth.cancelled":
      return {
        ...state,
        turnID: undefined,
        status: "ready",
        connectFlow: undefined,
        activity: "Sign-in cancelled",
      };
    case "provider.connected": {
      let next: SessionState = {
        ...state,
        turnID: undefined,
        connectFlow: undefined,
        status: "ready",
        activity: "Provider connected",
      };
      next = withModel(next, event.provider, event.model);
      if (event.message) next = appendTranscript(next, "assistant", event.message);
      return next;
    }
    case "model.options":
      return {
        ...state,
        modelFlow: {
          options: event.models ?? [],
          selectedIndex: Math.max(0, (event.models ?? []).findIndex((option) => option.current)),
          selecting: false,
        },
        status: "ready",
        activity: "Choose a model",
      };
    case "model.selected": {
      return withModel({
        ...state,
        modelFlow: undefined,
        status: "ready",
        activity: event.message ?? "Model changed",
      }, event.provider, event.model);
    }
    case "error": {
      const message = event.error ?? "The engine rejected the request.";
      let next: SessionState = event.turnId ? { ...state, turnID: undefined } : state;
      if (event.planId) {
        return recordError(next, message, {
          planDecision: "waiting",
          reviewing: true,
          status: "approval",
          activity: "Approval failed — choose again",
        });
      }
      return recordError(next, message, { status: "error" });
    }
    default:
      return state;
  }
}

export function reduceDiagnostic(state: SessionState, line: string): SessionState {
  const last = state.messages[state.messages.length - 1];
  if (last?.role === "diagnostic" && last.text === line) return state;
  return appendTranscript(state, "diagnostic", line);
}

export function reduceEngineClose(state: SessionState, reason: Error): SessionState {
  return recordError(state, reason.message, { status: "error", connected: false, turnID: undefined });
}

export const reconnectExplanation = "The engine restarted. This pane still shows the old transcript; the engine started a new session. Pending plans, in-flight turns, and engine history are gone.";

export function beginReconnect(state: SessionState, reason: Error): SessionState {
  let next: SessionState = {
    ...state,
    connected: false,
    status: "starting",
    turnID: undefined,
    plan: undefined,
    planDecision: "waiting",
    reviewing: false,
    connectFlow: undefined,
    modelFlow: undefined,
    error: undefined,
    activity: "Reconnecting to the local engine…",
  };
  next = reduceDiagnostic(next, reason.message);
  return reduceDiagnostic(next, reconnectExplanation);
}

function withModel(state: SessionState, provider?: string, model?: string): SessionState {
  if (!provider || !model) return state;
  return { ...state, modelName: `${provider} · ${shortModel(model)}`, hasModel: true };
}

export function reduceProtocolError(state: SessionState, reason: Error): SessionState {
  return recordError(state, reason.message);
}

export function appendTranscript(state: SessionState, role: TranscriptMessage["role"], text: string): SessionState {
  return { ...state, messages: appendMessage(state.messages, role, text) };
}

export function clearTranscript(state: SessionState): SessionState {
  return { ...state, messages: [], error: undefined };
}

export function recordError(state: SessionState, message: string, extras: Partial<SessionState> = {}): SessionState {
  const next: SessionState = { ...state, ...extras, error: message };
  if (state.error === message) return next;
  return appendTranscript(next, "error", message);
}

function appendMessage(messages: TranscriptMessage[], role: TranscriptMessage["role"], text: string): TranscriptMessage[] {
  const id = messages.reduce((max, message) => Math.max(max, message.id), 0) + 1;
  return [...messages, { id, role, text }];
}

function applyResponseDelta(messages: TranscriptMessage[], chunk: string): TranscriptMessage[] {
  const last = messages[messages.length - 1];
  if (last?.role === "assistant" && last.streaming) {
    return messages.map((message, index) => index === messages.length - 1 ? { ...message, text: message.text + chunk } : message);
  }
  const id = messages.reduce((max, message) => Math.max(max, message.id), 0) + 1;
  return [...messages, { id, role: "assistant", text: chunk, streaming: true }];
}

function finishAssistant(messages: TranscriptMessage[], text?: string): TranscriptMessage[] {
  const last = messages[messages.length - 1];
  if (last?.role === "assistant" && last.streaming) {
    return messages.map((message, index) => index === messages.length - 1
      ? { ...message, text: text ?? message.text, streaming: false }
      : message);
  }
  return text ? appendMessage(messages, "assistant", text) : messages;
}
