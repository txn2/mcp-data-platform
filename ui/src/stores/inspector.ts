import { create } from "zustand";

export interface ReplayIntent {
  tool_name: string;
  connection: string;
  parameters: Record<string, unknown>;
  event_id: string;
  event_timestamp: string;
}

interface InspectorState {
  replayIntent: ReplayIntent | null;
  setReplayIntent: (intent: ReplayIntent) => void;
  consumeReplayIntent: () => ReplayIntent | null;
}

export const useInspectorStore = create<InspectorState>((set, get) => ({
  replayIntent: null,
  setReplayIntent: (intent) => set({ replayIntent: intent }),
  consumeReplayIntent: () => {
    const intent = get().replayIntent;
    if (intent) set({ replayIntent: null });
    return intent;
  },
}));
