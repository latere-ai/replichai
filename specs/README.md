# Specs

Designs for work not yet built. One spec per tightly-scoped feature; each states the problem, the decisions, the acceptance criteria, and what it deliberately leaves out.

| # | Spec | Status | Depends on |
|---|---|---|---|
| 001 | [Paper identity and slugs](001-paper-identity-and-slugs.md) | draft | — |
| 002 | [Publishing reproductions](002-publishing-reproductions.md) | draft | 001 |
| 003 | [The reproduction registry](003-reproduction-registry.md) | draft | 001, 002 |
| 004 | [Port the core to Go](004-go-port.md) | implemented | — |

Read 001-003 in order: 001 decides what a paper *is*; 002 decides where a reproduction *goes*; 003 decides how repeat reproductions of one paper find each other. 004 is done and kept as the record of why the core is Go and what the port had to preserve.
