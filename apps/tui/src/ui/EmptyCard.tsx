import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";
import type { EmptyState } from "./empty.js";

export function EmptyCard({ state, banner = false }: { state: EmptyState; banner?: boolean }): React.ReactElement {
  const theme = useTheme();
  const color = state.kind === "engine-down" ? theme.error : state.kind === "no-models" ? theme.accent : theme.muted;
  return <Box flexDirection="column" marginTop={banner ? 1 : 0} borderStyle={banner ? "single" : undefined} borderColor={banner ? color : undefined} paddingX={banner ? 1 : 0}>
    <Text color={color} bold>{state.title}</Text>
    <Text color={theme.text}>{state.body}</Text>
    <Text color={theme.accent}>{state.action}</Text>
  </Box>;
}
