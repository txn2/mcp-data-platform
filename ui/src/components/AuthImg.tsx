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
  return <img src={resolvedSrc} {...props} />;
}
