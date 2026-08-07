#!/usr/bin/env python3
"""Generate the knowledge-pollution report's figures from the committed run data.

Reads the same archives pollution_tables.py reads and writes PNGs to figures/
(the toolchain copy) and docs/reference/benchmark-figures/knowledge-pollution/
(the site copy). Offline, deterministic, no API key. Palette: the series'
validated categorical order (blue #2a78d6 = Sonnet 5, orange #eb6834 =
Haiku 4.5, aqua #1baf7a = Opus 5), validated with the dataviz six-checks
script; the aqua slot's contrast warning is relieved by direct value labels
on every mark.
"""
import os

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

import pollution_tables as pt

SONNET, HAIKU, OPUS = "#2a78d6", "#eb6834", "#1baf7a"
INK, MUTED = "#1a1a19", "#6b6a63"
_here = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(_here, "figures")
_repo = os.path.dirname(os.path.dirname(os.path.dirname(_here)))
SITE = os.path.join(_repo, "docs", "reference", "benchmark-figures", "knowledge-pollution")


def _style(ax, ymax=105):
    ax.set_ylim(0, ymax)
    ax.spines[["top", "right"]].set_visible(False)
    ax.spines[["left", "bottom"]].set_color(MUTED)
    ax.tick_params(colors=MUTED, labelsize=9)
    ax.yaxis.grid(True, color="#e6e5df", linewidth=0.8)
    ax.set_axisbelow(True)


def _save(fig, name):
    for d in (OUT, SITE):
        os.makedirs(d, exist_ok=True)
        fig.savefig(os.path.join(d, name), bbox_inches="tight")
    plt.close(fig)
    print(f"wrote {name}")


def _adoption(family, name, benchrun=True):
    t = pt.tally(pt.load_arm(family, name)[1], benchrun=benchrun)
    return t.get("adopted", 0), t["n"]


def _bar_with_ci(ax, x, k, n, color, width=0.25):
    rate = 100.0 * k / n
    lo, hi = pt.wilson(k, n)
    ax.bar(x, rate, width=width, color=color, zorder=3)
    # The Wilson whisker carries the series hue so a zero-height bar still
    # shows which tier it belongs to; the white halo keeps it readable where
    # it crosses a filled bar.
    ax.vlines(x, 100 * lo, 100 * hi, color="white", linewidth=3.2, zorder=4)
    ax.vlines(x, 100 * lo, 100 * hi, color=color, linewidth=1.6, zorder=5)
    ax.annotate(f"{k}/{n}", (x, max(rate, 100 * hi) + 3), ha="center",
                fontsize=8, color=INK)


def fig1():
    """RQ1 adoption of the planted wrong claim, by class and tier."""
    tiers = (("haiku", HAIKU, "Haiku 4.5"), ("sonnet", SONNET, "Sonnet 5"), ("opus", OPUS, "Opus 5"))
    fig, ax = plt.subplots(figsize=(6.4, 3.4), dpi=200)
    for gi, cls in enumerate(("convention", "checkable")):
        for ti, (tier, color, _) in enumerate(tiers):
            k, n = _adoption("rq1-warehouse", f"{cls}-wrong-{tier}")
            _bar_with_ci(ax, gi + (ti - 1) * 0.27, k, n, color)
    _style(ax)
    ax.set_xticks([0, 1])
    ax.set_xticklabels(["convention\n(fiscal boundary — nothing refutes it)",
                        "checkable\n(order count — one query settles it)"], color=INK)
    ax.set_ylabel("adoption of the wrong claim (%)", fontsize=9, color=MUTED)
    ax.legend(handles=[plt.Rectangle((0, 0), 1, 1, color=c) for _, c, _ in tiers],
              labels=[l for _, _, l in tiers], frameon=False, fontsize=8, loc="upper left")
    ax.set_title("The only claim adopted is the one a query settles, and only by the weak tier",
                 fontsize=10, color=INK, loc="left")
    _save(fig, "fig1_adoption_by_class_and_tier.png")


