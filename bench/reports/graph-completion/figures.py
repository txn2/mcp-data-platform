#!/usr/bin/env python3
"""Generate the graph-completion report's figures from the committed run data.

Reads the same archives graph_tables.py reads and writes PNGs to figures/
(the toolchain copy) and docs/reference/benchmark-figures/graph-completion/
(the site copy). Offline, deterministic, no API key. Palette: the series'
validated categorical order (blue #2a78d6 = graph arm, orange #eb6834 =
stripped arm), validated with the dataviz six-checks script; direct value
labels sit on every mark.
"""
import os

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

import graph_tables as gt

GRAPH, STRIPPED = "#2a78d6", "#eb6834"
INK, MUTED = "#1a1a19", "#6b6a63"
_here = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(_here, "figures")
_repo = os.path.dirname(os.path.dirname(os.path.dirname(_here)))
SITE = os.path.join(_repo, "docs", "reference", "benchmark-figures", "graph-completion")

SCALES = (50, 500, 5000)


def _style(ax):
    ax.spines[["top", "right"]].set_visible(False)
    ax.spines[["left", "bottom"]].set_color(MUTED)
    ax.tick_params(colors=MUTED, labelsize=9)
    ax.yaxis.grid(True, color="#e6e5df", linewidth=0.8)
    ax.set_axisbelow(True)


def _save(fig, name):
    for d in (OUT, SITE):
        os.makedirs(d, exist_ok=True)
        fig.savefig(os.path.join(d, name), bbox_inches="tight", dpi=150)
    plt.close(fig)
    print(f"wrote {name}")


def _stats(agg):
    return {k: gt.row_stats(v) for k, v in agg.items()}


def fig1_cost(stats):
    """Searches per grounded constraint against corpus scale: the graph arm is
    flat across two orders of magnitude while the stripped arm roughly doubles."""
    fig, ax = plt.subplots(figsize=(6.4, 3.8))
    for arm, color, dy in (("graph", GRAPH, -0.09), ("stripped", STRIPPED, 0.07)):
        ys = [stats[(s, arm, "search")]["srch_grnd"] for s in SCALES]
        ax.plot(range(3), ys, color=color, linewidth=2, marker="o", markersize=8,
                label=f"{arm} arm")
        for x, y in zip(range(3), ys):
            ax.annotate(f"{y:.2f}", (x, y + dy), ha="center", fontsize=9, color=INK)
    ax.set_xticks(range(3), [str(s) for s in SCALES])
    ax.set_xlabel("corpus pages (log-spaced scales)", fontsize=9, color=MUTED)
    ax.set_ylabel("searches per grounded constraint", fontsize=9, color=MUTED)
    ax.set_ylim(0, 1.65)
    _style(ax)
    ax.legend(frameon=False, fontsize=9, loc="upper left")
    _save(fig, "fig1_search_cost_by_scale.png")


def fig2_provenance(stats):
    """Share of fetches whose reference was first seen on a fetched page:
    graph-arm agents use the edges more as the haystack grows; the stripped
    arm has no edges to use and stays at zero."""
    fig, ax = plt.subplots(figsize=(6.4, 3.8))
    width = 0.32
    for i, (arm, color) in enumerate((("graph", GRAPH), ("stripped", STRIPPED))):
        xs = [x + (i - 0.5) * width for x in range(3)]
        ys = [stats[(s, arm, "search")]["prov_page"] for s in SCALES]
        ax.bar(xs, ys, width=width, color=color, zorder=3, label=f"{arm} arm")
        for x, y in zip(xs, ys):
            ax.annotate(f"{y:.2f}", (x, y + 0.015), ha="center", fontsize=9, color=INK)
    ax.set_xticks(range(3), [str(s) for s in SCALES])
    ax.set_xlabel("corpus pages", fontsize=9, color=MUTED)
    ax.set_ylabel("page-provenance share of fetches", fontsize=9, color=MUTED)
    ax.set_ylim(0, 0.5)
    _style(ax)
    ax.legend(frameon=False, fontsize=9, loc="upper left")
    _save(fig, "fig2_page_provenance_by_scale.png")


def fig3_nosearch(stats, probe):
    """Grounded coverage with search unavailable: the stripped arm floors at
    zero at every reading budget, the graph arm is walked - and the walk is
    scale-invariant (the pilot's 42 pages vs the confirmatory 5000)."""
    fig, ax = plt.subplots(figsize=(6.4, 3.8))
    # Color follows the arm (graph blue, stripped orange); the pilot-vs-
    # confirmatory distinction is carried by the labels, not a repaint.
    bars = [
        ("stripped\n42 pages\n(pilot, both tiers)", probe[("stripped", "nosearch", "opus")]["off_grnd"], STRIPPED),
        ("graph\n42 pages\n(pilot, haiku)", probe[("graph", "nosearch", "haiku")]["off_grnd"], GRAPH),
        ("graph\n42 pages\n(pilot, opus)", probe[("graph", "nosearch", "opus")]["off_grnd"], GRAPH),
        ("graph\n5000 pages\n(confirmatory, opus)", stats[(5000, "graph", "nosearch")]["off_grnd"], GRAPH),
    ]
    for x, (label, y, color) in enumerate(bars):
        ax.bar(x, y, width=0.55, color=color, zorder=3)
        ax.annotate(f"{y:.2f}", (x, y + 0.02), ha="center", fontsize=10, color=INK)
    ax.set_xticks(range(len(bars)), [b[0] for b in bars], fontsize=8.5)
    ax.set_ylabel("off-entry grounded coverage, no search", fontsize=9, color=MUTED)
    ax.set_ylim(0, 1.12)
    _style(ax)
    _save(fig, "fig3_nosearch_robustness.png")


def main():
    agg = gt.aggregate(gt.load_confirmatory())
    stats = _stats(agg)
    probe = gt.t6_probe(gt.load_probe())
    fig1_cost(stats)
    fig2_provenance(stats)
    fig3_nosearch(stats, probe)


if __name__ == "__main__":
    main()
