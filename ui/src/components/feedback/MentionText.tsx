import { useDirectoryNames } from "@/api/portal/hooks";
import { splitBody } from "@/lib/mentions";

interface Props {
  body: string;
  /**
   * The addresses the server recorded as delivered mentions for this event
   * (`metadata.mentions`). Only these render as chips: a token naming someone
   * outside the item's audience is never recorded and notifies nobody, so
   * chipping it would tell every reader that person was tagged and is waiting
   * to reply. Undefined means nothing was recorded, which chips nothing.
   */
  mentions?: string[];
  className?: string;
}

/**
 * MentionText renders a comment body with its delivered @-mentions as chips.
 *
 * A mention is stored as @local(domain); the chip shows the person's name,
 * looked up per address, so a comment reads as "@Marcus Johnson" without the
 * body having to store a name that can go stale. Everything else renders as
 * plain text, preserving the author's line breaks.
 */
export function MentionText({ body, mentions, className }: Props) {
  const segments = splitBody(body);
  const delivered = new Set((mentions ?? []).map((m) => m.toLowerCase()));
  const names = useDirectoryNames(
    segments.flatMap((s) => (s.kind === "mention" && delivered.has(s.email) ? [s.email] : [])),
  );

  return (
    <p className={className}>
      {segments.map((segment, i) =>
        segment.kind === "mention" && delivered.has(segment.email) ? (
          <span
            key={i}
            title={segment.email}
            className="rounded bg-primary/10 px-1 font-medium text-primary"
          >
            @{names[segment.email] || segment.email}
          </span>
        ) : (
          <span key={i}>{segment.text}</span>
        ),
      )}
    </p>
  );
}
