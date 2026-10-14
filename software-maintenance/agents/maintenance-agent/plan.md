# Maintenance coordinator — implementation reference

Current as of the v0.6.0 acceptance run on 2026-10-13. This replaces the initial
implementation plan; it is not a list of work still pending. Use the
[solution README](../../README.md) and [profile guide](../../PROFILE_SETUP_GUIDE.md)
for setup, limits and remaining release gates.

## Responsibilities

The coordinator owns bounded observation collection, admission, application
case records and assessment handoffs. Framework stores own task/execution
lifecycle. The scheduler and collection worker read advisory observations
without an LLM; admitted investigations enter the normal orchestration harness.

- [agent.go](agent.go) constructs orchestration with runtime discovery and
  published skill bindings. There is no role-specific discovery allowlist.
  The supplied environment disables tiered selection; the framework still owns
  catalog construction, planning, execution and synthesis.
- [collector.go](collector.go) owns collection windows, deterministic sampling,
  coverage and admission. A selected observation is a lead, not a diagnosis.
- [investigation.go](investigation.go) submits the case through
  `ProcessRequestWithExecution` and validates the model's result against actual
  successful capability results. It preserves the model decision separately
  from receipt-validated operational status.
- Deterministic assessment capabilities live in the assessment tool, not in the
  coordinator's public reasoning capability. Post-completion bookkeeping records
  the outcome there; memory summaries are a separate framework facility.

## Outcome and safety boundaries

The agent can propose a repair, link attributable prior work, or return a
negative, blocked or inconclusive decision. A confirmed same-case publication
receipt establishes `draft_pr_opened`; prose alone does not. Linked conclusions
require their own evidence validation. The model does not choose trusted receipt
identities or bypass publication policy.

Per-step failures and aggregate `execution_success` remain separate from
domain-result acceptance. Valid artifacts are not erased by a later failed
step; invalid synthesis or an orchestration error is not fabricated into success.
Retained publication effects remain visible even if final validation fails.

Only this agent builds shared memory, in domain `software-maintenance`.
Historical memory is not current GitHub state or authority to suppress a case.

Collection ships disabled. Finite admission controls and an operator run ID
are required for the automatic test. The queued-task cancellation guard remains
best-effort; exactly-once delivery and crash-safe recovery are not established.

## Verification and current limitations

The v0.6.0 existing-cluster run completed collection → investigation → repair →
independent review → draft PR → assessment recording/readback. See the
[release verification summary](../../README.md#v060-release-adoption-verification-2026-10-13).
This does not prove cold installation, fork portability, all possible failure
paths or duplicate suppression under concurrent publishers. No merge or
production repair deployment is part of this example.
