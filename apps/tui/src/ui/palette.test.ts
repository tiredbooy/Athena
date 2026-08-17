import assert from "node:assert/strict";
import test from "node:test";
import {
  commandSuggestions,
  commands,
  nextPaletteIndex,
  resolveCommandInput,
  selectedCommand,
} from "./palette.js";

test("slash with no suffix lists every command", () => {
  const listed = commandSuggestions("/");
  assert.deepEqual(listed.map((command) => command.name), commands.map((command) => command.name));
});

test("prefix filters commands and a space closes the palette", () => {
  assert.deepEqual(commandSuggestions("/c").map((command) => command.name), ["/clear", "/connect", "/compact", "/cancel"]);
  assert.deepEqual(commandSuggestions("/cancel"), [{ name: "/cancel", description: "discard pending plan or question" }]);
  assert.deepEqual(commandSuggestions("/cancel now"), []);
  assert.deepEqual(commandSuggestions("help"), []);
});

test("/cancel is labeled as discarding a pending plan or question", () => {
  const cancel = commands.find((command) => command.name === "/cancel");
  assert.equal(cancel?.description, "discard pending plan or question");
});

test("/clear is view-only and /reset clears engine session state", () => {
  const clear = commands.find((command) => command.name === "/clear");
  const reset = commands.find((command) => command.name === "/reset");
  assert.match(clear?.description ?? "", /view only/i);
  assert.match(reset?.description ?? "", /engine history/i);
});

test("arrows wrap and Enter runs the highlighted command, not the partial draft", () => {
  const suggestions = commandSuggestions("/c");
  assert.equal(nextPaletteIndex(0, suggestions.length, -1), suggestions.length - 1);
  assert.equal(nextPaletteIndex(suggestions.length - 1, suggestions.length, 1), 0);
  assert.equal(selectedCommand(suggestions, 3)?.name, "/cancel");
  assert.equal(resolveCommandInput("/c", suggestions, 3), "/cancel");
  assert.equal(resolveCommandInput("/unknown", [], 0), "/unknown");
});
