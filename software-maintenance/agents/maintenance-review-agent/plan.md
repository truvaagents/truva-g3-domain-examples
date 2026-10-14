# Maintenance review agent — implementation reference

Current as of the v0.6.0 acceptance run on 2026-10-13. See the
[solution README](../../README.md) for setup and verification scope.

## Framework path and role

The public `review_maintenance_change` capability is an orchestrator capability.
[handlers.go](handlers.go) calls `ProcessRequestWithExecution`;
[agent.go](agent.go) wires normal discovery, skills and execution/debug backends.
No role-specific discovery allowlist or fixed review plan replaces the harness.
The supplied environment disables tiered selection.

Safety, review-result-contract and independent-verification skills are always
required. Evidence-investigation and outcome-assessment are selectable. The
reviewer assesses one exact candidate, its contract and actual before/after
verification. It has no candidate-editing or publication authority.

## Approval boundary

[receipt.go](receipt.go) accepts `approved`, `rejected` or `inconclusive`
model decisions. Negative decisions need no approval receipt or mandatory
evidence-call set.

Approval requires successful exact `get_service_profile` and
`get_review_bundle` results for the requested scope. The code validates
candidate/test/workspace/verification/job relationships, profiles, contract
references and expiry. Unsupported approval returns an actionable error; the
service does not manufacture a different model verdict.

The service creates an immutable case-scoped approval receipt only after these
checks. Only the publication-tool identity can retrieve it through the internal
`get_review_receipt` endpoint. Aggregate execution health and failed operations
remain separate facts in the response; a prior failed attempt does not replace
or invalidate authoritative evidence automatically.

## Verification

In the v0.6.0 existing-cluster run, the reviewer inspected the WELCOME100
candidate, contract and verification evidence and approved the exact candidate.
All 17 review steps completed successfully. This establishes that case's
approval path, not production security, deployment, or exhaustive review quality.
The test formatting issue was not raised as a blocker.

Current setup, profile readiness and remaining acceptance gates are documented
in the [README](../../README.md#public-release-readiness); private historical
investigation notes are not setup prerequisites.
