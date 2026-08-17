import type { Action } from "../protocol/types.js";
import { graphResultLine } from "./graphResult.js";

export function actionSummary(action: Action): string {
  const sent = action.summary?.trim();
  if (sent) return sent;
  const graph = graphResultLine(action);
  if (graph) return graph;
  const target = actionTarget(action);
  const kind = action.type.replaceAll("_", " ").trim() || "action";
  return target ? `${kind} → ${target}` : kind;
}

function actionTarget(action: Action): string {
  const parts: string[] = [];
  if (action.paths?.length) parts.push(action.paths.join(", "));
  else if (action.folders?.length) parts.push(action.folders.join(" ↔ "));
  else if (action.folder) parts.push(action.folder);
  else if (action.path) parts.push(action.path);
  else if (action.title) parts.push(action.title);
  else if (action.note_id) parts.push(`note ${action.note_id}`);
  if (action.new_folder) parts.push(`parent ${action.new_folder}`);
  if (action.include_children) parts.push("direct subfolders");
  if (action.color?.trim()) parts.push(action.color.trim());
  if (action.node_size_multiplier) parts.push(`${action.node_size_multiplier.toFixed(2)}x`);
  return parts.join(" · ");
}
