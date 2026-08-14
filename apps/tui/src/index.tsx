import React, { useEffect, useMemo, useRef, useState } from "react";
import { spawn } from "node:child_process";
import { Box, Text, render, useApp, useInput, useStdout } from "ink";
import TextInput from "ink-text-input";
import { EngineClient } from "./engine/EngineClient.js";
import type { Action, EngineEvent, ModelOption, ProviderConnection, ProviderPreset } from "./protocol/types.js";
import {
  buildTranscriptRows,
  orderedSelection,
  selectedText,
  selectionPointAt,
  windowTranscript,
  type Selection,
  type TranscriptContentRow,
  type TranscriptMessage,
  type TranscriptRow,
} from "./transcript.js";

type ChatMessage = TranscriptMessage;
type Plan = { id: string; actions: Action[] };
type PlanDecision = "waiting" | "applying" | "discarding";
type Command = { name: string; description: string };
type ConnectStep = "providers" | "name" | "base_url" | "api_key" | "model" | "oauth" | "saving";
type ConnectFlow = {
  step: ConnectStep;
  presets: ProviderPreset[];
  selectedIndex: number;
  preset?: ProviderPreset;
  values: ProviderConnection;
  oauthLines: string[];
};
type ModelFlow = { options: ModelOption[]; selectedIndex: number; selecting: boolean };

const amber = "#e4a853";
const ink = "#e9e2d0";
const dim = "#817b70";
const green = "#9fc17a";
const red = "#d87c72";
const blue = "#91b7d9";
const pulseFrames = ["◐", "◓", "◑", "◒"];
const loadingPhrases = ["Cooking", "Pulling in the useful context", "Checking the vault", "Locking it in"];

