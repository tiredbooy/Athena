import React, { useEffect, useMemo, useState } from "react";
import { Box, Text, render, useApp, useInput } from "ink";
import { EngineClient } from "./engine/EngineClient.js";
import type { Action, EngineEvent } from "./protocol/types.js";

type ChatMessage = { role: "user" | "assistant"; text: string };
type Plan = { id: string; actions: Action[] };

const amber = "#e4a853";
const ink = "#e9e2d0";
const dim = "#817b70";
const green = "#9fc17a";
const red = "#d87c72";

function App(): React.ReactElement {
  const { exit } = useApp();
  const [client] = useState(() => new EngineClient(process.env.ATHENA_ENGINE ?? "athena", ["engine"]));
  const [draft, setDraft] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [activity, setActivity] = useState("Connecting to the local engine…");
  const [status, setStatus] = useState("starting");
  const [turnID, setTurnID] = useState<string>();
  const [plan, setPlan] = useState<Plan>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const onEvent = (event: EngineEvent) => {
      switch (event.type) {
        case "engine.ready":
          setStatus("ready");
          setActivity("Ready · your vault stays local");
          return;
        case "turn.started":
          setTurnID(event.turnId);
          setStatus("working");
          setError(undefined);
          return;
        case "activity":
          if (event.activity) setActivity(formatActivity(event.activity));
          return;
        case "response":
          if (event.message) setMessages((current) => [...current, { role: "assistant", text: event.message! }]);
          setStatus("ready");
          setActivity("Ready");
          return;
        case "plan.ready":
          if (event.planId) setPlan({ id: event.planId, actions: event.actions ?? [] });
          setStatus("approval");
          setActivity("Review the proposed changes below");
          return;
        case "turn.completed":
          setTurnID(undefined);
          return;
        case "turn.cancelled":
          setStatus("ready");
          setTurnID(undefined);
          setActivity("Turn cancelled · your draft is still here");
          return;
        case "turn.failed":
          setStatus("error");
          setTurnID(undefined);
          setError(event.error ?? "The engine could not complete that turn.");
          return;
        case "plan.approved":
        case "plan.rejected":
          setPlan(undefined);
          setStatus("ready");
          setActivity(event.type === "plan.approved" ? "Changes applied" : "Changes discarded");
          if (event.message) setMessages((current) => [...current, { role: "assistant", text: event.message! }]);
          return;
        case "error":
          setError(event.error ?? "The engine rejected the request.");
          setStatus("error");
      }
    };
    const onClose = (reason: Error) => {
      setStatus("error");
      setError(reason.message);
    };
    const onProtocolError = (reason: Error) => setError(reason.message);
    client.on("event", onEvent);
    client.on("close", onClose);
    client.on("protocolError", onProtocolError);
    void client.hello().catch((reason: Error) => setError(reason.message));
    return () => {
      client.off("event", onEvent);
      client.off("close", onClose);
      client.off("protocolError", onProtocolError);
      client.dispose();
    };
  }, [client]);

  useInput((input, key) => {
    if (key.ctrl && input === "c") {
      client.dispose();
      exit();
      return;
    }
    if (plan) {
      if (input.toLowerCase() === "y" || key.return) {
        void client.approve(plan.id).catch((reason: Error) => setError(reason.message));
      } else if (input.toLowerCase() === "n" || key.escape) {
        void client.reject(plan.id).catch((reason: Error) => setError(reason.message));
      }
      return;
    }
    if (key.escape && turnID) {
      void client.cancel(turnID).catch((reason: Error) => setError(reason.message));
      setActivity("Cancellation requested…");
      return;
    }
    if (key.return) {
      if (key.shift) {
        setDraft((current) => `${current}\n`);
        return;
      }
      const inputText = draft.trim();
      if (!inputText || status === "working" || status === "starting") return;
      setMessages((current) => [...current, { role: "user", text: inputText }]);
      setDraft("");
      setError(undefined);
      setActivity("Sending to the local engine…");
      void client.submit(inputText).catch((reason: Error) => {
        setStatus("error");
        setError(reason.message);
      });
      return;
    }
    if (input) setDraft((current) => current + input);
    if (key.backspace) setDraft((current) => current.slice(0, -1));
  });

  const prompt = useMemo(() => {
    if (plan) return "  [Y] apply   [N] discard";
    if (turnID) return "  [Esc] cancel current turn";
    return "  [Enter] send   [Shift+Enter] newline   [Ctrl+C] quit";
  }, [plan, turnID]);

  return (
    <Box flexDirection="column" paddingX={2} paddingY={1}>
      <Box justifyContent="space-between">
        <Text color={amber} bold>A T H E N A</Text>
        <Text color={dim}>LOCAL ENGINE / TUI PREVIEW</Text>
      </Box>
      <Text color={dim}>────────────────────────────────────────────────────────────</Text>
      <Box flexDirection="column" marginTop={1}>
        {messages.length === 0 ? (
          <Text color={dim}>Ask Athena about your vault. Nothing leaves this process unless you configure a provider.</Text>
        ) : messages.map((message, index) => (
          <Box key={`${message.role}-${index}`} marginBottom={1}>
            <Text color={message.role === "user" ? amber : green} bold>{message.role === "user" ? "you  " : "owl  "}</Text>
            <Text color={ink}> {message.text}</Text>
          </Box>
        ))}
      </Box>
      {plan && <ApprovalCard plan={plan} />}
      {error && <Text color={red}>error · {error}</Text>}
      <Box marginTop={1}>
        <Text color={amber}>◆ </Text><Text color={dim}>{activity}</Text>
      </Box>
      <Box borderStyle="single" borderColor={status === "error" ? red : amber} paddingX={1} marginTop={1}>
        <Text color={amber}>❯ </Text><Text color={ink}>{draft || "Write a question…"}</Text>
      </Box>
      <Text color={dim}>{prompt}</Text>
    </Box>
  );
}

function ApprovalCard({ plan }: { plan: Plan }): React.ReactElement {
  return (
    <Box flexDirection="column" borderStyle="double" borderColor={amber} paddingX={1} marginBottom={1}>
      <Text color={amber} bold>PROPOSED CHANGES · {plan.actions.length} action(s)</Text>
      {plan.actions.map((action, index) => (
        <Text key={`${action.type}-${index}`} color={ink}>  · {describeAction(action)}</Text>
      ))}
    </Box>
  );
}

function describeAction(action: Action): string {
  const target = action.folder || action.title || (action.note_id ? `note ${action.note_id}` : "requested target");
  return `${action.type} → ${target}`;
}

function formatActivity(activity: NonNullable<EngineEvent["activity"]>): string {
  if (activity.phase === "provider_wait" && activity.provider && activity.model) {
    return `${activity.provider} · ${activity.model} is generating a response`;
  }
  if (activity.path) return `${capitalize(activity.phase)} ${activity.path}`;
  return activity.message;
}

function capitalize(value: string): string {
  return value.length === 0 ? value : value[0].toUpperCase() + value.slice(1);
}

render(<App />, { exitOnCtrlC: false });
