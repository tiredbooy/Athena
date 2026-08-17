import { createContext, createElement, useContext, type ReactNode } from "react";

export type ThemeName = "midnight" | "ocean" | "system";

export type Theme = {
  name: ThemeName;
  label: string;
  detail: string;
  bg?: string;
  text: string;
  muted: string;
  accent: string;
  rail: string;
  error: string;
  success: string;
  border: string;
  spinner: string;
};

export const themeNames: ThemeName[] = ["midnight", "ocean", "system"];

export const themes: Record<ThemeName, Theme> = {
  midnight: {
    name: "midnight",
    label: "midnight",
    detail: "purple dark chrome",
    bg: "#12101a",
    text: "#eceaf3",
    muted: "#8b8798",
    accent: "#7b6cf6",
    rail: "#5f5fdf",
    error: "#ff6b81",
    success: "#8fca7a",
    border: "#3a3748",
    spinner: "#7b6cf6",
  },
  ocean: {
    name: "ocean",
    label: "ocean",
    detail: "cyan and blue chrome",
    bg: "#0b161c",
    text: "#c8eef8",
    muted: "#6d8f99",
    accent: "#2ec8b4",
    rail: "#3db8d4",
    error: "#ff6b81",
    success: "#7dcea0",
    border: "#1a6b85",
    spinner: "#2ec8b4",
  },
  system: {
    name: "system",
    label: "system",
    detail: "terminal ANSI slots",
    text: "white",
    muted: "gray",
    accent: "cyan",
    rail: "blue",
    error: "red",
    success: "green",
    border: "blue",
    spinner: "cyan",
  },
};

// Named ANSI only. Hex and a painted background are what turn midnight into
// dark-on-dark when tmux or SSH has no truecolor.
const ansi16: Record<ThemeName, Theme> = {
  midnight: {
    name: "midnight",
    label: "midnight",
    detail: "purple dark chrome",
    text: "white",
    muted: "gray",
    accent: "magenta",
    rail: "blue",
    error: "red",
    success: "green",
    border: "magenta",
    spinner: "magenta",
  },
  ocean: {
    name: "ocean",
    label: "ocean",
    detail: "cyan and blue chrome",
    text: "white",
    muted: "gray",
    accent: "cyan",
    rail: "blue",
    error: "red",
    success: "green",
    border: "blue",
    spinner: "cyan",
  },
  system: themes.system,
};

export type ColorDepth = 16 | 256 | 24;

export function detectColorDepth(env: NodeJS.ProcessEnv = process.env): ColorDepth {
  if (env.NO_COLOR) return 16;
  switch (env.FORCE_COLOR) {
    case "0":
      return 16;
    case "1":
      return 16;
    case "2":
      return 256;
    case "3":
      return 24;
  }
  const colorterm = (env.COLORTERM ?? "").toLowerCase();
  if (colorterm === "truecolor" || colorterm === "24bit") return 24;
  const term = env.TERM ?? "";
  if (/256/.test(term) || term.includes("direct")) return 256;
  return 16;
}

export function resolveTheme(name: ThemeName, env: NodeJS.ProcessEnv = process.env): Theme {
  if (name === "system" || detectColorDepth(env) > 16) return themes[name];
  return ansi16[name];
}

export type ThemeFlow = { selectedIndex: number; saved: ThemeName };

const ThemeContext = createContext<Theme>(themes.midnight);

export function ThemeProvider({ theme, children }: { theme: Theme; children: ReactNode }) {
  return createElement(ThemeContext.Provider, { value: theme }, children);
}

export function useTheme(): Theme {
  return useContext(ThemeContext);
}

export function isThemeName(value: string): value is ThemeName {
  return themeNames.includes(value as ThemeName);
}

export function themeIndex(name: ThemeName): number {
  return Math.max(0, themeNames.indexOf(name));
}
