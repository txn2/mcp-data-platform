import { useAuthSrc } from "@/hooks/useAuthSrc";

type Props = Omit<React.ImgHTMLAttributes<HTMLImageElement>, "src"> & {
  src: string | undefined;
};

/**
 * An <img> that fetches authenticated URLs with the API key header.
 * In cookie auth mode, behaves like a normal <img>.
 */
export function AuthImg({ src, ...props }: Props) {
  const resolvedSrc = useAuthSrc(src);
  if (!resolvedSrc) return null;
  // Default to lazy/async so off-screen grid thumbnails don't all fetch and
  // decode on mount (a full grid otherwise loads every thumbnail at once).
  // Defaults come before the spread so callers can still override them.
  return <img loading="lazy" decoding="async" src={resolvedSrc} {...props} />;
}
