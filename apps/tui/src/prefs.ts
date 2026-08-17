import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import type { ThemeName } from "./theme.js";

const themeNames: readonly ThemeName[] = ["midnight", "ocean", "system"];

export type LastModel = { providerId: string; model: string };

export type UiPrefs = {
  theme: ThemeName;
  providerId?: string;
  model?: string;
  remoteVaultWarned?: boolean;
  vimMode?: boolean;
};

export function defaultPrefsPath(): string {
  const xdg = process.env.XDG_CONFIG_HOME?.trim();
  const base = xdg ? join(xdg, "athena") : join(homedir(), ".config", "athena");
  return join(base, "ui.json");
}

export function parseThemeName(value: unknown): ThemeName | undefined {
  return typeof value === "string" && themeNames.includes(value as ThemeName) ? value as ThemeName : undefined;
}

export function loadUiPrefs(path = defaultPrefsPath()): UiPrefs {
  const raw = readRaw(path);
  return {
    theme: parseThemeName(raw.theme) ?? "midnight",
    providerId: parseNonEmpty(raw.providerId),
    model: parseNonEmpty(raw.model),
    remoteVaultWarned: raw.remoteVaultWarned === true,
    vimMode: raw.vimMode === true,
  };
}

export function saveUiPrefs(patch: Partial<UiPrefs>, path = defaultPrefsPath()): void {
  const raw = readRaw(path);
  if (patch.theme !== undefined) raw.theme = patch.theme;
  if (patch.providerId !== undefined) raw.providerId = patch.providerId;
  if (patch.model !== undefined) raw.model = patch.model;
  if (patch.remoteVaultWarned !== undefined) raw.remoteVaultWarned = patch.remoteVaultWarned;
  if (patch.vimMode !== undefined) raw.vimMode = patch.vimMode;
  writeRaw(path, raw);
}

export function loadThemeName(path = defaultPrefsPath()): ThemeName {
  return loadUiPrefs(path).theme;
}

export function saveThemeName(name: ThemeName, path = defaultPrefsPath()): void {
  saveUiPrefs({ theme: name }, path);
}

export function loadLastModel(path = defaultPrefsPath()): LastModel | undefined {
  const prefs = loadUiPrefs(path);
  if (!prefs.providerId || !prefs.model) return undefined;
  return { providerId: prefs.providerId, model: prefs.model };
}

export function saveLastModel(last: LastModel, path = defaultPrefsPath()): void {
  saveUiPrefs({ providerId: last.providerId, model: last.model }, path);
}

export function loadRemoteVaultWarned(path = defaultPrefsPath()): boolean {
  return loadUiPrefs(path).remoteVaultWarned === true;
}

export function saveRemoteVaultWarned(path = defaultPrefsPath()): void {
  saveUiPrefs({ remoteVaultWarned: true }, path);
}

export function loadVimMode(path = defaultPrefsPath()): boolean {
  return loadUiPrefs(path).vimMode === true;
}

export function saveVimMode(enabled: boolean, path = defaultPrefsPath()): void {
  saveUiPrefs({ vimMode: enabled }, path);
}

// Restore only when hello disagrees. Same model means the engine already
// brought the last choice back from its own config.
export function shouldRestoreModel(saved: LastModel | undefined, ready: { model?: string }): boolean {
  if (!saved?.providerId || !saved.model) return false;
  return saved.model !== (ready.model ?? "").trim();
}

function parseNonEmpty(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

function readRaw(path: string): Record<string, unknown> {
  try {
    const value = JSON.parse(readFileSync(path, "utf8")) as unknown;
    return typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

function writeRaw(path: string, raw: Record<string, unknown>): void {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, `${JSON.stringify(raw, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
}
