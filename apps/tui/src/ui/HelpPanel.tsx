import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";
import { helpCommandLines, helpShortcutLines } from "./help.js";

export function HelpPanel(): React.ReactElement {
  const theme = useTheme();
  return <Box flexDirection="column" borderStyle="double" borderColor={theme.border} paddingX={1} marginTop={1}>
    <Text color={theme.accent} bold>HELP</Text>
    {helpShortcutLines.map((line) => <Text key={line} color={theme.text}>{line}</Text>)}
    <Text> </Text>
    {helpCommandLines().map((line) => <Text key={line} color={theme.rail}>{line}</Text>)}
    <Text color={theme.muted}>Esc close</Text>
  </Box>;
}
