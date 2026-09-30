import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { CalendarClock } from "lucide-react";
import {
  useScheduleTimeline,
  viewerTimezone,
  type ScheduleFireRow,
  type ScheduleFireWindow,
  type ScheduleRhythm,
} from "@/api/portal/hooks/scheduleTimeline";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { scheduleLine } from "./cadence";
import {
  NO_FACETS,
  ScheduleFilterBar,
  matchesFacets,
  type ScheduleFacets,
} from "./ScheduleFilters";
import {
  AXIS_HEIGHT,
  DOT_RADIUS,
  LABEL_WIDTH,
  MIN_PLOT_WIDTH,
  PAUSED_COLOR,
  RHYTHM_LABEL,
  RHYTHM_ORDER,
  ROW_HEIGHT,
  SECTION_HINT,
  SECTION_TITLE,
  TICK_HEIGHT,
  axisTicks,
  countPhrase,
  fireText,
  fitLabel,
  rhythmColor,
  rowDescription,
  rowWords,
  tickWidth,
  truncatedBand,
  usesDots,
  xAt,
} from "./timelineLayout";

// ScheduleTimelineTab answers the question the listing cannot (#1891): when do
// the scheduled scripts fire in relation to each other. Ten hourly jobs on the
// same minute, or what runs overnight, used to take opening each script and
// reading its cron line.
//
// Schedules fire at rates too different for one axis, so each is drawn on the
// axis the server files it under: today for the ones that fire more than once
// a day, this week for daily-to-weekly, three months for the rest. Every fire
// is the server's, expanded with the scheduler's own parse; this page places
// them and computes none.

interface Props {
  basePath: string;
  onNavigate: (path: string) => void;
  /** onShowScripts moves the page to its Automations tab, which is where a
   * schedule is set. */
  onShowScripts: () => void;
}

export function ScheduleTimelineTab({
  basePath,
  onNavigate,
  onShowScripts,
}: Props) {
  const { data, isLoading, error } = useScheduleTimeline();
  const [facets, setFacets] = useState<ScheduleFacets>(NO_FACETS);
  if (isLoading) {
    return (
      <p className="text-sm text-muted-foreground">Loading schedules...</p>
    );
  }
  if (error || !data) {
    return (
      <p className="text-sm text-muted-foreground">
        The schedules could not be loaded.
      </p>
    );
  }
  // A section is drawn when it has a schedule at all; the filters then narrow
  // its rows, and a section they empty says so rather than disappearing.
  const drawn = data.sections.filter((s) => s.rows.length > 0);
  if (drawn.length === 0 && data.unreadable.length === 0) {
    return (
      <EmptyState
        icon={CalendarClock}
        action={
          <Button variant="outline" size="sm" onClick={onShowScripts}>
            Go to Automations
          </Button>
        }
      >
        No automation runs on a schedule yet. A schedule is set on an
        automation's own page, and every scheduled automation is drawn here at
        the times it fires.
      </EmptyState>
    );
  }
  const shown = drawn.map((section) => ({
    ...section,
    rows: section.rows.filter((row) => matchesFacets(row, facets)),
  }));
  return (
    <div className="space-y-4">
      {drawn.length > 0 && (
        <ScheduleFilterBar
          rows={drawn.flatMap((s) => s.rows)}
          facets={facets}
          onFacets={setFacets}
        />
      )}
      <Legend timezone={data.timezone} rows={shown.flatMap((s) => s.rows)} />
      {shown.map((section) => (
        <SectionCard
          key={section.section}
          title={SECTION_TITLE[section.section]}
          action={
            <span className="text-xs text-muted-foreground">
              {SECTION_HINT[section.section]}
            </span>
          }
        >
          {section.rows.length === 0 ? (
            <p
              className="text-sm text-muted-foreground"
              data-testid={`schedule-section-unmatched-${section.section}`}
            >
              No schedule in this section matches the filters.
            </p>
          ) : (
            <TimelinePlot
              section={section}
              viewerZone={data.timezone || viewerTimezone()}
              onOpen={(id) => onNavigate(`${basePath}/${id}`)}
            />
          )}
        </SectionCard>
      ))}
      {data.unreadable.length > 0 && (
        <Alert>
          <AlertDescription>
            {data.unreadable.length === 1
              ? "One schedule could not be read and is not drawn: "
              : `${data.unreadable.length} schedules could not be read and are not drawn: `}
            {data.unreadable
              .map((u) => `${u.script_name || u.script_id} (${u.reason})`)
              .join("; ")}
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}

// Legend names the colors this page uses: the rhythms present, and paused
// when any row is. Identity is never color alone, because every row states its
// schedule in words as well.
function Legend({ timezone, rows }: { timezone: string; rows: ScheduleFireRow[] }) {
  const present = new Set<ScheduleRhythm>(
    rows.filter((r) => r.enabled).map((r) => r.rhythm),
  );
  const paused = rows.some((r) => !r.enabled);
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
      <span>
        Times are your own ({timezone}). Hover or focus a row for the
        exact time of a fire.
      </span>
      {RHYTHM_ORDER.filter((r) => present.has(r)).map((r) => (
        <span key={r} className="inline-flex items-center gap-1.5">
          <span
            aria-hidden
            className="inline-block h-3 w-1 rounded-sm"
            style={{ background: rhythmColor(r) }}
          />
          {RHYTHM_LABEL[r]}
        </span>
      ))}
      {paused && (
        <span className="inline-flex items-center gap-1.5">
          <span
            aria-hidden
            className="inline-block h-3 w-1 rounded-sm opacity-50"
            style={{ background: PAUSED_COLOR }}
          />
          Paused
        </span>
      )}
    </div>
  );
}

/** Active is the fire a tooltip is showing: which row, and which of its fires. */
interface Active {
  row: number;
  fire: number;
}

// useWidth tracks the width the plot is laid out in. jsdom has no
// ResizeObserver, and a first paint before the observer reports needs a size,
// so it starts from a desktop width.
function useWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(960);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w) setWidth(w);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return { ref, width };
}

