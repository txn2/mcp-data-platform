import { create } from "zustand";

/** User profile returned by GET /api/v1/portal/me. */
export interface UserProfile {
  user_id: string;
  email?: string;
  roles: string[];
  is_admin: boolean;
  persona?: string;
  tools?: string[];
}

type AuthMethod = "cookie" | "apikey" | null;

interface AuthState {
  /** Authenticated user profile (null = not authenticated). */
  user: UserProfile | null;
  /** How the user is authenticated. */
  authMethod: AuthMethod;
  /** API key stored in sessionStorage (for API-key auth mode). */
  apiKey: string;
  /** True while the initial session check is in progress. */
  loading: boolean;
  /** True when a previously valid session has expired (401 detected). */
  sessionExpired: boolean;

  /**
   * Check for an existing session cookie by calling GET /api/v1/portal/me
   * with credentials: 'include'. If valid, sets user + authMethod='cookie'.
   * If not, falls back to checking sessionStorage for an API key.
   */
  checkSession: () => Promise<void>;

  /** Redirect to the OIDC login endpoint. */
  loginOIDC: () => void;

  /** Authenticate with an API key (validates, then stores in sessionStorage). */
  loginApiKey: (key: string) => Promise<void>;

  /** Log out: clear cookie (redirect to /portal/auth/logout) or clear API key. */
  logout: () => void;

  /** Mark the session as expired and clear auth state. */
  expireSession: () => void;

  /** Convenience: true when user is non-null. */
  isAuthenticated: () => boolean;

  /** Convenience: true when user is admin. */
  isAdmin: () => boolean;
}

const API_KEY_STORAGE = "mcp-portal-api-key";

export const useAuthStore = create<AuthState>((set, get) => ({
  user: null,
  authMethod: null,
  apiKey: sessionStorage.getItem(API_KEY_STORAGE) ?? "",
  loading: true,
  sessionExpired: false,

  checkSession: async () => {
    set({ loading: true });

    // 1. Try cookie-based session (credentials: 'include' sends cookie).
    try {
      const res = await fetch("/api/v1/portal/me", {
        credentials: "include",
      });
      if (res.ok) {
        const profile = (await res.json()) as UserProfile;
        set({
          user: profile,
          authMethod: "cookie",
          loading: false,
          sessionExpired: false,
        });
        return;
      }
    } catch {
      // Server may be unreachable or cookie invalid — fall through.
    }

    // 2. Try API key from sessionStorage.
    const storedKey = sessionStorage.getItem(API_KEY_STORAGE) ?? "";
    if (storedKey) {
      try {
        const res = await fetch("/api/v1/portal/me", {
          headers: { "X-API-Key": storedKey },
        });
        if (res.ok) {
          const profile = (await res.json()) as UserProfile;
          set({
            user: profile,
            authMethod: "apikey",
            apiKey: storedKey,
            loading: false,
            sessionExpired: false,
          });
          return;
        }
      } catch {
        // Key invalid — clear it.
      }
      sessionStorage.removeItem(API_KEY_STORAGE);
    }

    // 3. Not authenticated.
    set({ user: null, authMethod: null, apiKey: "", loading: false });
  },

  loginOIDC: () => {
    window.location.href = "/portal/auth/login";
  },

  loginApiKey: async (key: string) => {
    const res = await fetch("/api/v1/portal/me", {
      headers: { "X-API-Key": key },
    });
    if (!res.ok) {
      throw new Error(
        res.status === 401 ? "Invalid API key" : `Server error (${res.status})`,
      );
    }
    const profile = (await res.json()) as UserProfile;
    sessionStorage.setItem(API_KEY_STORAGE, key);
    set({
      user: profile,
      authMethod: "apikey",
      apiKey: key,
      loading: false,
      sessionExpired: false,
    });
  },

  logout: () => {
    const method = get().authMethod;
    sessionStorage.removeItem(API_KEY_STORAGE);
    set({ user: null, authMethod: null, apiKey: "", loading: false });

    if (method === "cookie") {
      // Redirect to server-side logout which clears the cookie and redirects
      // to the OIDC end_session_endpoint.
      window.location.href = "/portal/auth/logout";
    }
  },

  expireSession: () => {
    sessionStorage.removeItem(API_KEY_STORAGE);
    set({
      user: null,
      authMethod: null,
      apiKey: "",
      loading: false,
      sessionExpired: true,
    });
  },

  isAuthenticated: () => get().user !== null,
  isAdmin: () => get().user?.is_admin === true,
}));
