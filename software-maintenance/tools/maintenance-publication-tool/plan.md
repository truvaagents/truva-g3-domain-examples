# Maintenance publication tool — implementation reference

Current as of the v0.6.0 acceptance run on 2026-10-13. See the
[README](../../README.md) and [profile guide](../../PROFILE_SETUP_GUIDE.md)
before enabling writes.

## Authority and capabilities

This is the only component with a GitHub write credential. It reads current
remediation evidence and can publish an exact verified, independently approved
candidate as a draft PR. It cannot merge, approve, mark ready, deploy or change
repository policy.

[tool.go](tool.go) declares `list_open_remediations`,
`get_remediation_changes`, `get_remediation_state`, `get_publication` and
`publish_draft_pr`. The state read accepts a PR number or an attributed
publication receipt plus its original case. Historical receipt attribution
survives compatible read-only profile changes, with limitations reported;
historical evidence does not authorize a new write.

## Publication controls

The tool retrieves bytes/verification directly from the workspace issuer and
approval directly from the review issuer. Caller prose or booleans do not
establish eligibility. It validates scope, expiry, profiles, candidate/test
hashes and file modes before GitHub mutation. Current base identity must agree
with the verified snapshot.

Publication identity binds case, candidate, verification, review and profile.
Branch/commit reconciliation handles repeated and ambiguous writes. Fresh bounded
open-PR checks also reject matching production-file bytes on the same base;
this is not semantic equivalence or an atomic cross-publisher lock.

The model supplies title/body. Validated publication metadata is retained for
reconciliation; the commit subject uses the proposed title. Author comes from
the profile, whose shipped value is an example placeholder, not a developer's
personal identity. Source and publication must name the same configured
repository; a fork-to-upstream PR workflow is not implemented.

Read cooldowns report `Retry-After` and `retry_not_before`. An explicit
`retryable:false` stops same-step retries; later calls may retry after the
deadline. This does not provide automatic durable waiting.

## Readiness and verification

The profile now has `publication_ready: true`, but the environment template
keeps `MAINTENANCE_PUBLICATION_ENABLE_WRITES=false`. Credentials and explicit
operator authorization are still required. Ready profile bytes alone grant no
write permission.

The v0.6.0 run inspected existing work and created draft PR #16 for the correct
WELCOME100 repair after baseline/candidate verification and independent review.
It neither merged nor deployed the fix. An open-PR repeat was not part of that
release acceptance run. New-user fork portability remains a
[release gate](../../README.md#public-release-readiness).
