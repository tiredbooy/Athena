import type { Activity, EngineEvent } from "../protocol/types.js";
import { graphResultLine } from "./graphResult.js";
import type { ActivityBlock, TranscriptMessage } from "../transcript.js";
import type { ThemeFlow } from "../theme.js";
import { helpPanelRows } from "./help.js";
import type { Command, ConnectFlow, ModelFlow, Plan } from "./types.js";

export type WorkKind = ActivityBlock["kind"];

export function formatActivity(activity: NonNullable<EngineEvent["activity"]>): string {
  if (activity.phase === "provider_wait") return "Generating a response";
  if (activity.tool && activity.state === "started") {
    return activity.target ? `${friendlyAction(activity.tool)} · ${activity.target}` : friendlyAction(activity.tool);
  }
  if (activity.path) return `${capitalize(activity.phase)} ${activity.path}`;
  return activity.message;
}

export function shortModel(model: string): string {
  const clean = model.split("/").pop()?.trim() ?? model.trim();
  return clean.length > 34 ? `${clean.slice(0, 33)}…` : clean;
}

export function showActivity(status: string, activity: string, hasLiveWork = false): boolean {
  if (hasLiveWork && (status === "working" || status === "ready")) return false;
  return status !== "ready" || (activity !== "Ready" && activity !== "Ready · your vault stays local");
}

export function reservedRows(plan: Plan | undefined, connectFlow: ConnectFlow | undefined, modelFlow: ModelFlow | undefined, suggestions: Command[], activityShown: boolean, themeFlow?: ThemeFlow, helpOpen = false, engineBanner = false): number {
  // Root padding, header, transcript margin, composer, and footer consume nine
  // rows before optional panels are rendered.
  let rows = 9;
  if (activityShown) rows += 2;
  if (suggestions.length > 0) rows += suggestions.length + 2;
  if (plan) rows += plan.actions.length + 5;
  if (themeFlow) rows += 8;
  if (helpOpen) rows += helpPanelRows();
  if (engineBanner) rows += 6;
  if (modelFlow) rows += Math.min(8, modelFlow.options.length + 3) + 5;
  if (connectFlow) {
    if (connectFlow.step === "providers") rows += connectFlow.presets.length + 5;
    else if (connectFlow.step === "oauth") rows += connectFlow.oauthLines.length + 5;
    else if (connectFlow.step === "remote-warning") rows += 7;
    else rows += 6;
  }
  return rows;
}

export function workKind(activity: Activity): WorkKind | undefined {
  const phase = activity.phase.toLowerCase();
  const tool = (activity.tool ?? "").toLowerCase();
  if (phase === "provider_wait" || phase === "planning" || phase === "replanning" || phase === "approval" || phase === "completed") {
    return undefined;
  }
  if (phase === "searching" || phase === "embedding") return "search";
  if (phase === "reading") return "read";
  if (phase === "executing" || phase === "observing") return "write";
  if (phase === "validating" || phase === "verifying") return "work";
  if (tool) {
    if (/search|embed/.test(tool)) return "search";
    if (/read|get_note|inventory|list/.test(tool)) return "read";
    if (/write|create|edit|update|move|delete|trash|restore|archiv|color|graph/.test(tool)) return "write";
    return "work";
  }
  if (activity.path) return "read";
  return undefined;
}

export function isWorkActivity(activity: Activity): boolean {
  return workKind(activity) !== undefined;
}

export function activityKey(activity: Activity): string {
  if (activity.run_id && activity.step) return `${activity.run_id}:${activity.step}:${activity.tool ?? activity.phase}`;
  if (activity.tool) return `tool:${activity.tool}:${activity.target ?? activity.path ?? ""}`;
  if (activity.path) return `${activity.phase}:${activity.path}`;
  return `${activity.phase}:${activity.message}`;
}

