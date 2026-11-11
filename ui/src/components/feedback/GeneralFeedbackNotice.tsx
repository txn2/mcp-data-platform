import { useGeneralFeedbackReach } from "@/api/portal/hooks";

// GeneralFeedbackNotice tells the author of a General post where it goes
// (#2603): visible to everyone, and sent to the people who handle feedback.
// When the administrator's list reaches nobody it says so instead, so an
// author is never told a post reached someone when it did not. It never names
// the recipients.
export function GeneralFeedbackNotice() {
  const { data } = useGeneralFeedbackReach();
  return (
    <p className="text-xs leading-relaxed text-muted-foreground" data-testid="general-feedback-notice">
      Posting to the General channel, visible to everyone on the platform.{" "}
      {data?.reaches_someone === false ? (
        <span className="font-semibold text-foreground">
          Nobody is told about new posts here yet. Name someone with @ so they see it.
        </span>
      ) : data?.reaches_someone ? (
        <span className="font-semibold text-foreground">It is sent to the people who handle feedback.</span>
      ) : null}
    </p>
  );
}
