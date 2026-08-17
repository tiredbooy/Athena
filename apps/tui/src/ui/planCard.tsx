import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";
import { actionSummary } from "./actionSummary.js";
import type { Plan, PlanDecision } from "./types.js";

export function ApprovalCard({ plan, decision, focused }: { plan: Plan; decision: PlanDecision; focused: boolean }): React.ReactElement {
  const theme = useTheme();
  const borderColor = decision === "waiting" ? focused ? theme.accent : theme.muted : decision === "applying" ? theme.rail : theme.muted;
  const heading = decision === "waiting"
    ? focused ? "ACTION REVIEW · FOCUSED" : "ACTION REVIEW · TAB TO RETURN"
    : decision === "applying"
      ? "APPLYING APPROVED CHANGES…"
      : "DISCARDING PROPOSED CHANGES…";
  const instruction = decision === "waiting"
    ? focused ? "Y / Enter apply · N / R discard · Esc back" : "Tab focuses this card · /cancel discards"
    : decision === "applying"
      ? "Athena is carrying out these changes now"
      : "Athena is clearing this proposal now";
  return <Box flexDirection="column" borderStyle="double" borderColor={borderColor} paddingX={1} marginBottom={1}>
    <Box justifyContent="space-between"><Text color={borderColor} bold>{heading}</Text><Text color={theme.muted}>{plan.actions.length} change(s)</Text></Box>
    {plan.actions.map((action, index) => <Text key={`${action.type}-${index}`} color={theme.text}>  · {actionSummary(action)}</Text>)}
    <Text color={decision === "waiting" ? theme.accent : theme.rail}>{instruction}</Text>
  </Box>;
}
