export type RemoteHint = {
  type?: string;
  auth?: string;
  base_url?: string;
};

export const remoteVaultWarning = "Inventory, search, and get_note send vault text to this provider. That leaves this machine.";

export function isLoopbackUrl(url: string): boolean {
  return /^https?:\/\/(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?(?:\/|$)/i.test(url.trim());
}

// Ollama and loopback HTTP stay on this machine. Everything else is remote.
export function isRemoteProvider(input: RemoteHint): boolean {
  const type = (input.type ?? "").toLowerCase();
  if (type === "ollama") return false;
  if (isLoopbackUrl(input.base_url ?? "")) return false;
  if (input.auth === "none" && type === "") return false;
  return Boolean(type || input.auth === "oauth" || input.auth === "api_key" || input.base_url);
}

export function needsRemoteVaultWarning(input: RemoteHint, alreadyWarned: boolean): boolean {
  return !alreadyWarned && isRemoteProvider(input);
}
