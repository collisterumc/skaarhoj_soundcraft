# Agents

This file is the shared memory for all agents working in this repo. When you learn
something about the project's conventions, preferences, or patterns, try to find a place
to update the repository documentation directly. But failing that, or for truly global
conventions, update this file. Do not depend on local/session memory. Those aren't
committed to the repo and aren't available for other users or agents to reference.

## Design Tradeoffs

When a fix or feature change conflicts with an existing design decision, **stop and ask**
rather than silently degrading the system. Explain the tradeoff and let the human decide.
Settled decisions are recorded in IMPLEMENTATION.md § "Decision log" — check there before
revisiting anything.

## Milestones and Gates

Work proceeds in the milestone order defined in TODO.md. Each milestone ends with a
**Gate** of agent-assertable criteria; verify the gate mechanically (build, test, grep,
protocol trace) before starting the next milestone. When a gate is satisfied, check the
boxes and, for decisions, add a dated `DECISION:` entry to IMPLEMENTATION.md.

## Dependencies

Pin Go module versions to exact versions in `go.mod` (no floating requirements). When
adding a new dependency, install it first, check the resolved version, and pin that
version. Check license compatibility before adding anything — this repo is BSD-3-Clause
and some upstream SKAARHOJ code carries Apache-2.0 + Commons Clause (see the Decision log
on licensing).

## Reference Material

`reference/` holds clones of upstream repos, sample packages, and PDF manuals. It is
git-ignored on purpose (license/copyright) — never commit anything from it, and don't copy
code near-verbatim from `core-skaarhoj-template`; implement from the IMPLEMENTATION.md
spec instead. Throwaway probe/experiment code goes in `/tmp`, not the repo.

## Documentation

Project documentation:

| Document | Purpose |
|----------|---------|
| [README.md](README.md) | Project overview, scope, and supported mixers |
| [TODO.md](TODO.md) | Milestone checklists with agent-assertable gates |
| [IMPLEMENTATION.md](IMPLEMENTATION.md) | Architecture, protocol reference, design decisions, decision log |

IMPLEMENTATION.md is a development-time document: it gets deleted when the app is done.
Never cite it from anything that outlives development — a code comment must state its
constraint or reason in full, not point at IMPLEMENTATION.md or a decision-log entry for
it. When the project wraps up, move anything still needed long-term (the licensing
decision, protocol facts a maintainer would miss, deployment details) into README.md
before deleting it.

Document directory hierarchy, not individual filenames. Don't list specific counts,
file-by-file breakdowns, data sizes, or row counts — these go stale when data changes.
When counts are necessary, note the date they were captured.

This is a public repository: keep documentation neutral and don't document internal
SKAARHOJ details beyond what's needed to build and deploy the core.

## Agent team

Three subagents in `.claude/agents/` divide the work. Use them in this order; each is
scoped deliberately, so don't ask one to do another's job.

| Agent | Use it for |
|-------|------------|
| `core-builder` | Building anything: code, tests, packaging, scripts, config, tooling |
| `adversarial-validator` | Attacking a finished change or claim before a Gate box gets checked |
| `clarity-reviewer` | Complexity and readability only — it does not hunt for bugs |

Give each one a bounded task. Run `adversarial-validator` even when the work looks
correct; it exists for exactly that case.

**Some work is not delegable.** A milestone that says so in TODO.md runs in the main
session, in small reported steps. Long interactive sessions against the owner's physical
hardware are the case that matters: the owner needs to see progress and be able to step
in, and a subagent that dies mid-run leaves the equipment in an unknown state.

Keep project facts in the documents, not in the agent definitions. The agents are told
which sections to read and are expected to re-read them each run, so a fact recorded once
in IMPLEMENTATION.md reaches all three and cannot drift out of sync.

## Git

**NEVER run `git commit`, `git push`, or any other git write operation unless the user has explicitly asked you to.** Stage files, show diffs, and describe what you would commit — but do NOT commit or push without a direct, unambiguous instruction from the user.

**"Commit" means commit to the branch that is currently checked out.** Never create a branch first, and never switch branches, including on `master`. This repository's work lands on `master` directly; branching without being asked leaves the user with a merge they did not want. If a branch is wanted, the user will say so.

Commit messages: a summary line stating the purpose, a blank line, then a few bullets — one per logical change. A one-line message is fine when the summary says it all.

- Bullets are ≤ 15 words. Count.
- Every bullet anchors to a staged change: it states that change's purpose, or the reason it was needed.
- Never restate the diff's contents (file lists, counts, specifics) — and never claim what the diff doesn't do (consequences elsewhere, plans, findings — those go in docs).
- Plain reader language, not internal jargon.
- Write from the staged files, not chat context or prior intent.

Hard rule — test every bullet before committing; delete or rewrite any failure:

1. Count its words. More than 15 → rewrite.
2. Name the staged change it describes. Can't → delete.
3. Is it that change's purpose or reason, or just the diff restated? Restated → delete.
4. Does it need project jargon to say? Yes → say it plainly.

Good — each bullet anchors to a staged change (the new default; the TODO entry):

```
Temporarily point the agent broker at the raw API Gateway hostname

- a content filter blocks the new custom domain at one site
- a TODO reverts this once the domain is unblocked
```

Bad — every bullet fails a test:

```
Update settings.py and TODO.md (2 files)

- changed broker_url default on line 49 of settings.py
- leverages per-caller ceilings for downstream mint hygiene
- old URLs keep working until the fleet cuts over
```

## Write plainly

This covers everything you write: chat replies, commit messages, comments,
docstrings, Markdown, pull request bodies, agent briefs.

**Structure**

- Lead with the answer or the action.
- Put the subject and verb near the start of the sentence.
- State the point, then explain it.
- One idea per sentence. Keep most sentences under 20 words.
- Use ordinary verbs, not abstract nouns.

**Length**

- Chat replies: a few sentences, or five bullets at most.
- Progress updates: two sentences.
- Documents run as long as their content needs. Write plain sentences, not
  fewer facts — never drop a constraint, measurement, date, or reason to hit
  a length.

**Don't**

- Restate the request back.
- Narrate your reasoning or critique yourself unless asked.
- Put two em-dash asides in one sentence.
- Rename a thing mid-paragraph. If it is the stream, call it the stream.
- End on a flourish that adds no fact.

**Examples**

Bad: "The only notification anyone receives fires at post time."
Good: "Notifications only fire at post time."

Bad: "Hence the two things this does not read: the status, and the current
revision."
Good: "It ignores the status and current revision."

Bad: "The removal of the fold was a simplification."
Good: "Removing the fold simplified it."

## Code comments

- Comment only non-obvious constraints, invariants, or reasons.
- Do not describe what readable code already says.
- Use one short sentence where you can.
- Never put status reports, change summaries, or conversation in a comment.
- Before finishing, delete comments that clear naming made unnecessary.