# Maintenance coordinator — implementation reference

Updated on 2026-10-15 for validation feedback on linked results. This replaces
the initial implementation plan; it is not a list of work still pending. Use the
[solution README](../../README.md) and [profile guide](../../PROFILE_SETUP_GUIDE.md)
for setup, limits and remaining release gates.

## Responsibilities

The coordinator owns bounded observation collection, admission, application
case records and assessment handoffs. Framework stores manage task and execution
lifecycles. The scheduler and collection worker read advisory observations
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

For linked results, the model sets `next_check.not_before` to a future time
within `result_contract.maximum_next_check_delay_seconds` and at or before the
selected assessment's `revalidate_after`. Validation occurs after evidence
refresh. A selected time that has passed or exceeds either bound produces
`LINKED_NEXT_CHECK_INVALID` feedback that includes the selected time and permitted
window. Invalid values are reported and rejected; the server does not silently
change the schedule. A recorded next check is guidance; it does not schedule
automatic execution. Expired or inapplicable assessments are rejected separately
from this timing error.

Per-step failures and aggregate `execution_success` remain separate from
domain-result acceptance. Valid artifacts are not erased by a later failed
step. Neither invalid synthesis nor an orchestration error is reported as success.
Retained publication effects remain visible even if final validation fails.

Only this agent builds shared memory, in domain `software-maintenance`.
Historical memory is not current GitHub state or authority to suppress a case.

Collection is disabled by default. Finite admission controls and an operator run
ID are required for the automatic test. The queued-task cancellation guard remains
best-effort; exactly-once delivery and crash-safe recovery are not established.

## Verification and current limitations

The v0.6.0 existing-cluster run completed collection → investigation → repair →
independent review → draft PR → assessment recording/readback. See the
[release verification summary](../../README.md#v060-release-adoption-verification-2026-10-13).
This does not prove cold installation, fork portability, all possible failure
paths or duplicate suppression under concurrent publishers. No merge or
production repair deployment is part of this example.
