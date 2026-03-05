import { create } from "zustand";

type TimeRangePreset = "1h" | "6h" | "24h" | "7d";

interface TimeRangeState {
  preset: TimeRangePreset;
  setPreset: (preset: TimeRangePreset) => void;
  getStartTime: () => string;
  getEndTime: () => string;
}

const presetDurations: Record<TimeRangePreset, number> = {
  "1h": 60 * 60 * 1000,
  "6h": 6 * 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
};

// Snap to the current minute so React Query keys are stable across re-renders.
// Without this, Date.now() produces a new string every render, causing infinite
// refetching and data that never leaves the loading state.
function snapToMinute(ms: number): string {
  const snapped = Math.floor(ms / 60_000) * 60_000;
  return new Date(snapped).toISOString();
}

export const useTimeRangeStore = create<TimeRangeState>((set, get) => ({
  preset: "24h",
  setPreset: (preset) => set({ preset }),
  getStartTime: () => {
    const duration = presetDurations[get().preset];
    return snapToMinute(Date.now() - duration);
  },
  getEndTime: () => snapToMinute(Date.now()),
}));

export type { TimeRangePreset };
