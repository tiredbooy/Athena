import React, { useEffect, useMemo, useRef, useState } from "react";
import { Box, Text, render, useApp, useInput, useStdout } from "ink";
import TextInput from "ink-text-input";
import { EngineClient } from "./engine/EngineClient.js";
import type { Action, EngineEvent } from "./protocol/types.js";

type ChatMessage = { id: number; role: "user" | "assistant"; text: string };
type Plan = { id: string; actions: Action[] };
type Command = { name: string; description: string };
type SelectionPoint = { message: number; offset: number };
type Selection = { anchor: SelectionPoint; focus: SelectionPoint };
type MessageLayout = { startRow: number; lines: Array<{ text: string; start: number }> };

const amber = "#e4a853";
const ink = "#e9e2d0";
const dim = "#817b70";
const green = "#9fc17a";
const red = "#d87c72";
const blue = "#91b7d9";
const pulseFrames = ["·", "•", "●", "•"];

const commands: Command[] = [
  { name: "/help", description: "show keyboard shortcuts" },
  { name: "/clear", description: "clear this transcript" },
  { name: "/doctor", description: "diagnose vault and providers" },
  { name: "/models", description: "list available models" },
  { name: "/compact", description: "compact conversation memory" },
  { name: "/cancel", description: "cancel the active turn" },
];

