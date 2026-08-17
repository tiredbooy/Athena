import React, { useEffect, useMemo, useRef, useState } from "react";
import { Box, useApp, useInput, useStdout } from "ink";
import { EngineClient } from "./engine/EngineClient.js";
import {
  appendTranscript,
  clearTranscript,
  initialSessionState,
  recordError,
  beginReconnect,
  reduceDiagnostic,
  reduceEvent,
  reduceProtocolError,
  type SessionState,
} from "./engine/session.js";
import type { EngineEvent, ProviderConnection, ProviderPreset } from "./protocol/types.js";
import {
  foldableActivityInView,
  nextTurnOffset,
  previousTurnOffset,
  turnIndexAtRow,
  userTurns,
  viewportTopRow,
} from "./scrollback.js";
import {
  buildTranscriptRows,
  windowTranscript,
  type Selection,
} from "./transcript.js";
import {
  formatActivity,
  hasLiveWork,
  lastFoldableActivityId,
  reservedRows,
  showActivity,
  toggleActivityFold,
} from "./ui/activity.js";
import { ActivityLine, Footer, Header, loadingPhrases, pulseFrames } from "./ui/chrome.js";
import { copyToClipboard, firstURL } from "./ui/clipboard.js";
import { Composer } from "./ui/composer.js";
import { fieldsFromPreset, modelSelectableCount, nextConnectField } from "./ui/catalog.js";
import { ConnectPanel, connectDefaultValue, connectPlaceholder } from "./ui/connect.js";
import { loadLastModel, loadRemoteVaultWarned, loadThemeName, loadVimMode, saveLastModel, saveRemoteVaultWarned, saveThemeName, saveVimMode, shouldRestoreModel } from "./prefs.js";
import { vimMotion } from "./ui/vim.js";
import { needsRemoteVaultWarning } from "./ui/remoteVault.js";
import { ThemeProvider, resolveTheme, themeIndex, themeNames, type ThemeFlow, type ThemeName } from "./theme.js";
import { ModelPanel } from "./ui/modelPicker.js";
import { CommandPalette } from "./ui/CommandPalette.js";
import { EmptyCard } from "./ui/EmptyCard.js";
import { resolveEmptyState } from "./ui/empty.js";
import { HelpPanel } from "./ui/HelpPanel.js";
import { isLocalCommand } from "./ui/help.js";
import { commandSuggestions, nextPaletteIndex, paletteHint, resolveCommandInput } from "./ui/palette.js";
import { ApprovalCard } from "./ui/planCard.js";
import { reviewKeyAction } from "./ui/planReview.js";
import { ThemePanel } from "./ui/themePicker.js";
import { handleMouseInput, TranscriptLine } from "./ui/transcript.js";

