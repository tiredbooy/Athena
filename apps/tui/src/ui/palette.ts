import type { Command } from "./types.js";

export const paletteHint = "↑↓ choose · Tab complete · Enter run · Esc close";

export const commands: Command[] = [
  { name: "/help", description: "show keys and commands" },
  { name: "/clear", description: "clear this transcript (view only)" },
  { name: "/reset", description: "clear engine history, plan, and question" },
  { name: "/doctor", description: "diagnose vault and providers" },
  { name: "/models", description: "list available models" },
  { name: "/connect", description: "connect a model provider" },
  { name: "/theme", description: "choose midnight, ocean, or system" },
  { name: "/compact", description: "compact conversation memory" },
  { name: "/cancel", description: "discard pending plan or question" },
  { name: "/vim-mode", description: "toggle vim keys for scrollback" },
];

export function commandSuggestions(draft: string): Command[] {
  if (!draft.startsWith("/") || draft.includes(" ")) return [];
  return commands.filter((command) => command.name.startsWith(draft.toLowerCase()));
}

export function nextPaletteIndex(current: number, count: number, delta: number): number {
  if (count <= 0) return 0;
  return (current + delta % count + count) % count;
}

export function selectedCommand(suggestions: Command[], index: number): Command | undefined {
  if (suggestions.length === 0) return undefined;
  return suggestions[nextPaletteIndex(index, suggestions.length, 0)];
}

export function resolveCommandInput(draft: string, suggestions: Command[], selectedIndex: number): string {
  return selectedCommand(suggestions, selectedIndex)?.name ?? draft.trim();
}