const commands: Command[] = [
  { name: "/help", description: "show keyboard shortcuts" },
  { name: "/clear", description: "clear this transcript" },
  { name: "/doctor", description: "diagnose vault and providers" },
  { name: "/models", description: "list available models" },
  { name: "/connect", description: "connect a model provider" },
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
  const [modelName, setModelName] = useState("local model");
  const [status, setStatus] = useState("starting");
  const [turnID, setTurnID] = useState<string>();
  const [plan, setPlan] = useState<Plan>();
  const [planDecision, setPlanDecision] = useState<PlanDecision>("waiting");
  const [error, setError] = useState<string>();
  const [pulseIndex, setPulseIndex] = useState(0);
  const [loadingPhraseIndex, setLoadingPhraseIndex] = useState(0);
  const [selection, setSelection] = useState<Selection>();
  const [scrollOffset, setScrollOffset] = useState(0);
  const [connectFlow, setConnectFlow] = useState<ConnectFlow>();
  const [modelFlow, setModelFlow] = useState<ModelFlow>();
  const activityTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const activityState = useRef({ lastShownAt: 0, pending: "" });
  const transcriptError = useRef<string | undefined>(undefined);
  const previousTranscriptRows = useRef(0);

  const addMessage = (role: ChatMessage["role"], text: string) => {
    setMessageID((currentID) => {
      const id = currentID + 1;
      setMessages((current) => [...current, { id, role, text }]);
      return id;
    });
  };

  useEffect(() => {
    if (!error) {
      transcriptError.current = undefined;
      return;
    }
    if (transcriptError.current === error) return;
    transcriptError.current = error;
    addMessage("error", error);
  }, [error]);

  function setActivityNow(next: string) {
    if (activityTimer.current) clearTimeout(activityTimer.current);
    activityTimer.current = undefined;
    activityState.current.pending = "";
    activityState.current.lastShownAt = Date.now();
    setActivity(next);
  }

  function queueActivity(next: string) {
    if (!next || next === activityState.current.pending) return;
    const elapsed = Date.now() - activityState.current.lastShownAt;
    const delay = Math.max(0, 850 - elapsed);
    activityState.current.pending = next;
    if (activityTimer.current) clearTimeout(activityTimer.current);
    activityTimer.current = setTimeout(() => {
      activityTimer.current = undefined;
      const pending = activityState.current.pending;
      activityState.current.pending = "";
      activityState.current.lastShownAt = Date.now();
      setActivity(pending);
    }, delay);
  }

  useEffect(() => () => {
    if (activityTimer.current) clearTimeout(activityTimer.current);
  }, []);

  useEffect(() => {
    const animated = status === "starting" || status === "working";
    if (!animated) {
      setPulseIndex(0);
      return;
    }
    const pulseTimer = setInterval(() => setPulseIndex((current) => (current + 1) % pulseFrames.length), 620);
    const phraseTimer = setInterval(() => setLoadingPhraseIndex((current) => (current + 1) % loadingPhrases.length), 4800);
    return () => {
      clearInterval(pulseTimer);
      clearInterval(phraseTimer);
    };
  }, [status]);

  useEffect(() => {
    const onEvent = (event: EngineEvent) => {
      switch (event.type) {
        case "engine.ready":
          if (event.provider && event.model) setModelName(`${event.provider} · ${shortModel(event.model)}`);
          setStatus("ready");
          setActivityNow("Ready");
          return;
        case "turn.started":
          setTurnID(event.turnId);
          setStatus("working");
          setError(undefined);
          return;
        case "activity":
          if (event.activity) {
            if (event.activity.provider && event.activity.model) setModelName(`${event.activity.provider} · ${shortModel(event.activity.model)}`);
            queueActivity(formatActivity(event.activity));
          }
          return;
        case "response":
          if (event.message) addMessage("assistant", event.message);
          setStatus("ready");
          setActivityNow("Ready");
          return;
        case "plan.ready":
          if (event.planId) setPlan({ id: event.planId, actions: event.actions ?? [] });
          setPlanDecision("waiting");
          setTurnID(undefined);
          setError(undefined);
          setStatus("approval");
          setActivityNow("Waiting for your approval");
          return;
        case "turn.completed":
          setTurnID(undefined);
          return;
        case "turn.cancelled":
          setStatus("ready");
          setTurnID(undefined);
          setActivityNow("Turn cancelled");
          return;
        case "turn.failed":
          setStatus("error");
          setTurnID(undefined);
          setError(event.error ?? "The engine could not complete that turn.");
          return;
        case "plan.approved":
        case "plan.rejected":
          setPlan(undefined);
          setPlanDecision("waiting");
          setStatus("ready");
          setActivityNow(event.type === "plan.approved" ? "Changes applied" : "Changes discarded");
          if (event.message) addMessage("assistant", event.message);
          return;
        case "provider.presets":
          setConnectFlow({
            step: "providers",
            presets: event.presets ?? [],
            selectedIndex: 0,
            values: { name: "", type: "", base_url: "", chat_model: "" },
            oauthLines: [],
          });
          setStatus("ready");
          setActivityNow("Choose a provider");
          return;
        case "provider.oauth.started":
          setTurnID(event.turnId);
          setStatus("working");
          setActivityNow("Waiting for provider sign-in");
          setConnectFlow((current) => current ? { ...current, step: "oauth" } : current);
          return;
        case "provider.oauth.progress":
          if (event.message) {
            setConnectFlow((current) => current ? { ...current, oauthLines: [...current.oauthLines, event.message!].slice(-8) } : current);
            const loginURL = firstURL(event.message);
            if (loginURL) {
              void copyToClipboard(loginURL, stdout).then((copied) => {
                if (copied) setActivityNow("Sign-in link copied to clipboard");
              });
            }
          }
          return;
        case "provider.oauth.cancelled":
          setTurnID(undefined);
          setStatus("ready");
          setConnectFlow(undefined);
          setActivityNow("Sign-in cancelled");
          return;
        case "provider.connected":
          if (event.provider && event.model) setModelName(`${event.provider} · ${shortModel(event.model)}`);
          if (event.message) addMessage("assistant", event.message);
          setTurnID(undefined);
          setConnectFlow(undefined);
          setStatus("ready");
          setActivityNow("Provider connected");
          return;
        case "model.options":
          setModelFlow({
            options: event.models ?? [],
            selectedIndex: Math.max(0, (event.models ?? []).findIndex((option) => option.current)),
            selecting: false,
          });
          setStatus("ready");
          setActivityNow("Choose a model");
          return;
        case "model.selected":
          if (event.provider && event.model) setModelName(`${event.provider} · ${shortModel(event.model)}`);
          setModelFlow(undefined);
          setStatus("ready");
          setActivityNow(event.message ?? "Model changed");
          return;
        case "error":
          setError(event.error ?? "The engine rejected the request.");
          if (event.turnId) setTurnID(undefined);
          if (event.planId) {
            setPlanDecision("waiting");
            setStatus("approval");
            setActivityNow("Approval failed — choose again");
          } else {
            setStatus("error");
          }
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
        void copyToClipboard(copiedText, stdout).then((copied) => {
          if (copied) {
            setActivityNow(`Copied ${copiedText.length} characters`);
            setStatus("ready");
          } else {
            setError("Could not access a clipboard. Your terminal may not support OSC 52.");
            setStatus("error");
          }
          setEditorKey((current) => current + 1);
        });
      }, scrollTranscript);
      return;
    }
    if (key.ctrl && input === "c") {
      client.dispose();
      exit();
      return;
    }
    if (key.pageUp) {
      scrollTranscript(Math.max(1, transcriptHeight - 1));
      return;
    }
    if (key.pageDown) {
      scrollTranscript(-Math.max(1, transcriptHeight - 1));
      return;
    }
    if (modelFlow) {
      if (key.escape) {
        setModelFlow(undefined);
        setActivityNow("Model selection closed");
        resetComposer();
        return;
      }
      if (modelFlow.selecting || modelFlow.options.length === 0) return;
      if (key.upArrow) {
        setModelFlow({ ...modelFlow, selectedIndex: (modelFlow.selectedIndex - 1 + modelFlow.options.length) % modelFlow.options.length });
        return;
      }
      if (key.downArrow) {
        setModelFlow({ ...modelFlow, selectedIndex: (modelFlow.selectedIndex + 1) % modelFlow.options.length });
        return;
      }
      if (key.return) {
        const selected = modelFlow.options[modelFlow.selectedIndex];
        setModelFlow({ ...modelFlow, selecting: true });
        setStatus("working");
        setActivityNow(`Switching to ${selected.model}…`);
        void client.selectModel(selected.providerId, selected.model).catch((reason: Error) => {
          setModelFlow((current) => current ? { ...current, selecting: false } : current);
          setStatus("error");
          setError(reason.message);
        });
        return;
      }
      return;
    }
    if (connectFlow) {
      if (key.escape) {
        if (connectFlow.step === "oauth" && turnID) void client.cancel(turnID).catch((reason: Error) => setError(reason.message));
        else {
          setConnectFlow(undefined);
          setActivityNow("Connection closed");
        }
        resetComposer();
        return;
      }
      if (connectFlow.step === "providers" && connectFlow.presets.length > 0) {
        if (key.upArrow) {
          setConnectFlow({ ...connectFlow, selectedIndex: (connectFlow.selectedIndex - 1 + connectFlow.presets.length) % connectFlow.presets.length });
          return;
        }
        if (key.downArrow) {
          setConnectFlow({ ...connectFlow, selectedIndex: (connectFlow.selectedIndex + 1) % connectFlow.presets.length });
          return;
        }
      }
      if (key.upArrow || key.downArrow) return;
    }
    if (plan) {
      if (planDecision !== "waiting") return;
      if (input.toLowerCase() === "y" || key.return) {
        setPlanDecision("applying");
        setStatus("working");
        setError(undefined);
        setActivityNow("Applying approved changes…");
        void client.approve(plan.id).catch((reason: Error) => {
          setPlanDecision("waiting");
          setStatus("approval");
          setActivityNow("Approval failed — choose again");
          setError(reason.message);
        });
      } else if (input.toLowerCase() === "n" || key.escape) {
        setPlanDecision("discarding");
        setStatus("working");
        setError(undefined);
        setActivityNow("Discarding proposed changes…");
        void client.reject(plan.id).catch((reason: Error) => {
          setPlanDecision("waiting");
          setStatus("approval");
          setActivityNow("Discard failed — choose again");
          setError(reason.message);
        });
      }
      return;
    }
    if (key.escape && turnID) {
      void client.cancel(turnID).catch((reason: Error) => setError(reason.message));
      setActivityNow("Cancellation requested…");
      return;
    }
    if (key.upArrow) {
      restoreHistory(-1);
      return;
    }
    if (key.downArrow) restoreHistory(1);
  }, { isActive: interactive });

  const terminalWidth = stdout.columns || 80;
  const terminalHeight = stdout.rows || 24;
  // Root horizontal padding consumes four columns and each role prefix consumes
  // seven more. Keeping those out of the content width prevents terminal-level
  // wrapping from adding rows that Ink did not account for.
  const messageWidth = Math.max(12, terminalWidth - 11);
  const suggestions = connectFlow || modelFlow ? [] : commandSuggestions(draft);
  const activityShown = showActivity(status, activity);
  const transcriptHeight = Math.max(3, terminalHeight - reservedRows(plan, connectFlow, modelFlow, suggestions, activityShown));
  const transcriptRows = useMemo(() => buildTranscriptRows(messages, messageWidth), [messages, messageWidth]);
  const transcript = useMemo(
    () => windowTranscript(transcriptRows, transcriptHeight, scrollOffset),
    [transcriptRows, transcriptHeight, scrollOffset],
  );
  const mouseState = useRef({ messages, rows: transcript.rows, selection });
  mouseState.current = { messages, rows: transcript.rows, selection };

  useEffect(() => {
    const previous = previousTranscriptRows.current;
    const growth = transcriptRows.length - previous;
    previousTranscriptRows.current = transcriptRows.length;
    setScrollOffset((current) => {
      const preserved = current > 0 && growth > 0 ? current + growth : current;
      return Math.min(transcript.maxOffset, Math.max(0, preserved));
    });
  }, [transcriptRows.length, transcript.maxOffset]);

  useEffect(() => {
    if (!interactive) return;
    const enableMouse = "\u001b[?1002h\u001b[?1006h";
    const disableMouse = "\u001b[?1006l\u001b[?1002l";
    stdout.write(enableMouse);
    return () => {
      stdout.write(disableMouse);
    };
  }, [interactive, stdout]);
  const prompt = modelFlow
    ? modelFlow.selecting ? "Switching model…" : "↑↓ choose · Enter select · Esc close"
    : connectFlow
    ? connectFlow.step === "oauth" ? "Esc cancel · complete sign-in in your browser" : "Enter continue · Esc close"
    : plan
    ? planDecision === "applying"
      ? "Applying approved changes…"
      : planDecision === "discarding"
        ? "Discarding proposed changes…"
        : "[Y] apply changes · [N] discard"
    : turnID
      ? "[Esc] cancel"
      : "Enter send · ↑↓ history · Ctrl+C quit";

  function scrollTranscript(delta: number) {
    setSelection(undefined);
    setScrollOffset((current) => Math.max(0, Math.min(transcript.maxOffset, current + delta)));
  }

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
    if (connectFlow) {
      submitConnectStep(inputText);
      return;
    }
    if (!inputText || status === "working" || status === "starting") return;
    if (inputText === "/clear") {
      setMessages([]);
      setSelection(undefined);
      setScrollOffset(0);
      setError(undefined);
      setActivityNow("Transcript cleared");
      resetComposer();
      return;
    }
    if (inputText === "/help") {
      setScrollOffset(0);
      addMessage("assistant", "Enter send · Shift+Enter newline · Esc cancel · ↑↓ history · PgUp/PgDn or mouse wheel scroll · drag to copy\n\n" + commands.map((command) => `${command.name} — ${command.description}`).join("\n"));
      remember(inputText);
      resetComposer();
      return;
    }
    if (inputText === "/connect") {
      remember(inputText);
      resetComposer();
      setError(undefined);
      setStatus("working");
      setActivityNow("Loading provider options…");
      void client.providers().catch((reason: Error) => {
        setStatus("error");
        setError(reason.message);
      });
      return;
    }
    if (inputText === "/models") {
      remember(inputText);
      resetComposer();
      setError(undefined);
      setStatus("working");
      setActivityNow("Loading available models…");
      void client.models().catch((reason: Error) => {
        setModelFlow(undefined);
        setStatus("error");
        setError(reason.message);
      });
      return;
    }
    setScrollOffset(0);
    addMessage("user", inputText);
    remember(inputText);
    resetComposer();
    setError(undefined);
    setStatus("working");
    setActivityNow("Sending to the local engine…");
    void client.submit(inputText).catch((reason: Error) => {
      setStatus("error");
      setError(reason.message);
    });
  }

  function submitConnectStep(value: string) {
    if (!connectFlow || connectFlow.step === "oauth" || connectFlow.step === "saving") return;
    setError(undefined);
    if (connectFlow.step === "providers") {
      const preset = connectFlow.presets[connectFlow.selectedIndex];
      if (!preset) return;
      if (!preset.available) {
        setError(preset.unavailable ?? `${preset.label} is unavailable`);
        return;
      }
      const values: ProviderConnection = {
        name: preset.name ?? "",
        type: preset.type,
        base_url: preset.base_url ?? "",
        api_key_env: preset.api_key_env,
        chat_model: preset.chat_model ?? "",
      };
      if (preset.auth === "oauth") {
        setConnectFlow({ ...connectFlow, preset, values, step: "oauth", oauthLines: ["Preparing secure device login…"] });
        setStatus("working");
        setActivityNow(`Starting ${preset.label} sign-in…`);
        void client.startOAuth(preset.id).catch((reason: Error) => {
          setStatus("error");
          setError(reason.message);
        });
        return;
      }
      if (preset.auth === "none") {
        saveProviderConnection(values);
        return;
      }
      setConnectFlow({ ...connectFlow, preset, values, step: "name" });
      setDraft(values.name);
      setEditorKey((current) => current + 1);
      return;
    }

    const values = { ...connectFlow.values };
    if (connectFlow.step === "name") values.name = value || values.name;
    if (connectFlow.step === "base_url") values.base_url = value || values.base_url;
    if (connectFlow.step === "api_key") values.api_key = value;
    if (connectFlow.step === "model") values.chat_model = value || values.chat_model;
    if ((connectFlow.step === "name" && !values.name) || (connectFlow.step === "base_url" && !values.base_url)) {
      setError("This field is required.");
      return;
    }
    if (connectFlow.step === "model") {
      if (!values.chat_model) {
        setError("A default chat model is required.");
        return;
      }
      saveProviderConnection(values);
      return;
    }
    const next: Record<Exclude<ConnectStep, "providers" | "oauth" | "saving" | "model">, ConnectStep> = {
      name: "base_url",
      base_url: "api_key",
      api_key: "model",
    };
    const step = next[connectFlow.step as "name" | "base_url" | "api_key"];
    setConnectFlow({ ...connectFlow, values, step });
    setDraft(connectDefaultValue(step, values));
    setEditorKey((current) => current + 1);
  }

  function saveProviderConnection(values: ProviderConnection) {
    setConnectFlow((current) => current ? { ...current, values, step: "saving" } : current);
    setDraft("");
    setEditorKey((current) => current + 1);
    setStatus("working");
    setActivityNow("Saving provider locally…");
    void client.connect(values).catch((reason: Error) => {
      setStatus("error");
      setError(reason.message);
      setConnectFlow((current) => current ? { ...current, step: "model" } : current);
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
      <Header />
      <Box flexDirection="column" height={transcriptHeight} flexShrink={0} overflow="hidden" marginTop={1}>
        {messages.length === 0 ? <Welcome /> : transcript.rows.map((row) => <TranscriptLine key={row.key} row={row} selection={selection} />)}
      </Box>
      {plan && <ApprovalCard plan={plan} decision={planDecision} />}
      {connectFlow && <ConnectPanel flow={connectFlow} />}
      {modelFlow && <ModelPanel flow={modelFlow} />}
      {suggestions.length > 0 && <CommandHints suggestions={suggestions} />}
      {activityShown && <ActivityLine activity={activity} status={status} pulse={pulseFrames[pulseIndex]} phrase={loadingPhrases[loadingPhraseIndex]} />}
      <Composer key={editorKey} draft={draft} focus={!plan && !modelFlow && connectFlow?.step !== "oauth" && connectFlow?.step !== "saving" && interactive} onChange={handleDraftChange} onSubmit={submitDraft} error={status === "error"} placeholder={modelFlow ? "Choose a model above" : connectPlaceholder(connectFlow)} mask={connectFlow?.step === "api_key" ? "•" : undefined} />
      <Footer prompt={prompt} model={modelName} scrollOffset={transcript.offset} maxScrollOffset={transcript.maxOffset} />
    </Box>
  );
}

function Header(): React.ReactElement {
  return <Text color={amber} bold>A T H E N A</Text>;
}

function Welcome(): React.ReactElement {
  return <Text color={dim}>Ask Athena to find, create, connect, or organize anything in your vault.</Text>;
}

function TranscriptLine({ row, selection }: { row: TranscriptRow; selection?: Selection }): React.ReactElement {
  if (row.kind === "spacer") return <Text> </Text>;
  const isUser = row.role === "user";
  const isError = row.role === "error";
  const prefix = row.firstLine ? isUser ? "you    " : isError ? "error  " : "athena " : "       ";
  const color = isUser ? amber : isError ? red : green;
  return <Text color={isError ? red : ink} wrap="truncate-end"><Text color={color} bold>{prefix}</Text>{renderSelectedLine(row, selection, isError ? red : ink)}</Text>;
}

function Composer({ draft, focus, onChange, onSubmit, error, placeholder, mask }: { draft: string; focus: boolean; onChange: (value: string) => void; onSubmit: (value: string) => void; error: boolean; placeholder: string; mask?: string }): React.ReactElement {
  return <Box borderStyle="single" borderColor={error ? red : focus ? amber : dim} paddingX={1} marginTop={1}><Text color={amber}>❯ </Text><TextInput value={draft} onChange={onChange} onSubmit={onSubmit} focus={focus} showCursor={focus} highlightPastedText placeholder={placeholder} mask={mask} /></Box>;
}

function ConnectPanel({ flow }: { flow: ConnectFlow }): React.ReactElement {
  if (flow.step === "providers") {
    return <Box flexDirection="column" borderStyle="double" borderColor={blue} paddingX={1} marginTop={1}>
      <Text color={blue} bold>CONNECT A MODEL PROVIDER</Text>
      {flow.presets.map((preset, index) => <Text key={preset.id} color={!preset.available ? dim : index === flow.selectedIndex ? amber : ink}>
        {index === flow.selectedIndex ? "❯ " : "  "}{preset.label} <Text color={dim}>· {preset.detail}</Text>{!preset.available ? <Text color={red}> · unavailable</Text> : null}
      </Text>)}
      <Text color={dim}>↑↓ choose · Enter continue · secrets stay local</Text>
    </Box>;
  }
  if (flow.step === "oauth") {
    return <Box flexDirection="column" borderStyle="double" borderColor={amber} paddingX={1} marginTop={1}>
      <Text color={amber} bold>{(flow.preset?.label ?? "PROVIDER").toUpperCase()} · DEVICE LOGIN</Text>
      {flow.oauthLines.map((line, index) => <Text key={`${line}-${index}`} color={index === flow.oauthLines.length - 1 ? ink : dim}>{line}</Text>)}
      <Text color={blue}>The login URL is copied when available. Athena is waiting for confirmation.</Text>
    </Box>;
  }
  const current = connectStepNumber(flow.step);
  return <Box flexDirection="column" borderStyle="double" borderColor={blue} paddingX={1} marginTop={1}>
    <Box justifyContent="space-between"><Text color={blue} bold>{flow.step === "saving" ? "SAVING PROVIDER" : "PROVIDER DETAILS"}</Text><Text color={dim}>{flow.preset?.label}</Text></Box>
    <Text color={dim}>{["name", "URL", "secret", "model"].map((label, index) => `${index + 1 === current ? "[" + label + "]" : label}`).join("  →  ")}</Text>
    <Text color={ink}>{connectStepHelp(flow)}</Text>
  </Box>;
}

function ModelPanel({ flow }: { flow: ModelFlow }): React.ReactElement {
  const limit = 6;
  const start = Math.min(Math.max(0, flow.selectedIndex - limit + 1), Math.max(0, flow.options.length - limit));
  const visible = flow.options.slice(start, start + limit);
  return <Box flexDirection="column" borderStyle="double" borderColor={blue} paddingX={1} marginTop={1}>
    <Box justifyContent="space-between"><Text color={blue} bold>AVAILABLE MODELS</Text><Text color={dim}>{flow.selectedIndex + 1}/{flow.options.length}</Text></Box>
    {visible.map((option, visibleIndex) => {
      const index = start + visibleIndex;
      return <Text key={`${option.providerId}-${option.model}`} color={index === flow.selectedIndex ? amber : ink} wrap="truncate-end">
        {index === flow.selectedIndex ? "❯ " : "  "}{option.model}<Text color={dim}> · {option.providerName}{option.current ? " · current" : ""}</Text>
      </Text>;
    })}
    <Text color={flow.selecting ? blue : dim}>{flow.selecting ? "Switching model…" : "↑↓ choose · Enter select · Esc close"}</Text>
  </Box>;
}

function connectDefaultValue(step: ConnectStep, values: ProviderConnection): string {
  switch (step) {
    case "name":
      return values.name;
    case "base_url":
      return values.base_url;
    case "model":
      return values.chat_model;
    default:
      return "";
  }
}

function connectPlaceholder(flow?: ConnectFlow): string {
  if (!flow) return "Ask Athena…";
  switch (flow.step) {
    case "providers":
      return "Press Enter to choose";
    case "name":
      return "Provider name";
    case "base_url":
      return "https://api.example.com/v1";
    case "api_key":
      return flow.values.api_key_env ? `API key or Enter to use ${flow.values.api_key_env}` : "API key (optional for local APIs)";
    case "model":
      return "Default model ID";
    case "oauth":
      return "Complete sign-in in your browser";
    case "saving":
      return "Saving provider…";
  }
}

function connectStepNumber(step: ConnectStep): number {
  switch (step) {
    case "name":
      return 1;
    case "base_url":
      return 2;
    case "api_key":
      return 3;
    case "model":
      return 4;
    default:
      return 0;
  }
}

function connectStepHelp(flow: ConnectFlow): string {
  switch (flow.step) {
    case "name":
      return "Give this connection a recognizable name.";
    case "base_url":
      return "Enter the provider's API base URL.";
    case "api_key":
      return flow.values.api_key_env
        ? `Paste a key, or press Enter to read ${flow.values.api_key_env}. Typed secrets are masked and stored locally.`
        : "Paste a key, or press Enter for an API that needs no authentication. Typed secrets are masked and stored locally.";
    case "model":
      return "Enter the exact model ID Athena should use by default.";
    case "saving":
      return "The key is written to a user-only local credential file and never added to athena.yaml.";
    default:
      return "";
  }
}

function firstURL(value: string): string | undefined {
  const match = value.match(/https?:\/\/[^\s<>"']+/i);
  return match?.[0]?.replace(/[),.;]+$/, "");
}

function ActivityLine({ activity, status, pulse, phrase }: { activity: string; status: string; pulse: string; phrase: string }): React.ReactElement {
  const loading = status === "starting" || status === "working";
  return <Box marginTop={1}><Text wrap="truncate-end"><Text color={status === "error" ? red : amber}>{pulse} </Text><Text color={ink}>{activity}</Text>{loading && <Text color={blue}> · {phrase}…</Text>}</Text></Box>;
}

function Footer({ prompt, model, scrollOffset, maxScrollOffset }: { prompt: string; model: string; scrollOffset: number; maxScrollOffset: number }): React.ReactElement {
  const scroll = maxScrollOffset > 0
    ? `PgUp/PgDn scroll${scrollOffset > 0 ? ` · ${scrollOffset} rows up` : ""}`
    : "";
  return <Box justifyContent="space-between"><Text color={dim} wrap="truncate-end">{prompt} · {scroll ? scroll + " · " : ""}drag copy</Text><Text color={dim} wrap="truncate-end">model · {model}</Text></Box>;
}

function ApprovalCard({ plan, decision }: { plan: Plan; decision: PlanDecision }): React.ReactElement {
  const borderColor = decision === "waiting" ? amber : decision === "applying" ? blue : dim;
  const heading = decision === "waiting"
    ? "ACTION REVIEW · YOUR APPROVAL NEEDED"
    : decision === "applying"
      ? "APPLYING APPROVED CHANGES…"
      : "DISCARDING PROPOSED CHANGES…";
  const instruction = decision === "waiting"
    ? "Press Y to apply · N to discard"
    : decision === "applying"
      ? "Athena is carrying out these changes now"
      : "Athena is clearing this proposal now";
  return <Box flexDirection="column" borderStyle="double" borderColor={borderColor} paddingX={1} marginBottom={1}>
    <Box justifyContent="space-between"><Text color={borderColor} bold>{heading}</Text><Text color={dim}>{plan.actions.length} change(s)</Text></Box>
    {plan.actions.map((action, index) => <Text key={`${action.type}-${index}`} color={ink}>  · {describeAction(action)}</Text>)}
    <Text color={decision === "waiting" ? amber : blue}>{instruction}</Text>
  </Box>;
}

function CommandHints({ suggestions }: { suggestions: Command[] }): React.ReactElement {
  return <Box flexDirection="column" marginTop={1}><Text color={dim}>commands · Tab completes</Text>{suggestions.slice(0, 4).map((command) => <Text key={command.name} color={blue}>  {command.name.padEnd(10)} {command.description}</Text>)}</Box>;
}

function renderSelectedLine(row: TranscriptContentRow, selection: Selection | undefined, color: string): React.ReactNode {
  const range = orderedSelection(selection);
  if (!range || row.message < range.start.message || row.message > range.end.message) return <Text color={color}>{row.text}</Text>;
  const start = row.message === range.start.message ? range.start.offset : 0;
  const end = row.message === range.end.message ? range.end.offset : Number.MAX_SAFE_INTEGER;
  const selectedStart = Math.max(0, start - row.start);
  const selectedEnd = Math.min(row.text.length, end - row.start);
  if (selectedEnd <= selectedStart) return <Text color={color}>{row.text}</Text>;
  return <><Text color={color}>{row.text.slice(0, selectedStart)}</Text><Text color={color} inverse>{row.text.slice(selectedStart, selectedEnd)}</Text><Text color={color}>{row.text.slice(selectedEnd)}</Text></>;
}

function handleMouseInput(
  chunk: string,
  state: { messages: ChatMessage[]; rows: TranscriptRow[]; selection?: Selection },
  setSelection: (selection?: Selection) => void,
  onCopy: (text: string) => void,
  onScroll: (delta: number) => void,
): void {
  const mouseEvent = /\u001b?\[<(\d+);(\d+);(\d+)([mM])/g;
  let match: RegExpExecArray | null;
  while ((match = mouseEvent.exec(chunk)) !== null) {
    const button = Number(match[1]);
    const column = Number(match[2]);
    const row = Number(match[3]);
    const kind = match[4];
    if ((button & 64) !== 0 && kind === "M") {
      onScroll((button & 1) === 0 ? 3 : -3);
      continue;
    }
    const point = selectionPointAt(row, column, state.rows);
    const leftButton = (button & 3) === 0;
    if (kind === "M" && leftButton && point) {
      const nextSelection = (button & 32) !== 0 && state.selection
        ? { anchor: state.selection.anchor, focus: point }
        : { anchor: point, focus: point };
      state.selection = nextSelection;
      setSelection(nextSelection);
    }
    if (kind === "m" && state.selection) {
      const text = selectedText(state.messages, state.selection);
      if (text.length > 0) onCopy(text);
      state.selection = undefined;
      setSelection(undefined);
    }
  }
}

function osc52Copy(text: string): string {
  return `\u001b]52;c;${Buffer.from(text, "utf8").toString("base64")}\u0007`;
}

async function copyToClipboard(text: string, stdout: NodeJS.WriteStream): Promise<boolean> {
  for (const hostCommand of hostClipboardCommands()) {
    if (await runClipboardCommand(hostCommand.command, hostCommand.args, text)) return true;
  }
  if (stdout.isTTY) {
    stdout.write(osc52Copy(text));
    return true;
  }
  return false;
}

function hostClipboardCommands(): Array<{ command: string; args: string[] }> {
  if (process.platform === "darwin") return [{ command: "pbcopy", args: [] }];
  if (process.platform === "win32") return [{ command: "clip", args: [] }];
  if (process.env.WAYLAND_DISPLAY) return [{ command: "wl-copy", args: [] }];
  if (process.env.DISPLAY) {
    return [
      { command: "xclip", args: ["-selection", "clipboard"] },
      { command: "xsel", args: ["--clipboard", "--input"] },
    ];
  }
  return [];
}

function runClipboardCommand(command: string, args: string[], text: string): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (success: boolean) => {
      if (settled) return;
      settled = true;
      resolve(success);
    };
    let child;
    try {
      child = spawn(command, args, { stdio: ["pipe", "ignore", "ignore"] });
    } catch {
      finish(false);
      return;
    }
    child.once("error", () => finish(false));
    child.once("close", (code) => finish(code === 0));
    child.stdin.end(text);
  });
}

