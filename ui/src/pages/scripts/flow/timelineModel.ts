import type { FlowTimedCall, FuncSpan } from "@/api/portal/hooks/scriptFlow";

// The pure half of the Timeline view (#1972): a run's calls as a flame chart.
// Time runs left to right and is not aggregated, so order and waits stay
// visible. The top row is main(); each helper a call was made through is a row
// below it, and the call itself is the bottom bar of its stack. A call's stack
// is its call site: "line:col" of every call from main() down to the platform
// call, so the function each frame is in is the one whose lines hold it.

export interface TimelineBar {
  key: string;
  depth: number;
  start: number;
  end: number;
  label: string;
  /** call is the audited call a bottom bar is, and index its place in the
   * run's timeline; frames carry neither. */
  call?: FlowTimedCall;
  index?: number;
}

export interface Timeline {
  bars: TimelineBar[];
  depth: number;
  /** span is the length drawn: the run's, or its last call's end. */
  span: number;
  /** upstream is the time some call was in flight; the rest is the script's
   * own work between calls. */
  upstream: number;
}

// fnAt names the innermost function whose lines hold a line.
export function fnAt(functions: FuncSpan[], line: number): string {
  let best: FuncSpan | undefined;
  for (const f of functions) {
    if (line >= f.line && line <= f.end_line && (!best || f.line >= best.line)) best = f;
  }
  return best ? `${best.name}()` : `line ${line}`;
}

export function lineOf(position: string): number {
  return Number(position.split(":")[0]) || 0;
}

// buildTimeline lays a run's calls out as bars.
export function buildTimeline(calls: FlowTimedCall[], functions: FuncSpan[], runMS: number): Timeline {
  const bars: TimelineBar[] = [];
  const last = calls.reduce((m, c) => Math.max(m, c.start_ms + c.duration_ms), 0);
  const span = Math.max(runMS, last, 1);
  bars.push({ key: "main", depth: 0, start: 0, end: span, label: "main()" });
  // open is the frame bar being extended at each depth, keyed by the call
  // site prefix that entered it.
  const open = new Map<number, { key: string; bar: TimelineBar }>();
  let depth = 1;
  calls.forEach((c, i) => {
    const site = c.call_site ?? [];
    const end = c.start_ms + c.duration_ms;
    for (let d = 1; d < site.length; d++) {
      const key = site.slice(0, d).join(">");
      const cur = open.get(d);
      if (cur && cur.key === key) {
        cur.bar.end = Math.max(cur.bar.end, end);
        continue;
      }
      for (const k of [...open.keys()]) if (k >= d) open.delete(k);
      const bar: TimelineBar = {
        key: `f${i}.${d}`,
        depth: d,
        start: c.start_ms,
        end,
        label: fnAt(functions, lineOf(site[d]!)),
      };
      bars.push(bar);
      open.set(d, { key, bar });
    }
    for (const k of [...open.keys()]) if (k >= Math.max(1, site.length)) open.delete(k);
    const at = Math.max(1, site.length);
    depth = Math.max(depth, at);
    bars.push({ key: `c${i}`, depth: at, start: c.start_ms, end, label: c.tool, call: c, index: i });
  });
  return { bars, depth: depth + 1, span, upstream: busy(calls) };
}

// busy is the length of the union of the calls' intervals.
export function busy(calls: FlowTimedCall[]): number {
  const spans = calls
    .map((c) => [c.start_ms, c.start_ms + c.duration_ms] as const)
    .sort((a, b) => a[0] - b[0]);
  let total = 0;
  let curStart = -1;
  let curEnd = -1;
  for (const [s, e] of spans) {
    if (s > curEnd) {
      if (curEnd > curStart) total += curEnd - curStart;
      curStart = s;
      curEnd = e;
    } else {
      curEnd = Math.max(curEnd, e);
    }
  }
  if (curEnd > curStart) total += curEnd - curStart;
  return total;
}
