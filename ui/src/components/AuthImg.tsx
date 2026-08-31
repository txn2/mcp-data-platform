import { useEffect } from "react";
import { useAuthSrc } from "@/hooks/useAuthSrc";

type Props = Omit<React.ImgHTMLAttributes<HTMLImageElement>, "src"> & {
  src: string | undefined;
  /**
   * Reported when the source could not be resolved at all.
   *
   * On a cookie session that is the element's own `onError`; on an API-key
   * session the bytes are fetched ahead of the element, so a refusal produces
   * no element and therefore no error event, and a caller with a fallback to
   * draw would wait on one forever (#1568). This fires in both cases, so a
   * caller can treat "cannot draw this" as one condition.
   */
  onLoadFailed?: () => void;
};

/**
 * An <img> that fetches authenticated URLs with the API key header.
 * In cookie auth mode, behaves like a normal <img>.
 */
export function AuthImg({ src, onLoadFailed, ...props }: Props) {
  const { src: resolvedSrc, failed } = useAuthSrc(src);

  useEffect(() => {
    if (failed) onLoadFailed?.();
  }, [failed, onLoadFailed]);

  if (!resolvedSrc) return null;
  // Default to lazy/async so off-screen grid thumbnails don't all fetch and
  // decode on mount (a full grid otherwise loads every thumbnail at once).
  // Defaults come before the spread so callers can still override them.
  return <img loading="lazy" decoding="async" src={resolvedSrc} {...props} />;
}