function commandSuggestions(draft: string): Command[] {
  if (!draft.startsWith("/") || draft.includes(" ")) return [];
  return commands.filter((command) => command.name.startsWith(draft.toLowerCase()));
}

function describeAction(action: Action): string {
  let target = action.folder || action.title || (action.note_id ? `note ${action.note_id}` : "requested target");
  if (action.paths?.length) target = action.paths.join(", ");
  if (action.folders?.length) target = action.folders.join(" ↔ ");
  if (action.type === "move_folder") target += action.new_folder ? ` → parent ${action.new_folder}` : " → vault root";
  if (action.type === "set_folder_colors" && action.include_children) target += " + direct subfolders";
  if (action.type === "set_graph_node_size" && action.node_size_multiplier) target = `all graph nodes → ${action.node_size_multiplier.toFixed(2)}x`;
  return `${action.type.replaceAll("_", " ")} → ${target}`;
}

function formatActivity(activity: NonNullable<EngineEvent["activity"]>): string {
  if (activity.phase === "provider_wait") return "Generating a response";
  if (activity.tool && activity.state === "started") {
    return activity.target ? `${friendlyAction(activity.tool)} · ${activity.target}` : friendlyAction(activity.tool);
  }
  if (activity.path) return `${capitalize(activity.phase)} ${activity.path}`;
  return activity.message;
}

