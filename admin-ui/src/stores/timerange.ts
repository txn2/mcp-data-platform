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

export const useTimeRangeStore = create<TimeRangeState>((set, get) => ({
  preset: "24h",
  setPreset: (preset) => set({ preset }),
  getStartTime: () => {
    const duration = presetDurations[get().preset];
    return new Date(Date.now() - duration).toISOString();
  },
  getEndTime: () => new Date().toISOString(),
}));

export type { TimeRangePreset };
