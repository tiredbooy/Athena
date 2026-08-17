import type { Action } from "../protocol/types.js";

export type GraphFields = {
  type?: string;
  tool?: string;
  folder?: string;
  target?: string;
  path?: string;
  color?: string;
  node_size_multiplier?: number;
  include_children?: boolean;
};

export function isGraphOp(kind: string): boolean {
  const value = kind.toLowerCase();
  return value.includes("folder_color") || value.includes("graph_node");
}

// Folder and size come from existing action fields. Hex color is shown only
// when the engine sends it (not today; Claude G-02 / E-03 / F-01).
export function graphResultLine(fields: GraphFields): string | undefined {
  if (!isGraphOp(fields.type ?? fields.tool ?? "")) return undefined;
  const folder = fields.folder || fields.target || fields.path;
  const parts = ["Graph"];
  if (folder) parts.push(folder);
  if (fields.include_children) parts.push("direct subfolders");
  if (fields.color?.trim()) parts.push(fields.color.trim());
  if (fields.node_size_multiplier) parts.push(`${fields.node_size_multiplier.toFixed(2)}x`);
  return parts.length > 1 ? parts.join(" · ") : undefined;
}

export function graphResultLines(actions: Action[]): string[] {
  return actions.map((action) => graphResultLine(action)).filter((line): line is string => Boolean(line));
}
