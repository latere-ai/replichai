# Specs

Designs for work not yet built. One spec per tightly-scoped feature; each states the problem, the decisions, the acceptance criteria, and what it deliberately leaves out.

| # | Spec | Status | Depends on |
|---|---|---|---|
| 001 | [Paper identity and slugs](001-paper-identity-and-slugs.md) | implemented | — |
| 002 | [Publishing reproductions](002-publishing-reproductions.md) | implemented | 001 |
| 003 | [The reproduction registry](003-reproduction-registry.md) | draft | 001, 002 |
| 004 | [Port the core to Go](004-go-port.md) | implemented | — |
| 005 | Capture and publish the reproduction transcript | planned | 002 |
| 006 | [The web application](006-web-application.md) | draft | 002, (003, 005) |
| 006f | [Taking a reproduction out](006f-taking-a-reproduction-out.md) | draft | 002, 004, (006c) |

Read 001-003 in order: 001 decides what a paper *is*; 002 decides where a reproduction *goes*; 003 decides how repeat reproductions of one paper find each other. 004 is done and kept as the record of why the core is Go and what the port had to preserve.

005 (planned) captures the agent's full session transcript as a first-class published artifact. 006 is a design spec for the single-tenant web application; it consumes 002/003/005 and spawns the child specs 006a–006f. It ships as a closed beta: anyone signs in with their latere account, but only seed users a platform admin has granted access reach the operator UI. The engine specs come first — the web layer wraps them, it does not replace them.

006f is the first 006 child drafted: publishing to GitHub with agent-authored commit history, on the native repo-mount mechanism a live spike confirmed. It revises 002's push model for the backend path (002's forge stays for offline/CLI), moves the redistribution gate from a pre-filter to a post-run verifier, and adds the collaborator invitation and the trusted-only visibility flip.
