import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { loadThemeName, parseThemeName, saveThemeName } from "./prefs.js";
import { detectColorDepth, isThemeName, resolveTheme, themeNames, themes } from "./theme.js";

test("midnight, ocean, and system each expose the required tokens", () => {
  for (const name of themeNames) {
    const theme = themes[name];
    assert.equal(theme.name, name);
    for (const token of ["text", "muted", "accent", "rail", "error", "success", "border", "spinner"] as const) {
      assert.ok(theme[token], `${name}.${token} is required`);
    }
  }
  assert.ok(themes.midnight.bg?.startsWith("#"));
  assert.ok(themes.ocean.bg?.startsWith("#"));
  assert.equal(themes.system.bg, undefined);
  assert.equal(themes.system.accent, "cyan");
  assert.equal(isThemeName("midnight"), true);
  assert.equal(isThemeName("solarized"), false);
});

test("system uses ANSI slot names so the terminal palette wins", () => {
  const hex = /#[0-9a-f]{3,8}/i;
  for (const token of ["text", "muted", "accent", "rail", "error", "success", "border", "spinner"] as const) {
    assert.equal(hex.test(themes.system[token]), false, `system.${token} must be an ANSI slot`);
  }
});

test("midnight on 16-color drops hex and a painted background", () => {
  assert.equal(detectColorDepth({ TERM: "xterm", COLORTERM: "" }), 16);
  assert.equal(detectColorDepth({ TERM: "screen", COLORTERM: "" }), 16);
  assert.equal(detectColorDepth({ TERM: "xterm-256color" }), 256);
  assert.equal(detectColorDepth({ COLORTERM: "truecolor", TERM: "tmux" }), 24);

  const midnight = resolveTheme("midnight", { TERM: "xterm" });
  assert.equal(midnight.bg, undefined);
  assert.equal(midnight.text, "white");
  assert.equal(midnight.muted, "gray");
  assert.equal(midnight.accent, "magenta");
  assert.equal(/#[0-9a-f]/i.test(JSON.stringify(midnight)), false);

  const rich = resolveTheme("midnight", { COLORTERM: "truecolor" });
  assert.ok(rich.bg?.startsWith("#"));
  assert.equal(resolveTheme("system", { TERM: "xterm" }).accent, "cyan");
});

test("prefs persist the last theme and ignore unknown values", () => {
  const dir = mkdtempSync(join(tmpdir(), "athena-ui-"));
  const path = join(dir, "ui.json");
  assert.equal(loadThemeName(path), "midnight");
  saveThemeName("ocean", path);
  assert.equal(loadThemeName(path), "ocean");
  writeFileSync(path, JSON.stringify({ theme: "solarized", extra: true }));
  assert.equal(loadThemeName(path), "midnight");
  assert.equal(parseThemeName("system"), "system");
  assert.equal(parseThemeName(1), undefined);
});
