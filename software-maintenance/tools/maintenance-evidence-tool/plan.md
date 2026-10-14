# Maintenance evidence tool — implementation reference

Current as of the v0.6.0 acceptance run on 2026-10-13. This replaces the initial
pre-deployment plan and synthetic examples. Use the
[README](../../README.md) for setup and the
[profile guide](../../PROFILE_SETUP_GUIDE.md) for a credential-safe profile read.

## Responsibility

A passive, profile-scoped adapter for service observations, saved business
outcomes, runtime configuration and build provenance. It returns facts and
coverage limitations, not diagnoses or proposed fixes. It does not mutate the
target, inspect GitHub source, execute code or call an LLM.

Implementation lives in [internal/serviceevidence](../../internal/serviceevidence);
[main.go](main.go) owns service bootstrap. The same evidence reader is reused
by the assessment tool. Framework dependencies are pinned to v0.6.0.

## Capabilities and field relationships

Current schemas are registered in
[tool.go](../../internal/serviceevidence/tool.go).
Invoke public capabilities with `POST /api/capabilities/<name>`, JSON parameters
and an authorized service bearer token. Success is HTTP 200 with
`{"success":true,"data":...}`; errors use non-2xx and a structured tool error.

| Capability | Required parameters | Purpose |
| --- | --- | --- |
| get_service_profile | profile_id | Read approved identity, canonical version, contract references, readiness and bounds |
| list_service_observations | profile_id, start, end | Read bounded advisory candidates; optional limit is capped by profile |
| get_service_evidence | profile_id, observation_id | Read observation, serving revision and optional saved-outcome reference |
| get_business_outcome | profile_id, outcome_id | Independently read an immutable accepted request, pricing snapshot and ledger |
| get_release_context | profile_id | Read current build provenance; optional expected_revision compares a serving build identity |
| get_runtime_configuration | profile_id | Read business configuration between two build-identity reads |

Important relationships in the HTTP success envelope:

- `data.observation.serving_revision` is the `build-...` value accepted as
  `expected_revision`. The separate Git commit identifies repository source.
- `data.outcome_reference.outcome_id` supplies the string for
  `get_business_outcome`; the reference object is beside `observation`,
  not inside it, and can be absent for rejected attempts.
- Serving revision and the original accepted order's revision can differ on
  replay. Current configuration alone does not establish historical applicability.
- `data.profile_version` is the typed canonical digest, not a raw JSON file hash.

## Bounds and failure semantics

Profiles own upstream URLs/endpoint templates, caller permissions, contract pins
and read limits. Requests cannot supply a target URL. The shipped observation
bounds are a ten-minute maximum window, 24-hour retention and 50 items.
Truncated windows and advisory coverage are explicit; no cursor or complete
transactional stream is invented.

Authentication uses distinct coordinator, repair and review identities.
Credentials stay outside model-visible capability parameters. The target's
current read endpoints do not require the order-submission token; deployment
isolation and target-side authorization remain separate responsibilities.
Single-owner profile controls are not a multi-tenant security claim.

The adapter uses bounded traced HTTP with request-context cancellation and
redirects disabled. It validates response shapes and returns sanitized structured
errors for missing evidence, timeouts, unavailable sources and invalid data.
Runtime-configuration revision changes return a retryable `RELEASE_CHANGED`.
There are no hidden internal retries in the evidence HTTP reader.

## Readiness and verification

The service profile now has `assessment_ready: true` and an immutable
owner-approved contract reference. The initial unavailable-target and
collection-only limitations are no longer the shipped setup.

The v0.6.0 live run read real observations, service provenance and saved outcomes
as inputs to the verified WELCOME100 repair. This does not establish complete
observation coverage, signed provenance, fork portability or production security.
See [release readiness](../../README.md#public-release-readiness).

Deploy through the solution-level setup script; container port is 8397.
The credential and profile variables are listed in [.env.example](.env.example);
shared non-secret settings come from the solution root template.
