# Maintenance review agent — implementation reference

See the [solution README](../../README.md) for setup and verification scope.

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

The `objective` field accepts non-empty UTF-8 prose, including line breaks and
tabs, within the profile's `maximum_objective_bytes` (8,192 in the shipped profile).
Other control characters are rejected. Case, candidate and verification
identifiers remain subject to their existing validation rules; accepting an
objective does not grant an approval receipt. `INVALID_REVIEW_SCOPE` distinguishes
invalid identifiers from objective failures and reports the objective's configured
byte limit and received byte count.

## Approval boundary

[receipt.go](receipt.go) accepts `approved`, `rejected` or `inconclusive`
model decisions. Negative decisions require neither an approval receipt nor a
mandatory set of evidence calls.

Approval requires successful `get_service_profile` and `get_review_bundle` results
matching the requested scope. The code validates relationships among the
candidate, test, workspace, verification and Job, as well as profiles, contract
references and expiry. An approval unsupported by that evidence returns an
actionable error; the service does not manufacture a different model verdict.

The workspace bundle supplies measured `target_sha256` values for each staged
regression file so the reviewer can establish matching bytes in the reproduction
and candidate trees. The review handler supplies the owner-approved
`acceptance_scope` as server context and returns it separately from the model
decision. The supplied `draft_pr` scope requires a successful
`repository-check` on the candidate. Its listed runtime checks remain pending
for deployment approval.

The service creates an immutable case-scoped approval receipt only after these
checks. Only the publication-tool identity can retrieve it through the internal
`get_review_receipt` endpoint. Aggregate execution health and failed operations
remain separate facts in the response; a prior failed attempt does not replace
or invalidate authoritative evidence automatically.

## Verification

The [November 17 Kind re-review](../../README.md#draft-review-and-publication-verification-2026-11-17)
reused the saved candidate and jobs. The reviewer cited matching regression
hashes and approved the exact candidate for draft publication. The stored
approval receipt matched the response.

In the v0.6.0 existing-cluster run, the reviewer inspected the WELCOME100
candidate, contract and verification evidence and approved the exact candidate.
All 17 review steps completed successfully. This establishes that case's
approval path, not production security, deployment, or exhaustive review quality.
The test formatting issue was not raised as a blocker.

Current setup, profile readiness and remaining acceptance gates are documented
in the [README](../../README.md#public-release-readiness); private historical
investigation notes are not setup prerequisites.
