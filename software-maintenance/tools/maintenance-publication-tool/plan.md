# Maintenance publication tool — implementation reference

Updated on 2026-10-16 to document safeguards for concurrent publication and
adoption of published v0.6.1 framework dependencies. See the
[README](../../README.md) and [profile guide](../../PROFILE_SETUP_GUIDE.md)
before enabling writes.

## Authority and capabilities

This is the only component with a GitHub write credential. It reads current
remediation evidence and can publish an exact verified, independently approved
candidate as a draft PR. It cannot merge, approve, mark ready, deploy or change
repository policy.

[tool.go](tool.go) declares `list_open_remediations`,
`get_remediation_changes`, `get_remediation_state`, `get_publication`,
`reconcile_publication` and `publish_draft_pr`. The `get_remediation_state`
capability accepts a PR number or an attributed publication receipt plus its
original case. Historical receipt attribution survives compatible read-only
profile changes, with limitations reported; historical evidence does not
authorize a new write.

## Publication controls

The tool retrieves candidate bytes and verification evidence directly from the
workspace issuer, and approval directly from the review issuer. Caller prose or
booleans do not establish eligibility. It validates scope, expiry, profiles,
candidate/test hashes and file modes before GitHub mutation. The current base
identity must match the verified snapshot.

Publication identity binds case, candidate, verification, review and profile.
Complete `list_open_remediations` results include `repository_view_id`. The
coordinator assesses that view and supplies it to `publish_draft_pr`; the tool
checks it again before GitHub writes. `REPOSITORY_VIEW_CHANGED` requests fresh
inspection and reassessment. Incomplete coverage blocks publication.

A Redis reservation scoped to the repository and base branch serializes this
tool's publication intents across cases, profiles and replicas. Ownership tokens
prevent stale workers from updating the reservation. The reservation retains an
intent with an uncertain outcome after its worker lease expires;
`reconcile_publication` uses the original case/publication references to inspect
GitHub and record or release the intent without new GitHub writes. Publication
remains blocked while external effects are uncertain. This coordinates cooperating
tool replicas; independent GitHub writers are outside its atomic boundary.

Fresh bounded open-PR checks also reject matching production-file bytes on the
same base. Different bytes require the agent's applicability assessment; these
controls do not prove semantic equivalence.

The model supplies the PR title and body. Validated publication metadata is
retained for reconciliation; the commit subject uses the proposed title. Commit
authorship comes from the profile, whose shipped values are example placeholders,
not a developer's personal identity. Source and publication must name the same
configured repository; a fork-to-upstream PR workflow is not implemented.

GitHub read cooldowns report `Retry-After` and `retry_not_before`. An explicit
`retryable:false` stops same-step retries; later calls may retry after the
deadline. This does not provide automatic durable waiting.

## Readiness and verification

The supplied profile has `publication_ready: true`, but the environment template
keeps `MAINTENANCE_PUBLICATION_ENABLE_WRITES=false`. Credentials and explicit
operator authorization are still required. Ready profile bytes alone grant no
write permission.

The v0.6.0 run inspected existing work and created draft PR #16 for the correct
WELCOME100 repair after baseline/candidate verification and independent review.
It neither merged nor deployed the fix. An open-PR repeat was not part of that
release acceptance run. A later multi-replica run created one correct draft and
rejected competing attempts with stale repository views; it does not prove
universal duplicate prevention. Fork portability for new users remains a
[release gate](../../README.md#public-release-readiness).