export function activityTitle(activity: Activity): string {
  const graph = graphResultLine({ tool: activity.tool, target: activity.target, path: activity.path });
  if (graph) return graph;
  const kind = workKind(activity);
  const label = activity.tool
    ? friendlyAction(activity.tool)
    : kind === "search" ? "Search"
    : kind === "read" ? "Read"
    : kind === "write" ? "Write"
    : capitalize(activity.phase || "Work");
  const target = activity.target || activity.path;
  if (target) return `${label} · ${target}`;
  if (activity.message) return activity.message;
  return label;
}

export function activityDetail(activity: Activity): string {
  const title = activityTitle(activity);
  if (!activity.message || activity.message === title || title.endsWith(` · ${activity.message}`)) return "";
  return activity.message;
}

export function activityRunning(activity: Activity): boolean {
  const state = (activity.state ?? "").toLowerCase();
  return state !== "succeeded" && state !== "failed" && state !== "completed";
}

export function canFoldActivity(block: Pick<ActivityBlock, "detail">): boolean {
  return block.detail.length > 0;
}

export function activityText(block: ActivityBlock): string {
  const marker = canFoldActivity(block) ? (block.folded ? "▸ " : "▾ ") : "";
  if (block.folded || !canFoldActivity(block)) return marker + block.title;
  return `${marker}${block.title}\n${block.detail}`;
}

export function applyActivity(messages: TranscriptMessage[], activity: Activity): TranscriptMessage[] {
  const kind = workKind(activity);
  if (!kind) return messages;
  const key = activityKey(activity);
  const block: ActivityBlock = {
    key,
    kind,
    phase: activity.phase,
    title: activityTitle(activity),
    detail: activityDetail(activity),
    state: activity.state,
    running: activityRunning(activity),
    folded: !activityRunning(activity),
  };
  const existingIndex = lastIndex(messages, (message) => message.role === "activity" && message.activity?.key === key);
  if (existingIndex >= 0) {
    const previous = messages[existingIndex];
    const next = {
      ...block,
      folded: previous.activity && !block.running ? previous.activity.folded : block.folded,
    };
    return messages.map((message, index) => index === existingIndex
      ? { ...message, text: activityText(next), activity: next }
      : message);
  }
  const settled = settleRunningActivity(messages);
  const id = settled.reduce((max, message) => Math.max(max, message.id), 0) + 1;
  return [...settled, { id, role: "activity", text: activityText(block), activity: block }];
}

export function finishActivityBlocks(messages: TranscriptMessage[]): TranscriptMessage[] {
  return settleRunningActivity(messages);
}

export function toggleActivityFold(messages: TranscriptMessage[], id: number): TranscriptMessage[] {
  return messages.map((message) => {
    if (message.id !== id || message.role !== "activity" || !message.activity || !canFoldActivity(message.activity)) return message;
    const activity = { ...message.activity, folded: !message.activity.folded };
    return { ...message, activity, text: activityText(activity) };
  });
}

export function lastFoldableActivityId(messages: TranscriptMessage[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (message.role === "activity" && message.activity && canFoldActivity(message.activity)) return message.id;
  }
  return undefined;
}

export function hasLiveWork(messages: TranscriptMessage[]): boolean {
  return messages.some((message) => message.role === "activity" && message.activity?.running);
}

function settleRunningActivity(messages: TranscriptMessage[]): TranscriptMessage[] {
  return messages.map((message) => {
    if (message.role !== "activity" || !message.activity?.running) return message;
    const activity = { ...message.activity, running: false, folded: true };
    return { ...message, activity, text: activityText(activity) };
  });
}

function lastIndex<T>(items: T[], match: (item: T) => boolean): number {
  for (let index = items.length - 1; index >= 0; index--) {
    if (match(items[index])) return index;
  }
  return -1;
}

function friendlyAction(action: string): string {
  return action
    .split("_")
    .filter(Boolean)
    .map(capitalize)
    .join(" ");
}

function capitalize(value: string): string {
  return value.length === 0 ? value : value[0].toUpperCase() + value.slice(1);
}