def fig2():
    """The mechanism: episodes split by whether the refuting count was observed."""
    arms = [
        ("rq1-warehouse", "checkable-wrong-haiku", "Haiku 4.5 — wrong claim planted"),
        ("rq1-warehouse", "checkable-absent-haiku", "Haiku 4.5 — nothing planted (control)"),
        ("rq1-warehouse", "checkable-wrong-sonnet", "Sonnet 5 — wrong claim planted"),
        ("rq1-warehouse", "checkable-wrong-opus", "Opus 5 — wrong claim planted"),
    ]
    # Outcome categories deliberately do not reuse the tier hues (fig 1 and
    # fig 3): the settled state is neutral gray, and the phenomenon — episodes
    # that skipped the observation and adopted — carries the accent.
    OBSERVED, ADOPTED = MUTED, HAIKU
    fig, ax = plt.subplots(figsize=(6.4, 2.8), dpi=200)
    ys = range(len(arms))
    for y, (family, name, label) in zip(ys, arms):
        _, attempts, dirpath = pt.load_arm(family, name)
        n = len(attempts)
        _, observed = pt.count_mechanism(dirpath)
        ax.barh(y, observed, color=OBSERVED, zorder=3, height=0.55)
        ax.barh(y, n - observed, left=observed, color=ADOPTED, zorder=3, height=0.55,
                edgecolor="white", linewidth=2)
        ax.annotate(f"{observed}", (max(observed - 0.4, 0.6), y), va="center", ha="right",
                    fontsize=8, color="white" if observed else INK)
        if n - observed:
            ax.annotate(f"{n - observed}", (n - 0.4, y), va="center", ha="right",
                        fontsize=8, color="white")
    ax.set_yticks(list(ys))
    ax.set_yticklabels([label for _, _, label in arms], fontsize=8.5, color=INK)
    ax.invert_yaxis()
    ax.set_xlim(0, 24)
    ax.set_xlabel("episodes (of 24)", fontsize=9, color=MUTED)
    ax.spines[["top", "right"]].set_visible(False)
    ax.spines[["left", "bottom"]].set_color(MUTED)
    ax.tick_params(colors=MUTED, labelsize=9)
    ax.legend(handles=[plt.Rectangle((0, 0), 1, 1, color=OBSERVED),
                       plt.Rectangle((0, 0), 1, 1, color=ADOPTED)],
              labels=["observed the count — every one answered correctly",
                      "did not observe it — every one adopted the claim"],
              frameon=False, fontsize=8, loc="lower right", bbox_to_anchor=(1.0, 1.0), ncols=1)
    ax.set_title("Verification displacement: the outcome is fully determined by one action",
                 fontsize=10, color=INK, loc="left", pad=30)
    _save(fig, "fig2_verification_displacement.png")


def fig3():
    """Adoption across the pre-registered robustness conditions, weak vs strong tier."""
    conditions = [
        ("catalog-entity sink (RQ1)",
         _adoption("rq1-warehouse", "checkable-wrong-haiku"),
         _adoption("rq1-warehouse", "checkable-wrong-sonnet")),
        ("bare statement, no directive",
         _adoption("directive-contrast", "checkable-wrong-haiku-bare"), None),
        ("knowledge-page sink",
         _adoption("generalization", "checkable-wrong-haiku-page"), None),
        ("second fixture (API monitor count)",
         _adoption("generalization", "api-checkable-wrong-haiku", benchrun=False),
         _adoption("generalization", "api-checkable-wrong-sonnet", benchrun=False)),
        ("raw model API, no agent client",
         _adoption("metered-replication", "checkable-wrong-haiku-api"),
         _adoption("metered-replication", "checkable-wrong-sonnet-api")),
    ]
    fig, ax = plt.subplots(figsize=(6.4, 3.2), dpi=200)
    ys = range(len(conditions))
    for y, (label, haiku, sonnet) in zip(ys, conditions):
        for series, color, dy in ((haiku, HAIKU, -0.08), (sonnet, SONNET, 0.18)):
            if series is None:
                continue
            k, n = series
            rate = 100.0 * k / n
            lo, hi = pt.wilson(k, n)
            ax.hlines(y + dy, 100 * lo, 100 * hi, color=color, linewidth=1.4, alpha=0.55, zorder=3)
            ax.plot(rate, y + dy, "o", color=color, markersize=7, zorder=4)
            up = color == HAIKU
            ax.annotate(f"{k}/{n}", (rate, y + dy), xytext=(10, 6 if up else -7),
                        textcoords="offset points", ha="left",
                        va="bottom" if up else "top", fontsize=7.5, color=INK,
                        annotation_clip=False)
    ax.set_yticks(list(ys))
    ax.set_yticklabels([c[0] for c in conditions], fontsize=8.5, color=INK)
    ax.invert_yaxis()
    ax.set_xlim(-3, 103)
    ax.set_xlabel("adoption of the wrong checkable claim (%), Wilson 95% interval", fontsize=9, color=MUTED)
    ax.spines[["top", "right"]].set_visible(False)
    ax.spines[["left", "bottom"]].set_color(MUTED)
    ax.tick_params(colors=MUTED, labelsize=9)
    ax.xaxis.grid(True, color="#e6e5df", linewidth=0.8)
    ax.set_axisbelow(True)
    ax.legend(handles=[plt.Line2D([], [], marker="o", linestyle="", color=HAIKU),
                       plt.Line2D([], [], marker="o", linestyle="", color=SONNET)],
              labels=["Haiku 4.5", "Sonnet 5"], frameon=False, fontsize=8,
              loc="center left", bbox_to_anchor=(0.06, 0.52))
    ax.set_title("The weak-tier effect survives every pre-registered variation; the strong tier never adopts",
                 fontsize=10, color=INK, loc="left")
    _save(fig, "fig3_robustness_sweep.png")


def main():
    fig1()
    fig2()
    fig3()


if __name__ == "__main__":
    main()
