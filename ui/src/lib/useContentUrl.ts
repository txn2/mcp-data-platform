import { useEffect, useState } from "react";
import { useAuthStore } from "@/stores/auth";

/**
 * Resolves a content endpoint into a URL that an `<img>`, `<audio>`, `<video>`
 * or `<iframe>` can load.
 *
 * A cookie session carries its credential on any same-origin subresource
 * request, so the endpoint URL is handed to the element unchanged and the
 * browser's own byte-range requests reach the server, which is what makes
 * seeking work on long audio and video without downloading the whole file.
 *
 * An API-key session has no ambient credential: the key is a header this code
 * adds to `fetch` calls it makes itself, and an element loading a URL does not
 * go through that path. For those sessions the content is fetched once and the
 * element is pointed at an object URL instead. Seeking still works inside the
 * blob, but the whole object is downloaded first: the cost of a credential
 * that only exists in JavaScript.
 */
export function useContentUrl(url: string, enabled = true): { src: string; loading: boolean; error: string | null } {
  const authMethod = useAuthStore((s) => s.authMethod);
  const apiKey = useAuthStore((s) => s.apiKey);
  const needsFetch = enabled && authMethod === "apikey" && !!apiKey;

  const [blobUrl, setBlobUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!needsFetch || !url) {
      setBlobUrl(null);
      return;
    }

    let objectUrl: string | null = null;
    let cancelled = false;
    setLoading(true);
    setError(null);

    fetch(url, { headers: { "X-API-Key": apiKey as string }, credentials: "include" })
      .then((res) => {
        if (!res.ok) throw new Error(`Failed to load content (${res.status})`);
        return res.blob();
      })
      .then((blob) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setBlobUrl(objectUrl);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load content");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [url, needsFetch, apiKey]);

  if (!needsFetch) return { src: url, loading: false, error: null };
  return { src: blobUrl ?? "", loading, error };
}
