import { spawn } from "node:child_process";

export function firstURL(value: string): string | undefined {
  const match = value.match(/https?:\/\/[^\s<>"']+/i);
  return match?.[0]?.replace(/[),.;]+$/, "");
}

export async function copyToClipboard(text: string, stdout: NodeJS.WriteStream): Promise<boolean> {
  for (const hostCommand of hostClipboardCommands()) {
    if (await runClipboardCommand(hostCommand.command, hostCommand.args, text)) return true;
  }
  if (stdout.isTTY) {
    stdout.write(osc52Copy(text));
    return true;
  }
  return false;
}

function osc52Copy(text: string): string {
  return `\u001b]52;c;${Buffer.from(text, "utf8").toString("base64")}\u0007`;
}

function hostClipboardCommands(): Array<{ command: string; args: string[] }> {
  if (process.platform === "darwin") return [{ command: "pbcopy", args: [] }];
  if (process.platform === "win32") return [{ command: "clip", args: [] }];
  if (process.env.WAYLAND_DISPLAY) return [{ command: "wl-copy", args: [] }];
  if (process.env.DISPLAY) {
    return [
      { command: "xclip", args: ["-selection", "clipboard"] },
      { command: "xsel", args: ["--clipboard", "--input"] },
    ];
  }
  return [];
}

function runClipboardCommand(command: string, args: string[], text: string): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (success: boolean) => {
      if (settled) return;
      settled = true;
      resolve(success);
    };
    let child;
    try {
      child = spawn(command, args, { stdio: ["pipe", "ignore", "ignore"] });
    } catch {
      finish(false);
      return;
    }
    child.once("error", () => finish(false));
    child.once("close", (code) => finish(code === 0));
    child.stdin.end(text);
  });
}
