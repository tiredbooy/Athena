import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";

export const pulseFrames = ["◐", "◓", "◑", "◒"];
export const loadingPhrases = ["Cooking", "Pulling in the useful context", "Checking the vault", "Locking it in"];

export function Header(): React.ReactElement {
  const theme = useTheme();
  return <Text color={theme.accent} bold>A T H E N A</Text>;
}

export function ActivityLine({ activity, status, pulse, phrase }: { activity: string; status: string; pulse: string; phrase: string }): React.ReactElement {
  const theme = useTheme();
  const loading = status === "starting" || status === "working";
  return <Box marginTop={1}><Text wrap="truncate-end"><Text color={status === "error" ? theme.error : theme.spinner}>{pulse} </Text><Text color={theme.text}>{activity}</Text>{loading && <Text color={theme.rail}> · {phrase}…</Text>}</Text></Box>;
}

export function Footer({ prompt, model, scrollOffset, maxScrollOffset }: { prompt: string; model: string; scrollOffset: number; maxScrollOffset: number }): React.ReactElement {
  const theme = useTheme();
  const scroll = maxScrollOffset > 0
    ? `PgUp/PgDn · Ctrl+↑↓ turns${scrollOffset > 0 ? ` · ${scrollOffset} rows up` : ""}`
    : "";
  return <Box justifyContent="space-between"><Text color={theme.muted} wrap="truncate-end">{prompt} · {scroll ? scroll + " · " : ""}drag copy</Text><Text color={theme.muted} wrap="truncate-end">model · {model}</Text></Box>;
}
