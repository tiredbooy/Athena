import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../theme.js";
import type { ProviderConnection } from "../protocol/types.js";
import type { ConnectField } from "./catalog.js";
import { remoteVaultWarning } from "./remoteVault.js";
import type { ConnectFlow, ConnectStep } from "./types.js";

const fieldCopy: Record<ConnectField, { label: string; placeholder: string; help: string }> = {
  name: { label: "name", placeholder: "Provider name", help: "Give this connection a recognizable name." },
  base_url: { label: "URL", placeholder: "https://api.example.com/v1", help: "Enter the provider's API base URL." },
  api_key: { label: "secret", placeholder: "API key", help: "Typed secrets are masked and stored locally." },
  chat_model: { label: "model", placeholder: "Default model ID", help: "Enter the exact model ID Athena should use by default." },
};

export function ConnectPanel({ flow }: { flow: ConnectFlow }): React.ReactElement {
  const theme = useTheme();
  if (flow.step === "providers") {
    return <Box flexDirection="column" borderStyle="double" borderColor={theme.border} paddingX={1} marginTop={1}>
      <Text color={theme.accent} bold>CONNECT A MODEL PROVIDER</Text>
      {flow.presets.map((preset, index) => <Text key={preset.id} color={!preset.available ? theme.muted : index === flow.selectedIndex ? theme.accent : theme.text}>
        {index === flow.selectedIndex ? "❯ " : "  "}{preset.label} <Text color={theme.muted}>· {preset.detail}</Text>{!preset.available ? <Text color={theme.error}> · unavailable</Text> : null}
      </Text>)}
      <Text color={theme.muted}>↑↓ choose · Enter continue · secrets stay local</Text>
    </Box>;
  }
  if (flow.step === "remote-warning") {
    return <Box flexDirection="column" borderStyle="double" borderColor={theme.error} paddingX={1} marginTop={1}>
      <Text color={theme.error} bold>REMOTE PROVIDER</Text>
      <Text color={theme.text}>{remoteVaultWarning}</Text>
      <Text color={theme.accent}>This is the first remote connect. {flow.preset?.label ?? "This provider"} will see those reads.</Text>
      <Text color={theme.muted}>Y / Enter continue · Esc / N back</Text>
    </Box>;
  }
  if (flow.step === "oauth") {
    return <Box flexDirection="column" borderStyle="double" borderColor={theme.accent} paddingX={1} marginTop={1}>
      <Text color={theme.accent} bold>{(flow.preset?.label ?? "PROVIDER").toUpperCase()} · DEVICE LOGIN</Text>
      {flow.oauthLines.map((line, index) => <Text key={`${line}-${index}`} color={index === flow.oauthLines.length - 1 ? theme.text : theme.muted}>{line}</Text>)}
      <Text color={theme.rail}>The login URL is copied when available. Athena is waiting for confirmation.</Text>
    </Box>;
  }
  const current = flow.fields.indexOf(flow.step as ConnectField);
  return <Box flexDirection="column" borderStyle="double" borderColor={theme.border} paddingX={1} marginTop={1}>
    <Box justifyContent="space-between"><Text color={theme.accent} bold>{flow.step === "saving" ? "SAVING PROVIDER" : "PROVIDER DETAILS"}</Text><Text color={theme.muted}>{flow.preset?.label}</Text></Box>
    <Text color={theme.muted}>{flow.fields.map((field, index) => `${index === current ? "[" + fieldCopy[field].label + "]" : fieldCopy[field].label}`).join("  →  ") || "saving"}</Text>
    <Text color={theme.text}>{connectStepHelp(flow)}</Text>
  </Box>;
}

export function connectDefaultValue(step: ConnectStep, values: ProviderConnection): string {
  switch (step) {
    case "name":
      return values.name;
    case "base_url":
      return values.base_url;
    case "chat_model":
      return values.chat_model;
    default:
      return "";
  }
}

export function connectPlaceholder(flow?: ConnectFlow): string {
  if (!flow) return "Ask Athena…";
  if (flow.step === "providers") return "Press Enter to choose";
  if (flow.step === "remote-warning") return "Y continue · N back";
  if (flow.step === "oauth") return "Complete sign-in in your browser";
  if (flow.step === "saving") return "Saving provider…";
  if (flow.step === "api_key") {
    return flow.values.api_key_env ? `API key or Enter to use ${flow.values.api_key_env}` : "API key (optional for local APIs)";
  }
  return fieldCopy[flow.step].placeholder;
}

function connectStepHelp(flow: ConnectFlow): string {
  if (flow.step === "saving") return "The key is written to a user-only local credential file and never added to athena.yaml.";
  if (flow.step === "api_key") {
    return flow.values.api_key_env
      ? `Paste a key, or press Enter to read ${flow.values.api_key_env}. Typed secrets are masked and stored locally.`
      : "Paste a key, or press Enter for an API that needs no authentication. Typed secrets are masked and stored locally.";
  }
  if (flow.step === "name" || flow.step === "base_url" || flow.step === "chat_model") return fieldCopy[flow.step].help;
  return "";
}
