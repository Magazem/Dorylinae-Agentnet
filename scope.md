# AgentNet — Scope

## Project

AgentNet is a local-first coordination layer that allows agents running in different harnesses and on different machines to discover each other, exchange work safely, collaborate through bounded sessions, and produce signed decision records.

## Source of Truth

The complete product definition and build plan is in:

`AgentNet Free Tier Build Plan.md`

**Read that file before making implementation decisions.**

It is the authoritative source for:

- Product goals and boundaries
- Architecture
- Technology choices
- Protocol and data models
- Implementation phases
- Tickets and acceptance tests
- Security requirements
- Testing strategy
- Metrics and gates
- Project decisions

Do not invent a competing architecture or roadmap when the build plan already specifies one.

## Current Development Model

The project is built incrementally using the phases and tickets defined in the build plan.

Each phase-table step is one ticket.

A ticket must have:

- A clear acceptance test
- A defined scope of files it may modify
- Defined interfaces/schemas it must preserve
- Dependencies on previous work where applicable

Tickets should be small enough to represent roughly one day of agent work. If a ticket is larger, split it before implementation.

## Entry Criteria

Do not begin a ticket unless:

1. Its acceptance test exists or can be made runnable.
2. Required interfaces are already available.
3. Required schema changes are documented first.
4. The ticket is small enough for one focused agent context.

## Exit Criteria

A ticket is complete only when:

1. Its acceptance test passes.
2. `go vet` and the project linter pass.
3. Relevant `--help` and `--json` output is documented.
4. Required audit-log behavior exists.
5. The change has been manually tested where required.

## Development Rules

- Follow the phase and ticket order in the build plan.
- Do not introduce features outside the defined phase without first adding them to the plan.
- Preserve the security boundaries defined by the plan.
- Untrusted text must never grant authority.
- Only daemon-issued capability tokens may grant authority.
- CLI commands should return within two seconds or return an ID that can be polled.
- Keep agent contexts focused on one ticket.
- Use the repository and ticket as the persistent context between agents rather than relying on previous conversation history.

## First Action

Before implementing anything:

1. Read `AgentNet Free Tier Build Plan.md`.
2. Determine the current phase and first incomplete ticket.
3. Inspect the repository to determine what has already been implemented.
4. Compare the repository state against that ticket's acceptance criteria.
5. Work only on the smallest next required increment.

If the repository state and the build plan disagree, surface the discrepancy before making a large architectural change.