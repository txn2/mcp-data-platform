import type { LucideIcon } from "lucide-react";
import { StatCard } from "@/components/cards/StatCard";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Pager } from "@/components/patterns/Pager";
import { SectionCard } from "@/components/patterns/SectionCard";
import type { SessionDetail } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { formatUser } from "@/lib/formatUser";
import { kindDescription, kindLabel } from "./kind";
import { SessionAssets, SessionInsights } from "./SessionOutputs";
import { SessionTimeline } from "./SessionTimeline";

// SessionDetailView is one session as it reads once it has been loaded: how
// much it did, what it left behind, and the ordered record of its calls.
//
// It is the body both session surfaces render. An operator reading someone
// else's session and a user reading their own are looking at the same thing,
// and the differences between them are passed in: where an asset opens, and
// whether a timeline row leads anywhere (only the admin has an event drawer to
// lead to). Forking this into two components is how the two views would start
// answering the same question differently.

/** Timeline entries per page. Exported so a page's Pager agrees with it. */
export const TIMELINE_PER_PAGE = 25;

export function SessionDetailView({
  session,
  page,
  onPage,
  onSelectEvent,
  onNavigate,
  assetPath,
  titleMap,
}: {
  session: SessionDetail;
  page: number;
  onPage: (page: number) => void;
  /** Opens one call in full. Omitted where the reader has no such surface. */
  onSelectEvent?: (eventId: string) => void;
  onNavigate: (path: string) => void;
  assetPath: (assetId: string) => string;
  titleMap: Record<string, string>;
}) {
  return (
    <>
      <SessionStats session={session} />

      <div className="grid gap-3 lg:grid-cols-2">
        <SessionAssets
          assets={session.assets}
          onNavigate={onNavigate}
          assetPath={assetPath}
        />
        <SessionInsights insights={session.insights} />
      </div>

      <SectionCard title={`Timeline (${session.timeline_total})`}>
        <SessionTimeline
          entries={session.timeline}
          isLoading={false}
          onSelect={onSelectEvent}
          titleMap={titleMap}
        />
        {session.timeline_total > TIMELINE_PER_PAGE && (
          <div className="pt-3">
            <Pager
              page={page}
              perPage={TIMELINE_PER_PAGE}
              total={session.timeline_total}
              onPage={onPage}
            />
          </div>
        )}
      </SectionCard>
    </>
  );
}

/**
 * SessionDetailHeader names the session being read. `showUser` carries the
 * caller into the identity line for an operator; a reader's own session drops
 * it, the way the list drops its User column.
 */
export function SessionDetailHeader({
  session,
  sessionId,
  backLabel,
  onBack,
  icon,
  showUser = true,
}: {
  session?: SessionDetail;
  sessionId: string;
  backLabel: string;
  onBack: () => void;
  icon: LucideIcon;
  showUser?: boolean;
}) {
  return (
    <PageHeader
      backLabel={backLabel}
      onBack={onBack}
      icon={icon}
      title={session ? kindLabel(session.kind) : "Session"}
      urn={sessionId}
      subtitle={session ? subtitleFor(session, showUser) : undefined}
    />
  );
}

/** The session at a glance: how much it did, and what it left behind. */
function SessionStats({ session }: { session: SessionDetail }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
      <StatCard label="Calls" value={session.call_count} />
      <StatCard
        label="Failed"
        value={session.failure_count}
        detail={
          session.failure_count > 0 ? "calls returned an error" : undefined
        }
      />
      <StatCard label="Duration" value={sessionDuration(session)} />
      <StatCard label="Assets" value={session.asset_count} />
      <StatCard label="Insights" value={session.insight_count} />
    </div>
  );
}

/** The identity line: who ran the session, as what, and when it started. */
function subtitleFor(session: SessionDetail, showUser: boolean): string {
  const parts = [
    showUser ? formatUser(session.user_id, session.user_email) : "",
    session.persona,
    kindDescription(session.kind),
    `started ${new Date(session.started_at).toLocaleString()}`,
  ];
  return parts.filter(Boolean).join(" · ");
}

/** Wall-clock span from the session's first call to its last. */
function sessionDuration(session: SessionDetail): string {
  const ms =
    new Date(session.last_active_at).getTime() -
    new Date(session.started_at).getTime();
  return formatDuration(Math.max(ms, 0));
}
