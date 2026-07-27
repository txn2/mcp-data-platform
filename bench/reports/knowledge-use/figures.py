#!/usr/bin/env python3
"""Generate the report's figures from the committed run data.

Reads the same archives pk_tables.py reads and writes PNGs to figures/
(the toolchain copy) and docs/reference/benchmark-figures/knowledge-use/
(the site copy). Offline, deterministic, no API key. Palette: the
validated two-series categorical pair (blue #2a78d6 = Sonnet 5, orange
#eb6834 = Haiku 4.5), validated with the dataviz six-checks script.
"""
import os

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

import pk_tables as pk

SONNET, HAIKU = "#2a78d6", "#eb6834"
INK, MUTED = "#1a1a19", "#6b6a63"
_here = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(_here, "figures")
_repo = os.path.dirname(os.path.dirname(os.path.dirname(_here)))
SITE = os.path.join(_repo, "docs", "reference", "benchmark-figures", "knowledge-use")


def _style(ax, ymax=105):
    ax.set_ylim(0, ymax)
    ax.spines[["top", "right"]].set_visible(False)
    ax.spines[["left", "bottom"]].set_color(MUTED)
    ax.tick_params(colors=MUTED, labelsize=9)
    ax.yaxis.grid(True, color="#e6e5df", linewidth=0.8)
    ax.set_axisbelow(True)


def _rate(attempts, pred, field):
    sel = [a for a in attempts if not a.get("error") and pred(a)]
    if not sel:
        return None
    if field == "verified":
        k = sum(1 for a in sel if a["outcome"]["observation"]["verified"])
    elif field == "trusted":
        k = sum(1 for a in sel if a.get("trusted"))
    else:
        k = sum(1 for a in sel if a["outcome"].get("correct") is True)
    return 100.0 * k / len(sel)


def _tagged(pattern, model):
    for r in pk.load(pattern):
        if f"-{model}-v1116-" in r["_dir"]:
            return r["attempts"]
    raise SystemExit(f"no tagged {model} run matches {pattern}")


def fig1():
    """Verification of a delivered, checkable note vs what checking costs."""
    worlds = ["monitors-3", "monitors-3-scoped", "monitors-3-scoped-5", "monitors-3-scoped-10"]
    costs = [1, 3, 6, 11]
    fig, ax = plt.subplots(figsize=(6.4, 3.6), dpi=200)
    for model, color, label in ((f"sonnet", SONNET, "Sonnet 5"), ("haiku", HAIKU, "Haiku 4.5")):
        attempts = _tagged("pk-answersweep/*", model)
        ys = [_rate(attempts, lambda a, w=w: a.get("seed_id") and a["query_world"] == w, "verified") for w in worlds]
        ax.plot(costs, ys, color=color, linewidth=2, marker="o", markersize=6, label=label)
        ax.annotate(label, (costs[-1], ys[-1]), xytext=(8, 0), textcoords="offset points",
                    color=INK, fontsize=9, va="center")
    _style(ax)
    ax.set_xticks(costs)
    ax.set_xlabel("calls required to re-establish the state", color=MUTED, fontsize=9)
    ax.set_ylabel("verification rate (%)", color=MUTED, fontsize=9)
    ax.set_title("Verification of a delivered, checkable note is insensitive to its cost\n"
                 "on the strong tier, and near-zero on the weak one (answer sweep, k=8, v1.116.0)",
                 fontsize=10, color=INK, loc="left")
    ax.legend(frameon=False, fontsize=9, loc="center right")
    return fig, "fig1_verification_vs_cost.png"


def fig2():
    """Reliance by derivability and tier: the two-factor headline."""
    groups = ["Derivable note\n(checkable world state)", "Non-derivable note\n(reporting convention)"]
    vals = {}
    for model in ("sonnet", "haiku"):
        sweep = _tagged("pk-answersweep/*", model)
        trust = _rate(sweep, lambda a: a.get("seed_id"), "trusted")
        bridge_runs = [r for r in pk.load("pk-bridge/*")
                       if (f"-{model}-v1116-" in r["_dir"]) or (model == "haiku" and "haiku" in r["_dir"])]
        battempts = bridge_runs[0]["attempts"]
        used = _rate(battempts, lambda a: a.get("seed_id"), "correct")
        vals[model] = [trust, used]
    fig, ax = plt.subplots(figsize=(6.4, 3.6), dpi=200)
    x = [0, 1]
    w = 0.32
    for i, (model, color, label) in enumerate(((f"sonnet", SONNET, "Sonnet 5"), ("haiku", HAIKU, "Haiku 4.5"))):
        xs = [xi + (i - 0.5) * (w + 0.02) for xi in x]
        bars = ax.bar(xs, vals[model], width=w, color=color, label=label)
        for b, v in zip(bars, vals[model]):
            ax.annotate(f"{v:.0f}%", (b.get_x() + b.get_width() / 2, v), xytext=(0, 4),
                        textcoords="offset points", ha="center", color=INK, fontsize=9)
    _style(ax, ymax=112)
    ax.set_xticks(x, groups, fontsize=9, color=INK)
    ax.set_ylabel("reliance on the delivered note (%)", color=MUTED, fontsize=9)
    ax.set_title("Reliance on stored knowledge is governed by derivability on the strong tier\n"
                 "and inverted by capability on the weak one (k=8 per cell)",
                 fontsize=10, color=INK, loc="left")
    ax.legend(frameon=False, fontsize=9)
    return fig, "fig2_reliance_by_derivability.png"


def fig3():
    """The stale-note cost: accuracy with and without the stale note, by tier."""
    conds = ["stale note delivered", "no note"]
    fig, ax = plt.subplots(figsize=(6.4, 3.6), dpi=200)
    x = [0, 1]
    w = 0.32
    for i, (model, color, label) in enumerate(((f"sonnet", SONNET, "Sonnet 5"), ("haiku", HAIKU, "Haiku 4.5"))):
        attempts = _tagged("pk-staleanswer/*", model)
        ys = [_rate(attempts, lambda a: bool(a.get("seed_id")), "correct"),
              _rate(attempts, lambda a: not a.get("seed_id"), "correct")]
        xs = [xi + (i - 0.5) * (w + 0.02) for xi in x]
        bars = ax.bar(xs, ys, width=w, color=color, label=label)
        for b, v in zip(bars, ys):
            ax.annotate(f"{v:.0f}%", (b.get_x() + b.get_width() / 2, v), xytext=(0, 4),
                        textcoords="offset points", ha="center", color=INK, fontsize=9)
    _style(ax, ymax=112)
    ax.set_xticks(x, conds, fontsize=9, color=INK)
    ax.set_ylabel("accuracy (%)", color=MUTED, fontsize=9)
    ax.set_title("A stale answer-bearing note is strictly worse than no note on the weak tier\n"
                 "(stale-answer cell, k=8, v1.116.0)",
                 fontsize=10, color=INK, loc="left")
    # The groups sit at x=0 and x=1, so upper center is guaranteed
    # whitespace; a frameless legend anywhere else can land on a bar and
    # camouflage its own swatch.
    ax.legend(frameon=False, fontsize=9, loc="upper center")
    return fig, "fig3_stale_note_cost.png"


def main():
    os.makedirs(OUT, exist_ok=True)
    os.makedirs(SITE, exist_ok=True)
    for build in (fig1, fig2, fig3):
        fig, name = build()
        fig.tight_layout()
        for d in (OUT, SITE):
            fig.savefig(os.path.join(d, name), facecolor="white")
        plt.close(fig)
        print("wrote", name)


if __name__ == "__main__":
    main()