function TimelinePlot({
  section,
  viewerZone,
  onOpen,
}: {
  section: ScheduleFireWindow;
  viewerZone: string;
  onOpen: (scriptID: string) => void;
}) {
  const { ref, width } = useWidth();
  const clipID = `schedule-wipe-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const [active, setActive] = useState<Active | null>(null);
  const from = Date.parse(section.from);
  const to = Date.parse(section.to);
  const plotWidth = Math.max(MIN_PLOT_WIDTH, width - LABEL_WIDTH - 16);
  const totalWidth = LABEL_WIDTH + plotWidth + 16;
  const plotHeight = section.rows.length * ROW_HEIGHT;
  const x = (at: number) => xAt(at, from, to, plotWidth);
  const fires = section.rows.map((r) => r.fires.map((f) => Date.parse(f)));
  const ticks = axisTicks(
    section.section,
    new Date(from),
    new Date(to),
    viewerZone,
  );

  // nearestFire picks the fire under the pointer, so a whole row is the hit
  // target rather than a 2px tick.
  const nearestFire = (
    row: number,
    clientX: number,
    svg: SVGSVGElement,
  ): number | null => {
    const left = svg.getBoundingClientRect().left;
    const px = clientX - left;
    let best: number | null = null;
    let bestDistance = 12;
    fires[row]!.forEach((f, i) => {
      const d = Math.abs(x(f) - px);
      if (d <= bestDistance) {
        best = i;
        bestDistance = d;
      }
    });
    return best;
  };

  const svgRef = useRef<SVGSVGElement>(null);
  const tooltip = active
    ? tooltipFor(section.rows[active.row]!, fires[active.row]![active.fire])
    : null;

  return (
    <div ref={ref} className="relative overflow-x-auto">
      <svg
        ref={svgRef}
        width={totalWidth}
        height={plotHeight + AXIS_HEIGHT}
        role="group"
        aria-label={`${SECTION_TITLE[section.section]} schedules`}
        className="block"
      >
        <defs>
          <clipPath id={clipID}>
            <rect
              className="schedule-wipe"
              x={LABEL_WIDTH - DOT_RADIUS - 4}
              y={0}
              width={plotWidth + 2 * (DOT_RADIUS + 4)}
              height={plotHeight}
            />
          </clipPath>
        </defs>
        {section.rows.map((_, i) =>
          i % 2 === 0 ? (
            <rect
              key={`band-${i}`}
              x={0}
              y={i * ROW_HEIGHT}
              width={totalWidth}
              height={ROW_HEIGHT}
              fill="hsl(var(--muted))"
              opacity={0.55}
            />
          ) : null,
        )}
        {ticks.map((t, i) => (
          <line
            key={`grid-${i}`}
            x1={x(t.at)}
            x2={x(t.at)}
            y1={0}
            y2={plotHeight}
            stroke="hsl(var(--border))"
            strokeWidth={1}
            opacity={t.major ? 1 : 0.5}
          />
        ))}
        {ticks
          .filter((t) => t.label)
          .map((t, i, labelled) => (
            <text
              key={`axis-${i}`}
              x={x(t.at)}
              y={plotHeight + 16}
              fontSize={11}
              fill="hsl(var(--muted-foreground))"
              textAnchor={
                i === 0
                  ? "start"
                  : i === labelled.length - 1 && t.at >= to
                    ? "end"
                    : "middle"
              }
            >
              {t.label}
            </text>
          ))}
        <g>
          {section.rows.map((row, i) => (
            <TimelineRow
              key={row.script_id}
              row={row}
              index={i}
              section={section}
              fires={fires[i]!}
              x={x}
              clipID={clipID}
              viewerZone={viewerZone}
              active={active?.row === i ? active.fire : null}
              onHover={(clientX, svg) => {
                const fire = nearestFire(i, clientX, svg);
                setActive(fire === null ? null : { row: i, fire });
              }}
              onStep={(fire) =>
                setActive(fire === null ? null : { row: i, fire })
              }
              onOpen={() => onOpen(row.script_id)}
            />
          ))}
        </g>
      </svg>
      {/* In a portal at viewport coordinates: the plot scrolls sideways, and a
          scroll container clips vertically too, which cut the fire times off
          a tooltip on the last row. */}
      {active &&
        tooltip &&
        svgRef.current &&
        createPortal(
          <div
            role="tooltip"
            className="pointer-events-none fixed z-50 rounded-md border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md"
            style={tooltipPosition(
              svgRef.current.getBoundingClientRect(),
              x(tooltip.at),
              active.row * ROW_HEIGHT + ROW_HEIGHT,
            )}
          >
            <div className="font-medium">{tooltip.name}</div>
            {fireText(tooltip.at, viewerZone, tooltip.zone).map((line) => (
              <div key={line}>{line}</div>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}

// TOOLTIP_WIDTH is the room a tooltip is given before it flips to the left of
// its mark rather than run off the viewport.
const TOOLTIP_WIDTH = 280;

// tooltipPosition places a tooltip just right of and below a mark, in viewport
// coordinates, flipping left when the mark is near the viewport's right edge.
function tooltipPosition(
  svg: DOMRect,
  markX: number,
  belowY: number,
): React.CSSProperties {
  const left = svg.left + markX + 8;
  const flip = left + TOOLTIP_WIDTH > window.innerWidth;
  return {
    left: flip ? Math.max(8, svg.left + markX - 8 - TOOLTIP_WIDTH) : left,
    top: svg.top + belowY,
  };
}

function tooltipFor(row: ScheduleFireRow, at: number | undefined) {
  if (at === undefined) return null;
  return { name: row.script_name || row.script_id, at, zone: row.timezone };
}

// TimelineRow is one schedule: its name and its schedule in words in the
// label column, and a mark at each fire. The row is one control: a click or
// Enter opens the script, and the arrow keys step the tooltip through its
// fires.
function TimelineRow({
  row,
  index,
  section,
  fires,
  x,
  clipID,
  viewerZone,
  active,
  onHover,
  onStep,
  onOpen,
}: {
  row: ScheduleFireRow;
  index: number;
  section: ScheduleFireWindow;
  fires: number[];
  x: (at: number) => number;
  clipID: string;
  viewerZone: string;
  active: number | null;
  onHover: (clientX: number, svg: SVGSVGElement) => void;
  onStep: (fire: number | null) => void;
  onOpen: () => void;
}) {
  const y = index * ROW_HEIGHT;
  const cy = y + ROW_HEIGHT / 2;
  const name = row.script_name || row.script_id;
  const words = rowWords(
    scheduleLine(row.cron_spec, row.timezone),
    row.timezone,
    viewerZone,
  );
  const desc = `${words} · ${countPhrase(section.section, row.fire_count)}${row.enabled ? "" : " · paused"}`;
  const shownDesc = rowDescription(
    words,
    countPhrase(section.section, row.fire_count),
    !row.enabled,
    52,
  );
  const color = row.enabled ? rhythmColor(row.rhythm) : PAUSED_COLOR;
  const dots = usesDots(fires);
  const band = truncatedBand(row, fires, x);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onOpen();
    } else if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
      e.preventDefault();
      if (fires.length === 0) return;
      const step = e.key === "ArrowRight" ? 1 : -1;
      const next =
        active === null
          ? 0
          : Math.min(fires.length - 1, Math.max(0, active + step));
      onStep(next);
    } else if (e.key === "Escape") {
      onStep(null);
    }
  };

  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={`${name}, ${desc}. Opens the script.`}
      className="group cursor-pointer outline-none focus-visible:[&>rect.row-hit]:stroke-ring"
      onClick={onOpen}
      onKeyDown={onKeyDown}
      onFocus={() => onStep(fires.length > 0 ? 0 : null)}
      onBlur={() => onStep(null)}
      onMouseMove={(e) => {
        const svg = (e.currentTarget as SVGGElement).ownerSVGElement;
        if (svg) onHover(e.clientX, svg);
      }}
      onMouseLeave={() => onStep(null)}
      data-testid={`schedule-row-${row.script_id}`}
    >
      <title>{`${name}\n${desc}`}</title>
      {/* The whole row is the hit target, and carries the focus ring. */}
      <rect
        className="row-hit"
        x={1}
        y={y + 1}
        width={x(Date.parse(section.to)) - 2}
        height={ROW_HEIGHT - 2}
        rx={4}
        fill="transparent"
        strokeWidth={2}
      />
      {/* The name reads as the link it is: every row opens its script. */}
      <text
        x={8}
        y={cy - 3}
        fontSize={13}
        fontWeight={500}
        fill="hsl(var(--primary))"
        className="group-hover:underline group-focus-visible:underline"
        data-testid="schedule-row-name"
      >
        {fitLabel(name, 36)}
      </text>
      <text x={8} y={cy + 12} fontSize={11} fill="hsl(var(--muted-foreground))">
        {shownDesc}
      </text>
      <g
        opacity={row.enabled ? 1 : 0.5}
        clipPath={`url(#${clipID})`}
        data-testid="schedule-marks"
      >
        {band && (
          <rect
            x={band.from}
            y={cy - TICK_HEIGHT / 2}
            width={Math.max(band.to - band.from, 1)}
            height={TICK_HEIGHT}
            rx={2}
            fill={color}
            data-testid="schedule-band"
          />
        )}
        {!band &&
          fires.map((f, i) =>
            dots ? (
              <circle
                key={i}
                cx={x(f)}
                cy={cy}
                r={DOT_RADIUS}
                fill={color}
                stroke="hsl(var(--card))"
                strokeWidth={2}
              />
            ) : (
              <line
                key={i}
                x1={x(f)}
                x2={x(f)}
                y1={cy - TICK_HEIGHT / 2}
                y2={cy + TICK_HEIGHT / 2}
                stroke={color}
                strokeWidth={tickWidth(row.fire_count)}
                strokeLinecap="round"
              />
            ),
          )}
        {active !== null && fires[active] !== undefined && (
          <circle
            cx={x(fires[active])}
            cy={cy}
            r={DOT_RADIUS + 3}
            fill="none"
            stroke="hsl(var(--foreground))"
            strokeWidth={1.5}
          />
        )}
      </g>
    </g>
  );
}
