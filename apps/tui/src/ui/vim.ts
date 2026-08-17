export type VimMotion = "insert" | "older" | "newer" | "top" | "bottom" | "none";

export function vimMotion(input: string): VimMotion {
  if (input === "i") return "insert";
  if (input === "k") return "older";
  if (input === "j") return "newer";
  if (input === "g") return "top";
  if (input === "G") return "bottom";
  return "none";
}
