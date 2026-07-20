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

Document directory hierarchy, not individual filenames. Don't list specific counts,
file-by-file breakdowns, data sizes, or row counts — these go stale when data changes.
When counts are necessary, note the date they were captured.

This is a public repository: keep documentation neutral and don't document internal
SKAARHOJ details beyond what's needed to build and deploy the core.

## Git

**NEVER run `git commit`, `git push`, or any other git write operation unless the user
has explicitly asked you to.** Stage files, show diffs, and describe what you would
commit — but do NOT commit or push without a direct, unambiguous instruction from the
user.

Commit messages: a short summary line stating the purpose, then a blank line, then a few
brief bullet points — one per logical change. Keep bullets short (≤ 15 words); don't
repeat details the diff already shows (file lists, counts, specifics).

When writing a commit message, always review the actual files being committed rather than
relying solely on chat context. The message should reflect the changes represented in the
files themselves rather than any prior description or intent.
