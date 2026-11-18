# Maintenance repair agent — implementation reference

Use the [solution README](../../README.md) for setup and current verification
scope.

## Goal and framework integration

Provide a bounded autonomous reasoning context for an admitted case and
immutable source snapshot. The agent may inspect evidence, stage candidate and
regression-test bytes, and request owner-approved sandbox checks. Review,
publication, merge, deployment and further delegation are outside its role.

[agent.go](agent.go) constructs normal framework orchestration with discovery,
Redis-backed execution/debug stores and published skill bindings.
[handlers.go](handlers.go) routes `propose_maintenance_change` through
`ProcessRequestWithExecution`. The model chooses actions; there is no
application-defined repair-stage sequence or role-specific catalog wrapper.
The supplied environment disables tiered selection without replacing discovery.

Always-required safety, result-contract and repair-role skills remain available
when optional skill activation fails. Profiles inject authorized scope and result
bounds, not a predetermined diagnosis.

## Result boundary

[result.go](result.go) validates model decisions and references from successful
tool steps. Supported decisions are `candidate_submitted`, `no_change`,
`not_reproduced` and `inconclusive`.

- A negative decision may preserve exploratory artifacts without recommending them.
- Candidate submission requires trusted workspace, candidate and verification
  references, matching case/source/profile identities and approved prerequisites.
- Verification may still be pending/running at submission. If the aggregate is
  pending or running and all its jobs have finished, the repair-role skill calls
  for one `run_verification` call with the same case and candidate before handoff.
  This refreshes the aggregate using the original jobs. A succeeded record must
  include the required reproduction, suite and publication-eligibility facts.
- Aggregate `execution_success` and bounded failed-operation evidence are
  exposed separately. A prior failed attempt is not, by itself, a reason to
  discard later valid evidence.
- An orchestration error or invalid result produces an error response with
  bounded actionable feedback, not an invented model decision.

Candidate bytes, jobs, artifact identities and authoritative verification remain
workspace-tool responsibilities. Service credentials are separate from
model-visible parameters.

## Verification

The [November 17 Kind run](../../README.md#draft-review-and-publication-verification-2026-11-17)
verified that the repair agent refreshed the aggregate before handoff. It also
verified that the agent recognized successful build and vet stages from the
protected script and its exit status when those stages emitted no output.

The v0.6.0 live run produced the correct WELCOME100 candidate and regression:
baseline failed at the expected assertion, candidate tests and vet passed.
A concurrent evidence-read conflict recovered on its second attempt. The
generated test still had minor formatting differences; the current check profile
does not enforce `gofmt`.

See [release verification](../../README.md#v060-release-adoption-verification-2026-10-13)
and the README's remaining public-release gates. One successful case is not a
claim of universal repair correctness.
