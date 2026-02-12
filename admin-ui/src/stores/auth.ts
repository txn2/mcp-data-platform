import { create } from "zustand";

interface AuthState {
  apiKey: string;
  setApiKey: (key: string) => void;
  clearApiKey: () => void;
  isAuthenticated: () => boolean;
}

const STORAGE_KEY = "mcp-admin-api-key";

export const useAuthStore = create<AuthState>((set, get) => ({
  apiKey: sessionStorage.getItem(STORAGE_KEY) ?? "",
  setApiKey: (key: string) => {
    sessionStorage.setItem(STORAGE_KEY, key);
    set({ apiKey: key });
  },
  clearApiKey: () => {
    sessionStorage.removeItem(STORAGE_KEY);
    set({ apiKey: "" });
  },
  isAuthenticated: () => get().apiKey.length > 0,
}));
