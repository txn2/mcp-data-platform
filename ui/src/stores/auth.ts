import { create } from "zustand";
import { buildLoginURL } from "@/lib/loginUrl";

/** User profile returned by GET /api/v1/portal/me. */
export interface UserProfile {
  user_id: string;
  email?: string;
  roles: string[];
  is_admin: boolean;
  persona?: string;
  tools?: string[];
  /**
   * CSRF token bound to the browser session. Present only for cookie-based
   * sessions; API-key auth is exempt and omits it. The SPA echoes it in the
   * X-CSRF-Token header on state-changing requests.
   */
  csrf_token?: string;
}

type AuthMethod = "cookie" | "apikey" | null;

interface AuthState {
  /** Authenticated user profile (null = not authenticated). */
  user: UserProfile | null;
  /** How the user is authenticated. */
  authMethod: AuthMethod;
  /** API key stored in sessionStorage (for API-key auth mode). */
  apiKey: string;
  /**
   * CSRF token for the current cookie session (empty in API-key mode). Sent
   * as the X-CSRF-Token header on state-changing cookie-authenticated requests.
   */
  csrfToken: string;
  /** True while the initial session check is in progress. */
  loading: boolean;
  /** True when a previously valid session has expired (401 detected). */
  sessionExpired: boolean;
  /**
   * True when the caller authenticated but their roles map to no persona (403
   * from /me). Distinct from being signed out: sending them to the login form
   * would loop them straight back through an identity provider that will
   * happily authenticate them again. The email they are signed in as, when the
   * server reported one.
   */
  accessDenied: boolean;
  deniedEmail: string;

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

/**
 * Read the account named by a 403 from /me, or "" when the body carries none.
 * The server answers the gate with RFC 9457 Problem Details plus an `email`
 * field; a body that is missing, unparseable, or shaped differently just means
 * the refusal is rendered without an address.
 */
async function deniedEmailFrom(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { email?: unknown };
    return typeof body.email === "string" ? body.email : "";
  } catch {
    return "";
  }
}

export const useAuthStore = create<AuthState>((set, get) => ({
  user: null,
  authMethod: null,
  apiKey: sessionStorage.getItem(API_KEY_STORAGE) ?? "",
  csrfToken: "",
  loading: true,
  sessionExpired: false,
  accessDenied: false,
  deniedEmail: "",

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
          csrfToken: profile.csrf_token ?? "",
          loading: false,
          sessionExpired: false,
          accessDenied: false,
          deniedEmail: "",
        });
        return;
      }
      // 403 means the session is valid but maps to no persona. Stop here: the
      // login form would send them back to an identity provider that will
      // authenticate them again and return them to this same refusal.
      if (res.status === 403) {
        // authMethod stays "cookie" so Sign out still clears the session
        // server-side; isAuthenticated reads user, which is null, so this
        // grants nothing.
        set({
          user: null,
          authMethod: "cookie",
          csrfToken: "",
          loading: false,
          accessDenied: true,
          deniedEmail: await deniedEmailFrom(res),
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
            csrfToken: "",
            loading: false,
            sessionExpired: false,
            accessDenied: false,
            deniedEmail: "",
          });
          return;
        }
        // A key that authenticates but maps to no persona is refused the same
        // way, and is kept rather than cleared: it is a valid credential, so
        // discarding it would misreport the problem as a bad key.
        if (res.status === 403) {
          set({
            user: null,
            authMethod: "apikey",
            apiKey: storedKey,
            csrfToken: "",
            loading: false,
            accessDenied: true,
            deniedEmail: await deniedEmailFrom(res),
          });
          return;
        }
      } catch {
        // Key invalid — clear it.
      }
      sessionStorage.removeItem(API_KEY_STORAGE);
    }

    // 3. Not authenticated.
    set({
      user: null,
      authMethod: null,
      apiKey: "",
      csrfToken: "",
      loading: false,
      accessDenied: false,
      deniedEmail: "",
    });
  },

  loginOIDC: () => {
    // Carry the current in-app location through the OIDC round-trip so an
    // unauthenticated user who opened a deep link lands back on it after login
    // instead of the default page. When the SPA renders the login form at a
    // protected deep link, window.location is that deep link (#710).
    window.location.href = buildLoginURL();
  },

  loginApiKey: async (key: string) => {
    const res = await fetch("/api/v1/portal/me", {
      headers: { "X-API-Key": key },
    });
    if (!res.ok) {
      if (res.status === 403) {
        throw new Error(
          "That key is valid, but the account it belongs to is not assigned to a persona. Ask an administrator to grant it access.",
        );
      }
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
      csrfToken: "",
      loading: false,
      sessionExpired: false,
      accessDenied: false,
      deniedEmail: "",
    });
  },

  logout: () => {
    const method = get().authMethod;
    sessionStorage.removeItem(API_KEY_STORAGE);
    set({
      user: null,
      authMethod: null,
      apiKey: "",
      csrfToken: "",
      loading: false,
      accessDenied: false,
      deniedEmail: "",
    });

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
      csrfToken: "",
      loading: false,
      sessionExpired: true,
    });
  },

  isAuthenticated: () => get().user !== null,
  isAdmin: () => get().user?.is_admin === true,
}));
