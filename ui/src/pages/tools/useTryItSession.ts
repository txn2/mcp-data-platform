import { useEffect, useRef, useState } from "react";
import { useInspectorStore } from "@/stores/inspector";
import type { ReplayIntent } from "@/stores/inspector";
import type { ToolCallResponse } from "@/api/admin/types";

/**
 * Per-tool Try It state: history list, latest result, replay context.
 *
 * Owns the state that must survive tab switches inside ToolDetail.
 * Mount this hook at ToolDetail's level (the parent that stays alive
 * across tab toggles) and pass the returned object into TryItTab as
 * a prop. Calling it directly inside TryItTab would lose state every
 * time the user clicks away and back, which was the regression
 * tracked as #343 bug 3.
 *
 * The hook automatically resets when toolName changes, mirroring the
 * "select a different tool from the list" UX. State is intentionally
 * NOT persisted to storage — replays of test calls should not survive
 * a page reload.
 *
 * The returned shape is intent-based, not setter-based: callers invoke
 * named actions (`addHistoryEntry`, `clearHistory`, `applyReplay`,
 * `dismissReplay`, etc.) rather than receiving raw `useState` setters.
 * This locks down the API surface and prevents inconsistent partial
 * updates from drifting in over time.
 */
export interface TryItSession {
  // ── State ────────────────────────────────────────────────────────
  history: HistoryEntry[];
  latestResult: ToolCallResponse | null;
  showRaw: boolean;
  historyOpen: boolean;
  replayParams: Record<string, unknown> | null;
  replaySource: ReplaySource | null;
  formVersion: number;

  // ── History actions ──────────────────────────────────────────────
  /** Prepend a new entry to the history (called when a tool call starts). */
  addHistoryEntry: (entry: HistoryEntry) => void;
  /** Patch a single history entry by id (called when a call completes). */
  updateHistoryEntry: (id: string, patch: Partial<HistoryEntry>) => void;
  /** Empty the history list. */
  clearHistory: () => void;

  // ── Result actions ───────────────────────────────────────────────
  /** Replace the displayed latest result; pass null to clear. */
  setLatestResult: (result: ToolCallResponse | null) => void;
  /** Toggle the raw-JSON view. */
  toggleRaw: () => void;
  /** Toggle the history list's collapse state. */
  toggleHistory: () => void;

  // ── Replay actions ───────────────────────────────────────────────
  /**
   * Apply a replay context (params + optional audit-event source).
   * Clears the latest result and raw view, and bumps formVersion so
   * the form re-renders with the new initial values.
   */
  applyReplay: (args: {
    params: Record<string, unknown>;
    source: ReplaySource | null;
  }) => void;
  /** Clear the replay banner and any pending replay params. */
  dismissReplay: () => void;

  // ── Form re-render token ─────────────────────────────────────────
  /**
   * Force ToolForm to re-mount its uncontrolled fields. Used when
   * replayParams change but their object identity wouldn't (e.g.
   * replaying the same audit event twice). Counter, not a setter.
   */
  bumpFormVersion: () => void;
}

export interface HistoryEntry {
  id: string;
  timestamp: string;
  parameters: Record<string, unknown>;
  response: ToolCallResponse | null;
  is_loading: boolean;
}

export interface ReplaySource {
  event_id: string;
  event_timestamp: string;
}

export function useTryItSession(toolName: string): TryItSession {
  const replayIntent = useInspectorStore((s) => s.replayIntent);
  const consumeReplayIntent = useInspectorStore((s) => s.consumeReplayIntent);

  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [latestResult, setLatestResult] = useState<ToolCallResponse | null>(
    null,
  );
  const [showRaw, setShowRaw] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(true);
  const [replayParams, setReplayParams] = useState<
    Record<string, unknown> | null
  >(null);
  const [replaySource, setReplaySource] = useState<ReplaySource | null>(null);
  const [formVersion, setFormVersion] = useState(0);

  // ── Action implementations ─────────────────────────────────────────
  const addHistoryEntry = (entry: HistoryEntry) =>
    setHistory((prev) => [entry, ...prev]);

  const updateHistoryEntry = (id: string, patch: Partial<HistoryEntry>) =>
    setHistory((prev) =>
      prev.map((h) => (h.id === id ? { ...h, ...patch } : h)),
    );

  const clearHistory = () => setHistory([]);

  const toggleRaw = () => setShowRaw((v) => !v);
  const toggleHistory = () => setHistoryOpen((v) => !v);

  const bumpFormVersion = () => setFormVersion((v) => v + 1);

  const applyReplay = (args: {
    params: Record<string, unknown>;
    source: ReplaySource | null;
  }) => {
    setReplayParams(args.params);
    setReplaySource(args.source);
    setLatestResult(null);
    setShowRaw(false);
    setFormVersion((v) => v + 1);
  };

  const dismissReplay = () => {
    setReplayParams(null);
    setReplaySource(null);
  };

  // ── Reset on tool change ──────────────────────────────────────────
  // Tab toggles do NOT trigger this — the hook only re-fires the reset
  // on toolName change because that's the only dependency.
  useEffect(() => {
    setHistory([]);
    setLatestResult(null);
    setShowRaw(false);
    setReplayParams(null);
    setReplaySource(null);
    setFormVersion((v) => v + 1);
  }, [toolName]);

  // ── Replay intent from EventDrawer ────────────────────────────────
  // Only fires when the requested tool matches the current one; intents
  // for other tools stay in the store for the matching mount.
  const consumedRef = useRef<string | null>(null);
  useEffect(() => {
    if (consumedRef.current === toolName) return;
    const intent: ReplayIntent | null = replayIntent;
    if (!intent || intent.tool_name !== toolName) return;
    consumeReplayIntent();
    consumedRef.current = toolName;
    setReplayParams(intent.parameters);
    setReplaySource({
      event_id: intent.event_id,
      event_timestamp: intent.event_timestamp,
    });
    setLatestResult(null);
    setShowRaw(false);
    setFormVersion((v) => v + 1);
  }, [replayIntent, consumeReplayIntent, toolName]);

  return {
    // State
    history,
    latestResult,
    showRaw,
    historyOpen,
    replayParams,
    replaySource,
    formVersion,
    // History actions
    addHistoryEntry,
    updateHistoryEntry,
    clearHistory,
    // Result actions
    setLatestResult,
    toggleRaw,
    toggleHistory,
    // Replay actions
    applyReplay,
    dismissReplay,
    // Form re-render token
    bumpFormVersion,
  };
}
