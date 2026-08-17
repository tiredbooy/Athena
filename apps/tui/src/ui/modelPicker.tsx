import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";
import { modelPickerRows } from "./catalog.js";
import type { ModelFlow } from "./types.js";

export function ModelPanel({ flow }: { flow: ModelFlow }): React.ReactElement {
  const theme = useTheme();
  const rows = modelPickerRows(flow.options);
  const selectedRow = rows.findIndex((row) => row.kind !== "header" && row.index === flow.selectedIndex);
  const limit = 8;
  const start = Math.min(Math.max(0, selectedRow - limit + 1), Math.max(0, rows.length - limit));
  const visible = rows.slice(start, start + limit);
  const count = flow.options.length + 1;
  return <Box flexDirection="column" borderStyle="double" borderColor={theme.border} paddingX={1} marginTop={1}>
    <Box justifyContent="space-between"><Text color={theme.accent} bold>CONNECTED PROVIDERS</Text><Text color={theme.muted}>{Math.min(flow.selectedIndex + 1, count)}/{count}</Text></Box>
    {flow.options.length === 0 && <Text color={theme.text}>No models yet. Connect a provider to continue.</Text>}
    {visible.map((row) => {
      if (row.kind === "header") return <Text key={row.key} color={theme.muted}>{row.label}</Text>;
      if (row.kind === "connect") {
        const selected = row.index === flow.selectedIndex;
        return <Text key={row.key} color={selected ? theme.accent : theme.text}>
          {selected ? "❯ " : "  "}Connect a new provider
        </Text>;
      }
      const selected = row.index === flow.selectedIndex;
      return <Text key={row.key} color={selected ? theme.accent : theme.text} wrap="truncate-end">
        {selected ? "❯ " : "  "}{row.option.model}<Text color={theme.muted}>{row.option.current ? " · current" : ""}</Text>
      </Text>;
    })}
    <Text color={flow.selecting ? theme.rail : theme.muted}>{flow.selecting ? "Switching model…" : "↑↓ choose · Enter select · Esc close"}</Text>
  </Box>;
}
