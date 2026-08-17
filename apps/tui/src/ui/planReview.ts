export type ReviewKeyAction = "approve" | "reject" | "back" | "none";

export function reviewKeyAction(input: string, key: { return?: boolean; escape?: boolean }): ReviewKeyAction {
  if (key.escape) return "back";
  if (key.return || input.toLowerCase() === "y") return "approve";
  if (input.toLowerCase() === "n" || input.toLowerCase() === "r") return "reject";
  return "none";
}

// Chat phrases must not approve a plan. Only the focused review keys do.
export function composerApprovesPlan(input: string): boolean {
  void input;
  return false;
}