function App(): React.ReactElement {
  const { exit } = useApp();
  const { stdout } = useStdout();
  const interactive = Boolean(process.stdin.isTTY && typeof process.stdin.setRawMode === "function");
  const [client] = useState(() => new EngineClient(process.env.ATHENA_ENGINE ?? "athena", ["engine"]));
  const [draft, setDraft] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [historyCursor, setHistoryCursor] = useState(-1);
  const [editorKey, setEditorKey] = useState(0);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [messageID, setMessageID] = useState(0);
  const [activity, setActivity] = useState("Connecting to the local engine…");
  const [status, setStatus] = useState("starting");
  const [turnID, setTurnID] = useState<string>();
  const [plan, setPlan] = useState<Plan>();
  const [error, setError] = useState<string>();
  const [pulseIndex, setPulseIndex] = useState(0);
  const [selection, setSelection] = useState<Selection>();

  const addMessage = (role: ChatMessage["role"], text: string) => {
    setMessageID((currentID) => {
      const id = currentID + 1;
      setMessages((current) => [...current, { id, role, text }]);
      return id;
    });
  };

  useEffect(() => {
    const animated = status === "starting" || status === "working";
    if (!animated) {
      setPulseIndex(0);
      return;
    }
    const timer = setInterval(() => setPulseIndex((current) => (current + 1) % pulseFrames.length), 360);
    return () => clearInterval(timer);
  }, [status]);

  useEffect(() => {
    const onEvent = (event: EngineEvent) => {
      switch (event.type) {
        case "engine.ready":
          setStatus("ready");
          setActivity("Ready");
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
          if (event.message) addMessage("assistant", event.message);
          setStatus("ready");
          setActivity("Ready");
          return;
        case "plan.ready":
          if (event.planId) setPlan({ id: event.planId, actions: event.actions ?? [] });
          setStatus("approval");
          setActivity("Review the proposed changes");
          return;
        case "turn.completed":
          setTurnID(undefined);
          return;
        case "turn.cancelled":
          setStatus("ready");
          setTurnID(undefined);
          setActivity("Turn cancelled");
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
          if (event.message) addMessage("assistant", event.message);
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
    if (input.includes("[<")) {
      handleMouseInput(input, mouseState.current, setSelection, (copiedText) => {
        stdout.write(osc52Copy(copiedText));
        setActivity(`Copied ${copiedText.length} characters`);
        setStatus("ready");
        setEditorKey((current) => current + 1);
      });
      return;
    }
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
    if (key.upArrow) {
      restoreHistory(-1);
      return;
    }
    if (key.downArrow) restoreHistory(1);
  }, { isActive: interactive });

  const visibleMessages = useMemo(() => {
    const rows = stdout.rows || 24;
    const limit = Math.max(3, rows - (plan ? 14 : 10));
    return messages.slice(-limit);
  }, [messages, plan, stdout.rows]);
  const suggestions = commandSuggestions(draft);
  const terminalWidth = stdout.columns || 80;
  const terminalHeight = stdout.rows || 24;
  const messageWidth = Math.max(12, terminalWidth - 10);
  const messageLayouts = useMemo(() => layoutMessages(visibleMessages, messageWidth), [visibleMessages, messageWidth]);
  const mouseState = useRef({ visibleMessages, messageLayouts, selection });
  mouseState.current = { visibleMessages, messageLayouts, selection };

  useEffect(() => {
    if (!interactive) return;
    const enableMouse = "\u001b[?1002h\u001b[?1006h";
    const disableMouse = "\u001b[?1006l\u001b[?1002l";
    stdout.write(enableMouse);
    return () => {
      stdout.write(disableMouse);
    };
  }, [interactive, stdout]);
  const prompt = plan
    ? "[Y] apply   [N] discard"
    : turnID
      ? "[Esc] cancel current turn"
      : "[Enter] send   [Shift+Enter] newline   [↑↓] history   [Ctrl+C] quit";

  function restoreHistory(direction: -1 | 1) {
    if (history.length === 0) return;
    const next = historyCursor === -1
      ? direction === -1 ? history.length - 1 : -1
      : Math.max(-1, Math.min(history.length - 1, historyCursor + direction));
    setHistoryCursor(next);
    setDraft(next === -1 ? "" : history[next]);
    setEditorKey((current) => current + 1);
  }

  function handleDraftChange(value: string) {
    // Ink's text input also receives the mouse escape sequence. The parent
    // consumes it for transcript selection; never let it become composer text.
    if (value.includes("[<")) return;
    setDraft(value);
    setHistoryCursor(-1);
  }

  function submitDraft(value: string) {
    const inputText = value.trim();
    if (!inputText || status === "working" || status === "starting") return;
    if (inputText === "/clear") {
      setMessages([]);
      setError(undefined);
      setActivity("Transcript cleared");
      resetComposer();
      return;
    }
    if (inputText === "/help") {
      addMessage("assistant", "Enter send · Shift+Enter newline · Esc cancel · ↑↓ history\n\n" + commands.map((command) => `${command.name} — ${command.description}`).join("\n"));
      remember(inputText);
      resetComposer();
      return;
    }
    addMessage("user", inputText);
    remember(inputText);
    resetComposer();
    setError(undefined);
    setStatus("working");
    setActivity("Sending to the local engine…");
    void client.submit(inputText).catch((reason: Error) => {
      setStatus("error");
      setError(reason.message);
    });
  }

  function remember(inputText: string) {
    setHistory((current) => current[current.length - 1] === inputText ? current : [...current, inputText].slice(-30));
  }

  function resetComposer() {
    setDraft("");
    setHistoryCursor(-1);
    setEditorKey((current) => current + 1);
  }

  return (
    <Box flexDirection="column" width={terminalWidth} height={terminalHeight} paddingX={2} paddingY={1}>
      <Header status={status} />
      <Box flexDirection="column" flexGrow={1} overflow="hidden" marginTop={1}>
        {visibleMessages.length === 0 ? <Welcome /> : visibleMessages.map((message, index) => <Message key={message.id} message={message} index={index} layout={messageLayouts[index]} selection={selection} />)}
      </Box>
      {plan && <ApprovalCard plan={plan} />}
      {error && <Text color={red}>error · {error}</Text>}
      {suggestions.length > 0 && <CommandHints suggestions={suggestions} />}
      {showActivity(status, activity) && <ActivityLine activity={activity} status={status} pulse={pulseFrames[pulseIndex]} />}
      <Composer key={editorKey} draft={draft} focus={!plan && interactive} onChange={handleDraftChange} onSubmit={submitDraft} error={status === "error"} />
      <Text color={dim}>{prompt}</Text>
    </Box>
  );
}

function Header({ status }: { status: string }): React.ReactElement {
  const label = status === "approval" ? "REVIEW" : status.toUpperCase();
  const color = status === "error" ? red : status === "working" ? amber : green;
  return <Box justifyContent="space-between"><Text color={amber} bold>A T H E N A</Text><Text color={color}> {label} </Text></Box>;
}

function Welcome(): React.ReactElement {
  return <Text color={dim}>Ask about your vault, notes, or books.</Text>;
}

function Message({ message, index, layout, selection }: { message: ChatMessage; index: number; layout: MessageLayout; selection?: Selection }): React.ReactElement {
  const isUser = message.role === "user";
  const prefix = isUser ? "you  " : "owl  ";
  return <Box flexDirection="column" marginBottom={1}>{layout.lines.map((line, lineIndex) => <Text key={`${message.id}-${lineIndex}`} color={ink}><Text color={isUser ? amber : green} bold>{lineIndex === 0 ? prefix : " ".repeat(prefix.length)}</Text>{renderSelectedLine(line.text, line.start, index, selection)}</Text>)}</Box>;
}

function Composer({ draft, focus, onChange, onSubmit, error }: { draft: string; focus: boolean; onChange: (value: string) => void; onSubmit: (value: string) => void; error: boolean }): React.ReactElement {
  return <Box borderStyle="single" borderColor={error ? red : focus ? amber : dim} paddingX={1} marginTop={1}><Text color={amber}>❯ </Text><TextInput value={draft} onChange={onChange} onSubmit={onSubmit} focus={focus} showCursor={focus} highlightPastedText placeholder="Ask Athena…" /></Box>;
}

function ActivityLine({ activity, status, pulse }: { activity: string; status: string; pulse: string }): React.ReactElement {
  return <Box marginTop={1}><Text color={status === "error" ? red : amber}>{pulse} </Text><Text color={dim}>{activity}</Text></Box>;
}

function ApprovalCard({ plan }: { plan: Plan }): React.ReactElement {
  return <Box flexDirection="column" borderStyle="double" borderColor={amber} paddingX={1} marginBottom={1}><Box justifyContent="space-between"><Text color={amber} bold>PROPOSED CHANGES</Text><Text color={dim}>{plan.actions.length} action(s) · {plan.id}</Text></Box>{plan.actions.map((action, index) => <Text key={`${action.type}-${index}`} color={ink}>  · {describeAction(action)}</Text>)}</Box>;
}

function CommandHints({ suggestions }: { suggestions: Command[] }): React.ReactElement {
  return <Box flexDirection="column" marginTop={1}><Text color={dim}>commands · Tab completes</Text>{suggestions.slice(0, 4).map((command) => <Text key={command.name} color={blue}>  {command.name.padEnd(10)} {command.description}</Text>)}</Box>;
}

function layoutMessages(messages: ChatMessage[], width: number): MessageLayout[] {
  let row = 4;
  return messages.map((message) => {
    const lines = wrapMessage(message.text, width);
    const layout = { startRow: row, lines };
    row += lines.length + 1;
    return layout;
  });
}

function wrapMessage(text: string, width: number): Array<{ text: string; start: number }> {
  const lines: Array<{ text: string; start: number }> = [];
  let offset = 0;
  for (const sourceLine of text.split("\n")) {
    let remaining = sourceLine;
    let lineStart = offset;
    if (remaining.length === 0) lines.push({ text: "", start: lineStart });
    while (remaining.length > 0) {
      if (remaining.length <= width) {
        lines.push({ text: remaining, start: lineStart });
        break;
      }
      let breakAt = remaining.lastIndexOf(" ", width);
      if (breakAt <= 0) breakAt = width;
      const line = remaining.slice(0, breakAt);
      lines.push({ text: line, start: lineStart });
      const consumed = breakAt < remaining.length && remaining[breakAt] === " " ? breakAt + 1 : breakAt;
      remaining = remaining.slice(consumed);
      lineStart += consumed;
    }
    offset += sourceLine.length + 1;
  }
  return lines;
}

function renderSelectedLine(text: string, lineStart: number, message: number, selection?: Selection): React.ReactNode {
  const range = orderedSelection(selection);
  if (!range || message < range.start.message || message > range.end.message) return <Text color={ink}>{text}</Text>;
  const start = message === range.start.message ? range.start.offset : 0;
  const end = message === range.end.message ? range.end.offset : Number.MAX_SAFE_INTEGER;
  const selectedStart = Math.max(0, start - lineStart);
  const selectedEnd = Math.min(text.length, end - lineStart);
  if (selectedEnd <= selectedStart) return <Text color={ink}>{text}</Text>;
  return <><Text color={ink}>{text.slice(0, selectedStart)}</Text><Text color={ink} inverse>{text.slice(selectedStart, selectedEnd)}</Text><Text color={ink}>{text.slice(selectedEnd)}</Text></>;
}

function orderedSelection(selection?: Selection): { start: SelectionPoint; end: SelectionPoint } | undefined {
  if (!selection) return undefined;
  const before = selection.anchor.message < selection.focus.message || (selection.anchor.message === selection.focus.message && selection.anchor.offset <= selection.focus.offset);
  return before ? { start: selection.anchor, end: selection.focus } : { start: selection.focus, end: selection.anchor };
}

function selectedText(messages: ChatMessage[], selection?: Selection): string {
  const range = orderedSelection(selection);
  if (!range) return "";
  const chunks: string[] = [];
  for (let index = range.start.message; index <= range.end.message; index++) {
    const message = messages[index]?.text ?? "";
    const start = index === range.start.message ? range.start.offset : 0;
    const end = index === range.end.message ? range.end.offset : message.length;
    chunks.push(message.slice(start, end));
  }
  return chunks.join("\n").trim();
}

function handleMouseInput(chunk: string, state: { visibleMessages: ChatMessage[]; messageLayouts: MessageLayout[]; selection?: Selection }, setSelection: (selection?: Selection) => void, onCopy: (text: string) => void): void {
  const mouseEvent = /\u001b?\[<(\d+);(\d+);(\d+)([mM])/g;
  let match: RegExpExecArray | null;
  while ((match = mouseEvent.exec(chunk)) !== null) {
    const button = Number(match[1]);
    const column = Number(match[2]);
    const row = Number(match[3]);
    const kind = match[4];
    const point = pointAt(row, column, state.messageLayouts);
    const leftButton = (button & 3) === 0;
    if (kind === "M" && leftButton && point) {
      const nextSelection = (button & 32) !== 0 && state.selection
        ? { anchor: state.selection.anchor, focus: point }
        : { anchor: point, focus: point };
      state.selection = nextSelection;
      setSelection(nextSelection);
    }
    if (kind === "m" && state.selection) {
      const text = selectedText(state.visibleMessages, state.selection);
      if (text.length > 0) onCopy(text);
      state.selection = undefined;
      setSelection(undefined);
    }
  }
}

function pointAt(row: number, column: number, layouts: MessageLayout[]): SelectionPoint | undefined {
  const contentStartColumn = 9;
  for (let message = 0; message < layouts.length; message++) {
    const layout = layouts[message];
    const lineIndex = row - layout.startRow;
    if (lineIndex < 0 || lineIndex >= layout.lines.length) continue;
    const line = layout.lines[lineIndex];
    const offset = Math.max(0, Math.min(line.text.length, column - contentStartColumn));
    return { message, offset: line.start + offset };
  }
  return undefined;
}

function osc52Copy(text: string): string {
  return `\u001b]52;c;${Buffer.from(text, "utf8").toString("base64")}\u0007`;
}

function commandSuggestions(draft: string): Command[] {
  if (!draft.startsWith("/") || draft.includes(" ")) return [];
  return commands.filter((command) => command.name.startsWith(draft.toLowerCase()));
}

function describeAction(action: Action): string {
  const target = action.folder || action.title || (action.note_id ? `note ${action.note_id}` : "requested target");
  return `${action.type} → ${target}`;
}

function formatActivity(activity: NonNullable<EngineEvent["activity"]>): string {
  if (activity.phase === "provider_wait" && activity.provider && activity.model) return `${activity.provider} · ${activity.model} is generating a response`;
  if (activity.path) return `${capitalize(activity.phase)} ${activity.path}`;
  return activity.message;
}

function capitalize(value: string): string {
  return value.length === 0 ? value : value[0].toUpperCase() + value.slice(1);
}

function showActivity(status: string, activity: string): boolean {
  return status !== "ready" || (activity !== "Ready" && activity !== "Ready · your vault stays local");
}

render(<App />, { exitOnCtrlC: false });
