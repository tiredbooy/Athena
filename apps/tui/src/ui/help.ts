import { commands } from "./palette.js";

export const helpShortcutLines = [
  "Enter send · Shift+Enter newline · Esc cancel turn",
  "/cancel discard plan · Y / N plan review · Tab return",
  "PgUp/PgDn scroll · Ctrl+↑↓ user turns · click or Ctrl+O fold",
  "/vim-mode · Esc normal · i insert · j/k turns · g/G top/bottom",
];

export function helpCommandLines(): string[] {
  return commands.map((command) => `${command.name.padEnd(10)} ${command.description}`);
}

export function helpPanelRows(): number {
  // Border, heading, shortcut block, command block, and the Esc hint.
  return 5 + helpShortcutLines.length + helpCommandLines().length;
}

export const localCommands = new Set(["/help", "/clear", "/theme", "/vim-mode"]);

export function isLocalCommand(input: string): boolean {
  return localCommands.has(input);
}
