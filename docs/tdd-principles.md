# Test-Driven Development Principles

Every feature in confine-ai is built through strict Red-Green-Refactor cycles, gated by a design check that decides whether the current architecture can support the next test before it is written. This document defines that methodology: the cycle, the phase rules, the design-check decision tree, and the quality gate that must pass before any code reaches review. It is the methodology reference — not the day-to-day checklist.

The reason the project commits to TDD is that AI coding agents lack three things by default that the discipline supplies: a concrete definition of "done" (the failing test), a fast feedback loop (seconds, not full-implementation cycles), and incremental progress that survives a cut session. Without these, agents write large untested blocks, guess at requirements, and produce code that passes nothing on first run.

For the short actionable cycle checklist loaded during implementation, see [`.claude/skills/tdd-workflow/SKILL.md`](../.claude/skills/tdd-workflow/SKILL.md). For test structure and conventions (table-driven tests, helpers, mocking policy), see the Testing Strategy section of [`CLAUDE.md`](../CLAUDE.md) and the Google Go Testing Best Practices linked from there.

## The TDD Cycle

Every feature is built through strict Red-Green-Refactor cycles. The feature-implementer never writes production code without a failing test first.

### Cycle Steps

| Step | Action | Rule |
|------|--------|------|
| **Plan** | Break the feature into TDD cycles | Write plan to `.scratch/implementation-plan.md` |
| **Design check** | Verify the current design supports the behavior | Gate before every cycle (see below) |
| **Red** | Write a failing test | Test must fail for the right reason |
| **Green** | Write minimum code to pass | No more code than the test demands |
| **Refactor** | Clean up, keep tests green | No new behavior during refactor |
| **Next cycle** | Return to design check | Repeat until feature complete |

### Design Check Gate

Before each Red phase, evaluate the codebase:

| Assessment | Action |
|------------|--------|
| **Ready** | Proceed to Red |
| **Small code gap** | Refactor first (keep tests green), then Red |
| **Design gap** | Invoke system-design-expert agent. Wait for approval |
| **Requirement gap** | Log feedback, invoke product-requirements-expert agent |
| **Architecture misfit** | Stop. Escalate to system-design-expert with `[ESCALATE]` |

The design check prevents forcing code into a design that cannot support it. Without this gate, implementers accumulate technical debt by working around structural problems instead of fixing them.

## Red Phase Rules

- Write exactly one test that fails.
- The test must fail for the right reason — a missing method, wrong return value, or unhandled case. Not a compilation error in unrelated code.
- Run the test and confirm it fails before proceeding.

## Green Phase Rules

- Write the minimum code to make the failing test pass.
- Do not generalize. Do not optimize. Do not handle cases the test does not cover.
- Run all tests after each change. If any test breaks, fix it before continuing.
- "Minimum" means the simplest implementation that satisfies the test — even if it looks naive. Subsequent cycles will drive the design toward the correct abstraction.

## Refactor Phase Rules

- Refactor only when all tests are green.
- No new behavior during refactor. If the refactoring introduces a new code path, it needs its own Red-Green cycle.
- Run all tests after each refactoring step.

## Quality Gate

Before invoking reviewers, all checks must pass. confine-ai enforces these via `make ci`:

| Check | Purpose |
|-------|---------|
| `go mod tidy` | No drift in `go.mod`/`go.sum` |
| `go fmt` | Code meets formatting standards |
| `go vet` | Standard static analysis |
| `golangci-lint` | Project lint rules pass |
| `deps-check` | No prohibited dependencies introduced |
| `go test` | All tests pass |
| `go build` | Project compiles without errors |

No exceptions. Fix failures before requesting review. See [`.claude/skills/code-quality-gate/SKILL.md`](../.claude/skills/code-quality-gate/SKILL.md) for the full gate definition.

## Bug Fixes Start with a Test

Every bug fix begins with a reproducing test — a test that fails because of the bug. Fix the bug. Confirm the test passes. This prevents regressions and documents the fix.

## Document Ownership During TDD

The feature-implementer agent writes code and tests. It does not modify documentation directly.

| Need | Action |
|------|--------|
| Requirement unclear | Log feedback, invoke product-requirements-expert |
| Design needs updating | Invoke system-design-expert |
| Architecture misfit | Escalate to system-design-expert with `[ESCALATE]` |

This separation ensures documentation changes go through the owning agent, not through ad-hoc edits during implementation.
