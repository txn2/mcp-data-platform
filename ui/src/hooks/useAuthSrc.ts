import { useState, useEffect } from "react";
import { authedFetch } from "@/api/authed";
import { useAuthStore } from "@/stores/auth";

/** A resolved image source, and whether resolving it failed. */
export interface AuthSrc {
  /** What to put in the element's src, or undefined until one is resolved. */
  src?: string;
  /**
   * Whether the fetch that would have produced one was refused or errored.
   *
   * It is distinct from an absent src, which is also the state while the fetch
   * is in flight: a caller with a fallback to draw needs to know which of the
   * two it is looking at, and on an API-key session there is no <img> load
   * event to tell it (#1568).
   */
  failed: boolean;
}

/**
 * Resolves a URL this session is entitled to read into something an <img> can
 * take.
 *
 * In cookie auth the browser sends credentials on the element's own request, so
 * the URL is handed back untouched. In API-key auth an <img src> carries no
 * `X-API-Key` and is answered 401, so the bytes are fetched with the session's
 * credentials and handed over as a blob URL instead.
 */
export function useAuthSrc(url: string | undefined): AuthSrc {
  const authMethod = useAuthStore((s) => s.authMethod);
  const [state, setState] = useState<AuthSrc>({ failed: false });

  useEffect(() => {
    if (!url) {
      setState({ failed: false });
      return;
    }

    // Cookie auth: browser sends credentials automatically on <img> tags, and
    // the element's own error event reports a failure.
    if (authMethod !== "apikey") {
      setState({ src: url, failed: false });
      return;
    }

    let revoke: string | undefined;

    authedFetch(url)
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.blob();
      })
      .then((blob) => {
        revoke = URL.createObjectURL(blob);
        setState({ src: revoke, failed: false });
      })
      .catch(() => {
        setState({ failed: true });
      });

    return () => {
      if (revoke) URL.revokeObjectURL(revoke);
    };
  }, [url, authMethod]);

  return state;
}