function friendlyAction(action: string): string {
  return action
    .split("_")
    .filter(Boolean)
    .map(capitalize)
    .join(" ");
}

function shortModel(model: string): string {
  const clean = model.split("/").pop()?.trim() ?? model.trim();
  return clean.length > 34 ? `${clean.slice(0, 33)}…` : clean;
}

function capitalize(value: string): string {
  return value.length === 0 ? value : value[0].toUpperCase() + value.slice(1);
}

function showActivity(status: string, activity: string): boolean {
  return status !== "ready" || (activity !== "Ready" && activity !== "Ready · your vault stays local");
}

function reservedRows(plan: Plan | undefined, connectFlow: ConnectFlow | undefined, modelFlow: ModelFlow | undefined, suggestions: Command[], activityShown: boolean): number {
  // Root padding, header, transcript margin, composer, and footer consume nine
  // rows before optional panels are rendered.
  let rows = 9;
  if (activityShown) rows += 2;
  if (suggestions.length > 0) rows += Math.min(4, suggestions.length) + 2;
  if (plan) rows += plan.actions.length + 5;
  if (modelFlow) rows += Math.min(6, modelFlow.options.length) + 5;
  if (connectFlow) {
    if (connectFlow.step === "providers") rows += connectFlow.presets.length + 5;
    else if (connectFlow.step === "oauth") rows += connectFlow.oauthLines.length + 5;
    else rows += 6;
  }
  return rows;
}

render(<App />, { exitOnCtrlC: false });
