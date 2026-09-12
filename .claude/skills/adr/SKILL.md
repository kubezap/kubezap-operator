---
name: adr
description: Record a non-obvious architecture or technical decision as a design record. Use whenever a CRD/type change, new controller/gateway/binary, reconciliation-logic change, security-posture change, or new external dependency gets decided, or when the user wants to document/revisit one.
---

# ADR (Design Record)

Record a technical decision so future sessions and subagents have the rationale, not just the outcome. KubeZap's design records use a required 6-section format (not a lean 4-section ADR) — see `planning/process/design-process.md` for the full rules; this skill just wraps that process.

## Steps

1. Confirm this actually needs a design record — check `planning/process/design-process.md`'s "When This Applies" list. Skip for typo fixes, test-only additions, doc-only changes.
2. Copy `.claude/templates/design-record.md` to `docs/design/<YYYY-MM-DD>-<slug>.md` (today's actual date, not a placeholder).
3. Fill in all six required sections: Problem Statement, Constraints, Invariants, Rejected Alternatives, Tradeoffs, Final Decision. Do not omit any — `planning/process/design-process.md` treats an omitted section as blocking implementation.
4. Set the header's `> Status:` to `Draft` while writing, `Approved` once implementation is confirmed correct (build/test/lint clean and, if applicable, live-validated).
5. Add or update the record's row in `docs/design/README.md`'s index table (date, title, status, one-sentence summary).
6. If this decision affects the overall stack or a documented architecture, update `docs/architecture.md` or the relevant `docs/api/*.md` with a short summary + link back to the record — those docs stay skimmable; the record holds the full reasoning.
7. If a decision is later reversed, don't edit or delete the old record — write a new one and mark the old one's Status as `Superseded by docs/design/<file>.md`, and update the index row.

## Don't

- Don't write a design record for reversible, low-stakes choices (a variable name, a one-line bug fix) — this is for decisions that would be expensive to unwind, or that a future agent might otherwise redo differently without the context.
- Don't skip a section because it feels obvious — the Rejected Alternatives section in particular is often the most useful part later, even when the answer seems self-evident now.
