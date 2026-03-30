# Alkemio WOPI Service

Go microservice implementing the WOPI protocol for Collabora Online
integration into the Alkemio platform.

## Tech Stack

- **Language**: Go 1.25
- **Database**: PostgreSQL, pgx v5 driver, sqlc for query generation
- **Messaging**: RabbitMQ via Watermill
- **Logging**: Zap (structured)
- **Architecture**: Hexagonal (ports and adapters)
- **Alkemio Server**: Node/TypeScript at `/Users/antst/work/alkemio/server`

## Architecture Rules

- Domain core has zero infrastructure imports
- External systems accessed exclusively through ports (interfaces)
  with concrete adapters
- No adapter imports another adapter directly
- SQL queries defined in `.sql` files compiled via sqlc — no
  hand-written queries outside migrations

## Anti-Patterns — Strictly Prohibited

1. Do not apply speculative fixes — find root cause first
2. Do not keep code "just in case" or for backward compatibility
   unless explicitly requested
3. Do not duplicate logic — find or create a single shared
   implementation
4. Do not add superficial tests for coverage padding
5. Do not invent performance SLAs without evidence
6. Do not create abstractions for hypothetical future needs
7. Do not add comments explaining obvious code
8. Do not rely on training data for dependency versions — check
   online
9. Do not create documentation files unless explicitly requested
10. Do not assume — ask or search when something is unclear

## Development Workflow

- Always run `golangci-lint run` before committing
- Tests must defend real invariants — no coverage-padding tests
- Root cause analysis is mandatory before any bug fix; document the
  cause with evidence
- Verify latest dependency versions online (pkg.go.dev, GitHub
  releases) — never trust training data
- If something is unclear, ask or search — do not guess

## Integration Context

- Auth delegates to Alkemio's Ory Kratos / JWT / policy engine
- Storage delegates to Alkemio's storage management layer
- RabbitMQ patterns align with existing
  `collaborative-document-integration` service (INFO, WHO, SAVE,
  FETCH)
- WOPI proof key validation required on all requests from Collabora

## Full Constitution

See `.specify/memory/constitution.md` for the complete set of
principles and governance rules.
