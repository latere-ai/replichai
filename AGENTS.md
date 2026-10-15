Respond briefly and precisely.
A bug fix is not complete without a test that fails without the fix and passes with it.
Specs live in specs/. A large feature starts as a tightly scoped spec, then the implementation; when it is done, record the outcome in the spec and update specs/README.md in the same change.
Commit often, one small change per commit. Maintainers push to main once a batch of work is complete and verified; contributors open a pull request.
Clean up any leftovers in follow-up commits.
Aim for high quality: every feature verified by an end-to-end test, and every package held to the shared gate's 90% coverage floor. The floor is waived here until the date in .lateregate.yaml, so new code must not widen the gap.
Write documents in the reader's language and in simple terms. Documents exist so users understand what the software is for and how to use it.
Nothing private goes in this repository: no token, no internal hostname, no link to a private repository, no person's data. Latere hostnames appear only as defaults or examples.
Every sentence is written for one reader (user, operator, contributor), and the register follows that reader: https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md
Write in American English, without em dashes.
