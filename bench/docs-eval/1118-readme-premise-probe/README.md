# README premise probe (#1118)

What does a model conclude about this project after reading `README.md` and
nothing else?

[Issue #1118](https://github.com/txn2/mcp-data-platform/issues/1118) proposed
adding prose to the README to answer two beliefs an AI evaluator was assumed to
arrive with. The issue made that assumption testable and required it be tested
first: if the beliefs did not actually appear, the issue was to be closed
unwritten. This directory is that test.

## Design

One evaluation is one `claude -p --bare` invocation carrying the full text of
`README.md` and the instruction *"Give me a critical assessment of this
project."* `--bare` is what makes it a fair proxy for an outside reader: it
skips `CLAUDE.md` discovery, hooks, and auto-memory, so the model sees the
README and no other repository context. No tools, no network, no repository
access. Every run is a fresh context.

Five runs on each of three models (`opus`, `sonnet`, `haiku`), n=15 per result
directory. `baseline/` holds a sixteenth transcript, `sonnet-run0`, from the
harness smoke test; it is kept because it is real data, and excluded from every
rate below.

Each transcript is then classified against four propositions by a separate
`opus` pass ([`rubric.txt`](rubric.txt)), which returns, for each proposition, a
fired/not-fired verdict and a **verbatim quote** from the transcript whenever it
says fired. The quote requirement is what makes the judge auditable: every fire
reported here was read back against its source before being counted.

Two propositions are variables (the beliefs #1118 targets) and two are
controls, which the README already answers and which were predicted to come
back clean.

| | Proposition | Role |
| --- | --- | --- |
| M1 | Release cadence, team size, or solo authorship means security-critical code is unreviewed | variable |
| M2 | The project rolled its own OAuth rather than delegating to an identity provider | variable |
| M3 | Nobody has validated that the platform changes agent behavior | control |
| M4 | DataHub is a hard dependency and a lock-in cost | control |

The rubric draws the line at assertion, not mention. Neutrally observing that
an OAuth server exists is not M2. Criticizing the benchmark as self-produced or
single-model is not M3, because a reader doing that has plainly read the
evidence the page presents; M3 requires claiming evidence is *absent*. Noting
that cross-enrichment specifically needs a semantic layer is not M4; M4
requires presenting DataHub as required for the project as a whole.

## Baseline result

Against `README.md` at `9c3fad3b`, before any change from #1118; the
[after-the-change](#after-the-change) run is at the end. Raw runs in
[`baseline/`](baseline/), per-transcript verdicts in
[`baseline/counts.csv`](baseline/counts.csv), provenance in
[`baseline/RUN_META.txt`](baseline/RUN_META.txt).

| | Proposition | opus | sonnet | haiku | total |
| --- | --- | --- | --- | --- | --- |
| M1 | velocity outruns scrutiny | 1/5 | 3/5 | 0/5 | **4/15 (27%)** |
| M2 | rolled their own OAuth | 5/5 | 3/5 | 1/5 | **9/15 (60%)** |
| M3 | *control:* nobody validated it | 0/5 | 0/5 | 0/5 | **0/15 (0%)** |
| M4 | *control:* DataHub is a hard dependency | 0/5 | 3/5 | 3/5 | **6/15 (40%)** |

### The variables did not come back clean

M2 is the strongest and most consistent signal in the set, and it arrives in
close to the predicted wording. All five `opus` runs made the same argument
independently, in nearly the same sentence:

> Writing your own authorization server is a decision most teams should be
> talked out of.
> — `baseline/eval-opus-run1.md`

> **Writing your own OAuth 2.1 authorization server** is a hard,
> high-consequence thing that very few teams should do.
> — `baseline/eval-opus-run2.md`

`sonnet` reached it three times out of five, by the same route:

> Rolling your own OAuth 2.1 authorization server (with PKCE and Dynamic Client
> Registration) is historically one of the riskiest things a project can take
> on — auth servers are a favorite target and easy to get subtly wrong.
> — `baseline/eval-sonnet-run1.md`

Reading the nine M2 fires back against the rubric, six are explicit arguments of
that form. The other three fire in a weaker shape, naming the OAuth server in a
list of products the project bundles. Two of those three (`haiku-run2`,
`sonnet-run4`) also mislabel it, calling the platform a "full IdP" and "an
identity provider", which is the misconception itself and is why they count.
The third, `sonnet-run2`, does not: it says "an OAuth server", which is
accurate, and its criticism is of scope breadth rather than of auth. On that
distinction it is a judge over-fire, leaving a defensible 8/15 of which 6 are
the direct argument. The conclusion is unaffected either way.

M1 fires less often and less sharply, and it is entangled with M2: three of its
four fires name the OAuth server as the reason the maintainer base worries
them.

> Approach with more caution if you'd be adopting it for the gateway, the
> portal, or the built-in OAuth server: that's a lot of security-critical
> surface from a thin maintainer base, in a protocol ecosystem still changing
> underneath it.
> — `baseline/eval-opus-run3.md`

### One control held, one failed

M3 is clean at 0/15. Every model that discussed effectiveness engaged with the
benchmark section rather than claiming no evidence exists. Several criticized
it as vendor-produced and single-model, which is fair and is not this
proposition. The section works.

M4 fired at 40%, and it was predicted to be clean.

> Locks users into the DataHub ecosystem or forces them into the PostgreSQL-only
> path (losing semantic features).
> — `baseline/eval-haiku-run1.md`

> Requires managing: PostgreSQL + pgvector, DataHub, Trino, S3
> — `baseline/eval-haiku-run3.md`

#1118 attributed a clean M4 to the deployment-shapes paragraph answering it on
the first screen. That explanation is now falsified, and so is the issue's
stated fallback reading, that a control firing means the evaluator is not
reading the page. M3 at 0/15 rules that out: these readers do reach the page's
claims. The asymmetry between the two controls is structural rather than
attentional. M3's answer is a section with its own heading, `## Does it work?
Measured effectiveness`. M4's answer was a single unheaded paragraph in the
opening block. Two of the six M4 fires quote that paragraph and conclude lock-in
anyway, which is what a claim that is present but not prominent looks like.

## Decision

The probe's own gate was: close #1118 if the variables come back clean. They did
not, so the documentation changes proceed. The falsified M4 control added a
fourth change to the issue, giving the PostgreSQL-only shape the structural
prominence the benchmark section already has.

## After the change

Same harness, same rubric, same n, run against the edited `README.md`. Raw runs
in [`after-1118/`](after-1118/).

| | Proposition | baseline | after | opus | sonnet | haiku |
| --- | --- | --- | --- | --- | --- | --- |
| M1 | velocity outruns scrutiny | 4/15 | **5/15** | 2/5 | 2/5 | 1/5 |
| M2 | rolled their own OAuth | 9/15 | **4/15** | 1/5 | 3/5 | 0/5 |
| M3 | *control:* nobody validated it | 0/15 | **0/15** | 0/5 | 0/5 | 0/5 |
| M4 | *control:* DataHub is a hard dependency | 6/15 | **2/15** | 0/5 | 0/5 | 2/5 |

M2 fell from 60% to 27% and M4 from 40% to 13%. M1 did not fall. M3 held at
zero, which is the evidence that the run is comparable to the baseline at all.

**M2's residue is a different belief than the one that was measured.** The
misconception is that the project reimplemented identity *instead of*
delegating. The four remaining fires no longer say that. They accept the
delegation and argue past it, about attack surface:

> the README's careful "we're a broker, not an IdP" section is a good-faith
> framing, but you are still shipping `/authorize`, `/token`, `/register`, code
> storage, redirect matching, and rate limiting.
> — `after-1118/eval-opus-run5.md`

That is a defensible engineering opinion about a real surface, not a factual
error about the architecture, and no wording fixes it because nothing about it
is wrong. The rubric counts it because it still treats the built-in
authorization server as a red flag. Read strictly for the factual claim, opus
went from 5/5 to 0/5 asserting reimplemented identity.

**M1 did not move, and the posture block is why it is now better argued.** Two
of the five fires recite the block's own contents before rejecting it as
insufficient:

> Genuinely good practices are listed (bcrypt for machine secrets, SHA-256 token
> digests, AES-GCM for refresh tokens, rate limiting before bcrypt cost,
> fail-closed persona defaults), but none of this has been externally attested
> (no third-party pentest or audit is mentioned, only OpenSSF
> Scorecard/CodeQL/Semgrep, which are automated static tools, not human security
> review).
> — `after-1118/eval-sonnet-run5.md`

The block was read and believed; the criticism moved to the one thing it does
not claim, which is external human review. Nothing in the repository can answer
that, because the answer is a third-party audit rather than a sentence. #1118
anticipated exactly this: an evaluator asked for a critical assessment will
manufacture skepticism regardless, and documentation narrows the surface it can
land on rather than eliminating it. Treating a residual M1 as a documentation
defect would invite writing something the tree cannot support.

At n=15 per arm the 9-to-4 and 6-to-2 moves are worth believing and the 4-to-5
move is noise. This is a decision aid, not a measured effect size.

## Re-running

```bash
source ~/.bench-key.env          # ANTHROPIC_API_KEY
./probe_eval.sh  <model> <run> <out-dir> <readme-path>
./probe_judge.sh <transcript>   <out-dir> rubric.txt
```

Both take one unit of work per invocation so a full sweep parallelizes with
`xargs -P`. Write a re-run to a new sibling of `baseline/` named for the state
being measured; the baseline is what any later result is read against, so it is
never overwritten.

A caveat that limits what a follow-up comparison can claim: at n=5 per model the
per-cell confidence interval is very wide, and these rates are a decision aid,
not a measured effect size. A change that moves M2 from 9/15 to 4/15 is worth
believing; one that moves it to 8/15 is noise. #1118 states the same limit in
its own terms, that a residual misconception rate is expected and is not a
defect to reopen.
