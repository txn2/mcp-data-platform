import { useState, useEffect } from "react";
import { useAuthStore } from "@/stores/auth";

/**
 * Fetches a URL with authentication and returns a blob URL for use in <img> tags.
 * In cookie auth mode, returns the original URL (browser sends cookies automatically).
 * In API key mode, fetches with the X-API-Key header and creates a blob URL.
 */
export function useAuthSrc(url: string | undefined): string | undefined {
  const authMethod = useAuthStore((s) => s.authMethod);
  const apiKey = useAuthStore((s) => s.apiKey);
  const [blobUrl, setBlobUrl] = useState<string | undefined>(undefined);

  useEffect(() => {
    if (!url) {
      setBlobUrl(undefined);
      return;
    }

    // Cookie auth: browser sends credentials automatically on <img> tags.
    if (authMethod !== "apikey") {
      setBlobUrl(url);
      return;
    }

    let revoke: string | undefined;

    fetch(url, { headers: { "X-API-Key": apiKey } })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.blob();
      })
      .then((blob) => {
        revoke = URL.createObjectURL(blob);
        setBlobUrl(revoke);
      })
      .catch(() => {
        setBlobUrl(undefined);
      });

    return () => {
      if (revoke) URL.revokeObjectURL(revoke);
    };
  }, [url, authMethod, apiKey]);

  return blobUrl;
}
