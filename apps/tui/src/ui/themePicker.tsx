import React from "react";
import { Box, Text } from "ink";
import { themeNames, themes, useTheme, type ThemeFlow } from "../theme.js";

export function ThemePanel({ flow }: { flow: ThemeFlow }): React.ReactElement {
  const theme = useTheme();
  return <Box flexDirection="column" borderStyle="double" borderColor={theme.border} paddingX={1} marginTop={1}>
    <Box justifyContent="space-between"><Text color={theme.accent} bold>THEME</Text><Text color={theme.muted}>{flow.selectedIndex + 1}/{themeNames.length}</Text></Box>
    {themeNames.map((name, index) => {
      const option = themes[name];
      const selected = index === flow.selectedIndex;
      return <Text key={name} color={selected ? theme.accent : theme.text}>
        {selected ? "❯ " : "  "}{option.label}<Text color={theme.muted}> · {option.detail}{name === flow.saved ? " · saved" : ""}</Text>
      </Text>;
    })}
    <Text color={theme.muted}>↑↓ preview · Enter save · Esc revert</Text>
  </Box>;
}
