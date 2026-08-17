import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";
import { nextPaletteIndex, paletteHint } from "./palette.js";
import type { Command } from "./types.js";

export function CommandPalette({ suggestions, selectedIndex }: { suggestions: Command[]; selectedIndex: number }): React.ReactElement {
  const theme = useTheme();
  const selected = nextPaletteIndex(selectedIndex, suggestions.length, 0);
  return <Box flexDirection="column" marginTop={1}>
    <Text color={theme.muted}>{paletteHint}</Text>
    {suggestions.map((command, index) => <Text key={command.name} color={index === selected ? theme.accent : theme.rail}>
      {index === selected ? "❯ " : "  "}{command.name.padEnd(10)} {command.description}
    </Text>)}
  </Box>;
}
