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
 */
export interface TryItSession {
  history: HistoryEntry[];
  setHistory: React.Dispatch<React.SetStateAction<HistoryEntry[]>>;
  latestResult: ToolCallResponse | null;
  setLatestResult: React.Dispatch<
    React.SetStateAction<ToolCallResponse | null>
  >;
  showRaw: boolean;
  setShowRaw: React.Dispatch<React.SetStateAction<boolean>>;
  historyOpen: boolean;
  setHistoryOpen: React.Dispatch<React.SetStateAction<boolean>>;
  replayParams: Record<string, unknown> | null;
  setReplayParams: React.Dispatch<
    React.SetStateAction<Record<string, unknown> | null>
  >;
  replaySource: ReplaySource | null;
  setReplaySource: React.Dispatch<React.SetStateAction<ReplaySource | null>>;
  formVersion: number;
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
  const bumpFormVersion = () => setFormVersion((v) => v + 1);

  // Reset session state when the selected tool changes. Tab toggles do
  // NOT trigger this — the hook only re-fires the reset on toolName
  // change because that's the only dependency.
  useEffect(() => {
    setHistory([]);
    setLatestResult(null);
    setShowRaw(false);
    setReplayParams(null);
    setReplaySource(null);
    setFormVersion((v) => v + 1);
  }, [toolName]);

  // Consume a replay intent from the inspector store (set by EventDrawer).
  // Only fires when the requested tool matches the current one;
  // intents for other tools stay in the store for the matching mount.
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
    history,
    setHistory,
    latestResult,
    setLatestResult,
    showRaw,
    setShowRaw,
    historyOpen,
    setHistoryOpen,
    replayParams,
    setReplayParams,
    replaySource,
    setReplaySource,
    formVersion,
    bumpFormVersion,
  };
}