export function App(): React.ReactElement {
  const { exit } = useApp();
  const { stdout } = useStdout();
  const interactive = Boolean(process.stdin.isTTY && typeof process.stdin.setRawMode === "function");
  const [client] = useState(() => new EngineClient(process.env.ATHENA_ENGINE ?? "athena", ["engine"]));
  const [draft, setDraft] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [historyCursor, setHistoryCursor] = useState(-1);
  const [editorKey, setEditorKey] = useState(0);
  const [session, setSession] = useState(initialSessionState);
  const { messages, activity, modelName, status, connected, turnID, plan, planDecision, reviewing, connectFlow, modelFlow } = session;
  const [pulseIndex, setPulseIndex] = useState(0);
  const [loadingPhraseIndex, setLoadingPhraseIndex] = useState(0);
  const [selection, setSelection] = useState<Selection>();
  const [scrollOffset, setScrollOffset] = useState(0);
  const [themeName, setThemeName] = useState<ThemeName>(loadThemeName);
  const [themeFlow, setThemeFlow] = useState<ThemeFlow>();
  const [helpOpen, setHelpOpen] = useState(false);
  const [vimMode, setVimMode] = useState(loadVimMode);
  const [vimNormal, setVimNormal] = useState(false);
  const [paletteIndex, setPaletteIndex] = useState(0);
  const activityTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const activityState = useRef({ lastShownAt: 0, pending: "" });
  const previousTranscriptRows = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const reconnectAttempt = useRef(0);
  const lastProviderId = useRef(loadLastModel()?.providerId);

  function rememberModel(providerId: string, model: string) {
    if (!providerId.trim() || !model.trim()) return;
    lastProviderId.current = providerId;
    saveLastModel({ providerId, model });
  }

  function restoreAfterHello(ready: EngineEvent) {
    const saved = loadLastModel();
    if (!saved || !shouldRestoreModel(saved, ready)) return;
    void client.selectModel(saved.providerId, saved.model).catch((reason: Error) => fail(reason.message));
  }

  function patchSession(patch: Partial<SessionState> | ((current: SessionState) => SessionState)) {
    setSession((current) => typeof patch === "function" ? patch(current) : { ...current, ...patch });
  }

  function fail(message: string, extras: Partial<SessionState> = {}) {
    patchSession((current) => recordError(current, message, extras));
  }

  function setActivityNow(next: string) {
    if (activityTimer.current) clearTimeout(activityTimer.current);
    activityTimer.current = undefined;
    activityState.current.pending = "";
    activityState.current.lastShownAt = Date.now();
    patchSession({ activity: next });
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
      patchSession({ activity: pending });
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
      setSession((current) => {
        const next = reduceEvent(current, event);
        if (event.type === "activity") return { ...next, activity: current.activity };
        if (activityTimer.current) clearTimeout(activityTimer.current);
        activityTimer.current = undefined;
        activityState.current.pending = "";
        activityState.current.lastShownAt = Date.now();
        return next;
      });
      if (event.type === "activity" && event.activity) queueActivity(formatActivity(event.activity));
      if (event.type === "model.options") {
        const current = (event.models ?? []).find((option) => option.current);
        if (current) rememberModel(current.providerId, current.model);
      }
      if (event.type === "provider.connected" && event.model && lastProviderId.current) {
        rememberModel(lastProviderId.current, event.model);
      }
      if (event.type === "provider.oauth.progress" && event.message) {
        const loginURL = firstURL(event.message);
        if (loginURL) {
          void copyToClipboard(loginURL, stdout).then((copied) => {
            if (copied) setActivityNow("Sign-in link copied to clipboard");
          });
        }
      }
    };
    const onClose = (reason: Error) => {
      if (client.isDisposed) return;
      patchSession((current) => beginReconnect(current, reason));
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
      const delay = Math.min(5000, 400 * 2 ** reconnectAttempt.current);
      reconnectAttempt.current += 1;
      reconnectTimer.current = setTimeout(() => {
        reconnectTimer.current = undefined;
        if (client.isDisposed) return;
        try {
          client.reconnect();
        } catch (error) {
          fail(error instanceof Error ? error.message : String(error), { status: "starting", activity: "Reconnect failed — retrying…" });
          return;
        }
        void client.hello().then((ready) => {
          reconnectAttempt.current = 0;
          restoreAfterHello(ready);
        }).catch((error: Error) => {
          fail(error.message, { status: "starting", activity: "Reconnect failed — retrying…" });
        });
      }, delay);
    };
    const onProtocolError = (reason: Error) => patchSession((current) => reduceProtocolError(current, reason));
    const onDiagnostic = (line: string) => patchSession((current) => reduceDiagnostic(current, line));
    client.on("event", onEvent);
    client.on("close", onClose);
    client.on("protocolError", onProtocolError);
    client.on("diagnostic", onDiagnostic);
    void client.hello().then((ready) => restoreAfterHello(ready)).catch((reason: Error) => fail(reason.message, { status: "error" }));
    return () => {
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
      client.off("event", onEvent);
      client.off("close", onClose);
      client.off("protocolError", onProtocolError);
      client.off("diagnostic", onDiagnostic);
      client.dispose();
    };
  }, [client]);

  useInput((input, key) => {
    if (input.includes("[<")) {
      handleMouseInput(input, mouseState.current, setSelection, (copiedText) => {
        void copyToClipboard(copiedText, stdout).then((copied) => {
          if (copied) patchSession({ activity: `Copied ${copiedText.length} characters`, status: "ready" });
          else fail("Could not access a clipboard. Your terminal may not support OSC 52.", { status: "error" });
          setEditorKey((current) => current + 1);
        });
      }, scrollTranscript, (id) => patchSession((current) => ({ ...current, messages: toggleActivityFold(current.messages, id) })));
      return;
    }
    if (key.ctrl && input === "o") {
      const turns = userTurns(messages, transcriptRows);
      const id = foldableActivityInView(turns, transcriptRows.length, transcriptHeight, scrollOffset, lastFoldableActivityId(messages));
      if (id !== undefined) patchSession((current) => ({ ...current, messages: toggleActivityFold(current.messages, id) }));
      setEditorKey((current) => current + 1);
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
    if (key.ctrl && (key.upArrow || key.downArrow)) {
      const turns = userTurns(messages, transcriptRows);
      const next = key.upArrow
        ? previousTurnOffset(turns, transcriptRows.length, transcriptHeight, scrollOffset)
        : nextTurnOffset(turns, transcriptRows.length, transcriptHeight, scrollOffset);
      setSelection(undefined);
      setScrollOffset(next);
      const index = turnIndexAtRow(turns, viewportTopRow(transcriptRows.length, transcriptHeight, next));
      if (index >= 0) setActivityNow(`Turn ${index + 1} / ${turns.length}`);
      return;
    }
    if (helpOpen) {
      if (key.escape) {
        setHelpOpen(false);
        setActivityNow("Help closed");
        resetComposer();
      }
      return;
    }
    if (themeFlow) {
      if (key.escape) {
        setThemeName(themeFlow.saved);
        setThemeFlow(undefined);
        setActivityNow("Theme preview discarded");
        resetComposer();
        return;
      }
      if (key.upArrow) {
        const selectedIndex = nextPaletteIndex(themeFlow.selectedIndex, themeNames.length, -1);
        setThemeFlow({ ...themeFlow, selectedIndex });
        setThemeName(themeNames[selectedIndex]);
        return;
      }
      if (key.downArrow) {
        const selectedIndex = nextPaletteIndex(themeFlow.selectedIndex, themeNames.length, 1);
        setThemeFlow({ ...themeFlow, selectedIndex });
        setThemeName(themeNames[selectedIndex]);
        return;
      }
      if (key.return) {
        const selected = themeNames[themeFlow.selectedIndex];
        setThemeName(selected);
        saveThemeName(selected);
        setThemeFlow(undefined);
        setActivityNow(`Theme · ${selected}`);
        resetComposer();
        return;
      }
      return;
    }
    if (modelFlow) {
      if (key.escape) {
        patchSession({ modelFlow: undefined, activity: "Model selection closed" });
        resetComposer();
        return;
      }
      if (modelFlow.selecting) return;
      const selectable = modelSelectableCount(modelFlow.options);
      if (selectable === 0) return;
      if (key.upArrow) {
        patchSession({ modelFlow: { ...modelFlow, selectedIndex: (modelFlow.selectedIndex - 1 + selectable) % selectable } });
        return;
      }
      if (key.downArrow) {
        patchSession({ modelFlow: { ...modelFlow, selectedIndex: (modelFlow.selectedIndex + 1) % selectable } });
        return;
      }
      if (key.return) {
        if (modelFlow.selectedIndex >= modelFlow.options.length) {
          patchSession({ modelFlow: undefined, error: undefined, status: "working", activity: "Loading provider options…" });
          resetComposer();
          void client.providers().catch((reason: Error) => fail(reason.message, { status: "error" }));
          return;
        }
        const selected = modelFlow.options[modelFlow.selectedIndex];
        if (!selected) return;
        patchSession({ modelFlow: { ...modelFlow, selecting: true }, status: "working", activity: `Switching to ${selected.model}…` });
        rememberModel(selected.providerId, selected.model);
        void client.selectModel(selected.providerId, selected.model).catch((reason: Error) => {
          fail(reason.message, { status: "error" });
          patchSession((current) => current.modelFlow ? { ...current, modelFlow: { ...current.modelFlow, selecting: false } } : current);
        });
        return;
      }
      return;
    }
    if (connectFlow) {
      if (connectFlow.step === "remote-warning") {
        if (key.escape || input.toLowerCase() === "n") {
          patchSession({ connectFlow: { ...connectFlow, step: "providers" }, activity: "Choose a provider" });
          resetComposer();
          return;
        }
        if (key.return || input.toLowerCase() === "y") {
          saveRemoteVaultWarned();
          continueConnectPreset();
          return;
        }
        return;
      }
      if (key.escape) {
        if (connectFlow.step === "oauth" && turnID) void client.cancel(turnID).catch((reason: Error) => fail(reason.message));
        else patchSession({ connectFlow: undefined, activity: "Connection closed" });
        resetComposer();
        return;
      }
      if (connectFlow.step === "providers" && connectFlow.presets.length > 0) {
        if (key.upArrow) {
          patchSession({ connectFlow: { ...connectFlow, selectedIndex: (connectFlow.selectedIndex - 1 + connectFlow.presets.length) % connectFlow.presets.length } });
          return;
        }
        if (key.downArrow) {
          patchSession({ connectFlow: { ...connectFlow, selectedIndex: (connectFlow.selectedIndex + 1) % connectFlow.presets.length } });
          return;
        }
      }
      if (key.upArrow || key.downArrow) return;
    }
    if (suggestions.length > 0) {
      if (key.escape) {
        resetComposer();
        return;
      }
      if (key.upArrow) {
        setPaletteIndex((current) => nextPaletteIndex(current, suggestions.length, -1));
        return;
      }
      if (key.downArrow) {
        setPaletteIndex((current) => nextPaletteIndex(current, suggestions.length, 1));
        return;
      }
      if (key.tab || input === "\t") {
        const completed = resolveCommandInput(draft, suggestions, paletteIndex);
        setDraft(completed);
        setPaletteIndex(0);
        setHistoryCursor(-1);
        setEditorKey((current) => current + 1);
        return;
      }
    }
    if (plan && planDecision === "waiting" && reviewing) {
      const action = reviewKeyAction(input, key);
      if (action === "approve") approvePendingPlan();
      else if (action === "reject") rejectPendingPlan();
      else if (action === "back") {
        patchSession({ reviewing: false, activity: "Review paused — Tab to return" });
        resetComposer();
      }
      return;
    }
    if (plan && planDecision !== "waiting") return;
    if (plan && (key.tab || input === "\t") && draft.trim() === "") {
      patchSession({ reviewing: true });
      resetComposer();
      return;
    }
    if (key.escape && turnID) {
      void client.cancel(turnID).catch((reason: Error) => fail(reason.message));
      setActivityNow("Cancellation requested…");
      return;
    }
    if (vimMode && !vimNormal && key.escape) {
      setVimNormal(true);
      setActivityNow("Vim · normal");
      resetComposer();
      return;
    }
    if (vimMode && vimNormal) {
      const motion = vimMotion(input);
      if (motion === "insert") {
        setVimNormal(false);
        setActivityNow("Vim · insert");
        resetComposer();
        return;
      }
      if (motion === "older" || motion === "newer") {
        const turns = userTurns(messages, transcriptRows);
        const next = motion === "older"
          ? previousTurnOffset(turns, transcriptRows.length, transcriptHeight, scrollOffset)
          : nextTurnOffset(turns, transcriptRows.length, transcriptHeight, scrollOffset);
        setSelection(undefined);
        setScrollOffset(next);
        const index = turnIndexAtRow(turns, viewportTopRow(transcriptRows.length, transcriptHeight, next));
        if (index >= 0) setActivityNow(`Turn ${index + 1} / ${turns.length}`);
        return;
      }
      if (motion === "top") {
        setSelection(undefined);
        setScrollOffset(transcript.maxOffset);
        setActivityNow("Top");
        return;
      }
      if (motion === "bottom") {
        setSelection(undefined);
        setScrollOffset(0);
        setActivityNow("Bottom");
        return;
      }
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
  const empty = resolveEmptyState({ status, connected, hasModel: session.hasModel, messageCount: messages.length });
  const engineBanner = empty?.kind === "engine-down" && messages.length > 0;
  const suggestions = helpOpen || connectFlow || modelFlow || reviewing || themeFlow ? [] : commandSuggestions(draft);
  const activityShown = showActivity(status, activity, hasLiveWork(messages));
  const transcriptHeight = Math.max(3, terminalHeight - reservedRows(plan, connectFlow, modelFlow, suggestions, activityShown, themeFlow, helpOpen, engineBanner));
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
  const prompt = helpOpen
    ? "Esc close help"
    : themeFlow
    ? "↑↓ preview · Enter save · Esc revert"
    : modelFlow
    ? modelFlow.selecting ? "Switching model…" : "↑↓ choose · Enter select · Esc close"
    : connectFlow
    ? connectFlow.step === "oauth" ? "Esc cancel · complete sign-in in your browser"
    : connectFlow.step === "remote-warning" ? "Y/Enter continue · Esc/N back"
    : "Enter continue · Esc close"
    : plan
    ? planDecision === "applying"
      ? "Applying approved changes…"
      : planDecision === "discarding"
        ? "Discarding proposed changes…"
        : reviewing
          ? "[Y] apply · [N]/[R] discard · Esc back"
          : "Tab review card · /cancel discard"
    : suggestions.length > 0
      ? paletteHint
    : turnID
      ? "[Esc] cancel turn"
      : vimMode && vimNormal
        ? "j/k turns · g/G top/bottom · i insert"
        : vimMode
          ? "Enter send · Esc normal · / commands"
          : "Enter send · / commands · ↑↓ history · Ctrl+C quit";

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
    setPaletteIndex(0);
    setEditorKey((current) => current + 1);
  }

  function handleDraftChange(value: string) {
    // Ink's text input also receives the mouse escape sequence. The parent
    // consumes it for transcript selection; never let it become composer text.
    if (value.includes("[<") || value.includes("\t")) return;
    setDraft(value);
    setHistoryCursor(-1);
    setPaletteIndex(0);
  }

  function submitDraft(value: string) {
    if (connectFlow) {
      submitConnectStep(value.trim());
      return;
    }
    const inputText = resolveCommandInput(value, suggestions, paletteIndex);
    if (!inputText || status === "working" || (status === "starting" && !isLocalCommand(inputText))) return;
    if (inputText === "/clear") {
      patchSession((current) => ({ ...clearTranscript(current), activity: "Transcript cleared" }));
      setSelection(undefined);
      setScrollOffset(0);
      resetComposer();
      return;
    }
    if (inputText === "/reset") {
      remember(inputText);
      resetComposer();
      setSelection(undefined);
      setScrollOffset(0);
      patchSession({ error: undefined, status: "working", activity: "Resetting the engine session…" });
      void client.reset().catch((reason: Error) => fail(reason.message, { status: "error" }));
      return;
    }
    if (inputText === "/help") {
      remember(inputText);
      resetComposer();
      setHelpOpen(true);
      setActivityNow("Keys and commands");
      return;
    }
    if (!connected && !isLocalCommand(inputText)) return;
    if (inputText === "/cancel") {
      remember(inputText);
      resetComposer();
      patchSession({ error: undefined });
      if (plan && planDecision === "waiting") {
        rejectPendingPlan();
        return;
      }
      setScrollOffset(0);
      patchSession((current) => ({
        ...appendTranscript(current, "user", inputText),
        status: "working",
        activity: "Discarding pending plan or question…",
      }));
      void client.submit("/cancel").catch((reason: Error) => fail(reason.message, { status: "error" }));
      return;
    }
    if (inputText === "/vim-mode") {
      remember(inputText);
      resetComposer();
      const next = !vimMode;
      setVimMode(next);
      saveVimMode(next);
      setVimNormal(false);
      setActivityNow(next ? "Vim mode on · Esc normal · i insert" : "Vim mode off");
      return;
    }
    if (inputText === "/theme") {
      remember(inputText);
      resetComposer();
      patchSession({ error: undefined, activity: "Choose a theme" });
      setThemeFlow({ selectedIndex: themeIndex(themeName), saved: themeName });
      return;
    }
    if (inputText === "/connect") {
      remember(inputText);
      resetComposer();
      patchSession({ error: undefined, status: "working", activity: "Loading provider options…" });
      void client.providers().catch((reason: Error) => fail(reason.message, { status: "error" }));
      return;
    }
    if (inputText === "/models") {
      remember(inputText);
      resetComposer();
      patchSession({ error: undefined, status: "working", activity: "Loading available models…" });
      void client.models().catch((reason: Error) => fail(reason.message, { status: "error", modelFlow: undefined }));
      return;
    }
    setScrollOffset(0);
    remember(inputText);
    resetComposer();
    patchSession((current) => ({
      ...appendTranscript(current, "user", inputText),
      error: undefined,
      status: "working",
      activity: "Sending to the local engine…",
    }));
    void client.submit(inputText).catch((reason: Error) => fail(reason.message, { status: "error" }));
  }

  function continueConnectPreset(choice?: { preset: ProviderPreset; values: ProviderConnection }) {
    if (!connectFlow) return;
    const preset = choice?.preset ?? connectFlow.preset;
    const values = choice?.values ?? connectFlow.values;
    if (!preset) return;
    lastProviderId.current = preset.id;
    if (preset.auth === "oauth") {
      patchSession({
        connectFlow: { ...connectFlow, preset, values, fields: [], step: "oauth", oauthLines: ["Preparing secure device login…"] },
        status: "working",
        activity: `Starting ${preset.label} sign-in…`,
      });
      if (preset.chat_model) rememberModel(preset.id, preset.chat_model);
      void client.startOAuth(preset.id).catch((reason: Error) => fail(reason.message, { status: "error" }));
      return;
    }
    const fields = fieldsFromPreset(preset);
    if (preset.auth === "none" || fields.length === 0) {
      saveProviderConnection(values);
      return;
    }
    patchSession({ connectFlow: { ...connectFlow, preset, values, fields, step: fields[0] } });
    setDraft(connectDefaultValue(fields[0], values));
    setEditorKey((current) => current + 1);
  }

  function submitConnectStep(value: string) {
    if (!connectFlow || connectFlow.step === "oauth" || connectFlow.step === "saving") return;
    patchSession({ error: undefined });
    if (connectFlow.step === "remote-warning") {
      saveRemoteVaultWarned();
      continueConnectPreset();
      return;
    }
    if (connectFlow.step === "providers") {
      const preset = connectFlow.presets[connectFlow.selectedIndex];
      if (!preset) return;
      if (!preset.available) {
        fail(preset.unavailable ?? `${preset.label} is unavailable`);
        return;
      }
      const values: ProviderConnection = {
        name: preset.name ?? "",
        type: preset.type,
        base_url: preset.base_url ?? "",
        api_key_env: preset.api_key_env,
        chat_model: preset.chat_model ?? "",
      };
      lastProviderId.current = preset.id;
      if (needsRemoteVaultWarning(preset, loadRemoteVaultWarned())) {
        patchSession({
          connectFlow: { ...connectFlow, preset, values, fields: fieldsFromPreset(preset), step: "remote-warning" },
          activity: "Remote provider warning",
        });
        resetComposer();
        return;
      }
      continueConnectPreset({ preset, values });
      return;
    }

    const values = { ...connectFlow.values };
    if (connectFlow.step === "name") values.name = value || values.name;
    if (connectFlow.step === "base_url") values.base_url = value || values.base_url;
    if (connectFlow.step === "api_key") values.api_key = value;
    if (connectFlow.step === "chat_model") values.chat_model = value || values.chat_model;
    if ((connectFlow.step === "name" && !values.name) || (connectFlow.step === "base_url" && !values.base_url)) {
      fail("This field is required.");
      return;
    }
    const next = nextConnectField(connectFlow.fields, connectFlow.step);
    if (!next) {
      if (!values.chat_model) {
        fail("A default chat model is required.");
        return;
      }
      saveProviderConnection(values);
      return;
    }
    patchSession({ connectFlow: { ...connectFlow, values, step: next } });
    setDraft(connectDefaultValue(next, values));
    setEditorKey((current) => current + 1);
  }

  function saveProviderConnection(values: ProviderConnection) {
    patchSession((current) => ({
      ...current,
      connectFlow: current.connectFlow ? { ...current.connectFlow, values, step: "saving" } : current.connectFlow,
      status: "working",
      activity: "Saving provider locally…",
    }));
    setDraft("");
    setEditorKey((current) => current + 1);
    void client.connect(values).catch((reason: Error) => {
      fail(reason.message, { status: "error" });
      patchSession((current) => current.connectFlow
        ? { ...current, connectFlow: { ...current.connectFlow, step: current.connectFlow.fields[current.connectFlow.fields.length - 1] ?? "chat_model" } }
        : current);
    });
  }

  function approvePendingPlan() {
    if (!plan || planDecision !== "waiting") return;
    patchSession({ planDecision: "applying", reviewing: true, status: "working", error: undefined, activity: "Applying approved changes…" });
    void client.approve(plan.id).catch((reason: Error) => {
      fail(reason.message, { planDecision: "waiting", reviewing: true, status: "approval", activity: "Approval failed — choose again" });
    });
  }

  function rejectPendingPlan() {
    if (!plan || planDecision !== "waiting") return;
    patchSession({ planDecision: "discarding", reviewing: true, status: "working", error: undefined, activity: "Discarding proposed changes…" });
    void client.reject(plan.id).catch((reason: Error) => {
      fail(reason.message, { planDecision: "waiting", reviewing: true, status: "approval", activity: "Discard failed — choose again" });
    });
  }

  function remember(inputText: string) {
    setHistory((current) => current[current.length - 1] === inputText ? current : [...current, inputText].slice(-30));
  }

  function resetComposer() {
    setDraft("");
    setHistoryCursor(-1);
    setPaletteIndex(0);
    setEditorKey((current) => current + 1);
  }

  const theme = resolveTheme(themeName);
  return (
    <ThemeProvider theme={theme}>
      <Box flexDirection="column" width={terminalWidth} height={terminalHeight} paddingX={2} paddingY={1} backgroundColor={theme.bg}>
        <Header />
        <Box flexDirection="column" height={transcriptHeight} flexShrink={0} overflow="hidden" marginTop={1}>
          {messages.length === 0
            ? empty && <EmptyCard state={empty} />
            : transcript.rows.map((row) => <TranscriptLine key={row.key} row={row} selection={selection} />)}
        </Box>
        {engineBanner && empty && <EmptyCard state={empty} banner />}
        {plan && <ApprovalCard plan={plan} decision={planDecision} focused={reviewing && planDecision === "waiting"} />}
        {connectFlow && <ConnectPanel flow={connectFlow} />}
        {modelFlow && <ModelPanel flow={modelFlow} />}
        {themeFlow && <ThemePanel flow={themeFlow} />}
        {helpOpen && <HelpPanel />}
        {suggestions.length > 0 && <CommandPalette suggestions={suggestions} selectedIndex={paletteIndex} />}
        {activityShown && <ActivityLine activity={activity} status={status} pulse={pulseFrames[pulseIndex]} phrase={loadingPhrases[loadingPhraseIndex]} />}
        <Composer key={editorKey} draft={draft} focus={!vimNormal && !helpOpen && !reviewing && planDecision === "waiting" && !modelFlow && !themeFlow && connectFlow?.step !== "oauth" && connectFlow?.step !== "saving" && interactive} onChange={handleDraftChange} onSubmit={submitDraft} error={status === "error"} placeholder={vimNormal ? "i insert · j/k turns · g/G top/bottom" : helpOpen ? "Esc closes help" : !connected && status === "error" ? "Engine is down — restart Athena" : !session.hasModel && !connectFlow && !modelFlow ? "/connect or /models" : reviewing ? "Review the plan above" : plan ? "Tab to review the plan" : themeFlow ? "Choose a theme above" : modelFlow ? "Choose a model above" : connectPlaceholder(connectFlow)} mask={connectFlow?.step === "api_key" ? "•" : undefined} />
        <Footer prompt={prompt} model={modelName} scrollOffset={transcript.offset} maxScrollOffset={transcript.maxOffset} />
      </Box>
    </ThemeProvider>
  );
}
