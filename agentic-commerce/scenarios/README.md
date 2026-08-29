# Manual scenario verification

The scenario JSON files are webhook inputs. `expected-results.json` is the
machine-readable assertion manifest and must never be posted as an event.

The following five live tests are intentionally reserved for an operator-paired
session. Their implementation and deterministic component tests are complete,
but they must not be triggered as part of setup, smoke, or an unattended demo:

- `conflicting-evidence`
- `policy-changed-during-hitl`
- `policy-changed-reapproval`
- `state-changed-during-hitl`
- `alternate-domain-switching`

`insufficient-evidence` was first verified in an operator-paired session and is
now safe to include in the default demonstration as its abstention path.

## Manual control access

The agent is available through `http://agentic-commerce.localhost`. The two
development-only controls are hidden from LLM discovery and have no ingress;
open local forwards before a paired live test:

```bash
kubectl port-forward -n truvag3-examples service/commerce-core-service 8395:80
kubectl port-forward -n truvag3-examples service/domain-knowledge-gateway-service 8396:80
```

Reset both mutable demo selectors before and after each reserved test:

```bash
curl -sS -X POST http://127.0.0.1:8395/api/capabilities/manage_demo_state \
  -H 'Content-Type: application/json' -d '{"action":"reset"}'
curl -sS -X POST http://127.0.0.1:8396/api/capabilities/manage_demo_domain_pack \
  -H 'Content-Type: application/json' -d '{"action":"activate","pack_name":"commerce-demo"}'
```

For each webhook fixture, replace its checked-in `event_id` with a unique value,
POST it to `/webhook/order-risk`, and poll the returned `status_url`. Preserve
the event response, final task response, Registry execution JSON and DAG, Jaeger
trace JSON, and final Commerce order response as audit evidence.

## Conflicting evidence

Submit `conflicting-evidence.json`. The final task must report
`terminal_outcome=conflicting_evidence` and
`policy_outcome=conflicting_evidence`. Registry must show exact reads of both
`demo-replacement-conflict-allow` and `demo-replacement-conflict-deny`, followed
by `evaluate_recovery_policy`; it must show no execute, verify, or notification
step. `ORD-1006` must remain version 1 with no recovery record.

## Insufficient evidence

Submit `insufficient-evidence.json`. The final task must report
`terminal_outcome=insufficient_evidence` and
`policy_outcome=insufficient_evidence`, not a generic `needs_review`. The agent
must still call the deterministic evaluator with the scoped FedEx evidence so
Commerce Core records which required order facts are absent. No command,
verification, notification, or order mutation is allowed.

## Policy changed during HITL

Submit `policy-changed-during-hitl.json` and wait until its execute step has a
pending checkpoint. Before approving, activate both halves of the changed
policy basis:

```bash
curl -sS -X POST http://127.0.0.1:8396/api/capabilities/manage_demo_domain_pack \
  -H 'Content-Type: application/json' \
  -d '{"action":"activate","pack_name":"commerce-demo-policy-changed"}'
curl -sS -X POST http://127.0.0.1:8395/api/capabilities/manage_demo_state \
  -H 'Content-Type: application/json' \
  -d '{"action":"activate_policy_release","release_id":"commerce-us-2026-08-20-policy-change"}'
```

Approve the checkpoint, then resume it. Resume must return HTTP 409 with
`status=replan_required`,
`terminal_outcome=policy_changed_during_hitl`, and
`retry_same_event=true`. The original task becomes safely retryable. The old
approval checkpoint is aborted, no recovery command is stored, and `ORD-1004`
remains version 1 with no recovery record. Reposting the same event must create
a fresh decision against the active release rather than reuse the old approval.

`policy-changed-reapproval.json` uses `ORD-1007` at 349 USD to prove the
stronger continuation: after the old basis is rejected and the same event is
reposted, the changed 300 USD threshold still requires approval, so a new
decision token and checkpoint must be created. The old checkpoint must never
authorize the replanned command.

## State changed during HITL

Submit `state-changed-during-hitl.json` and wait for its checkpoint. Before
approval, apply the explicit external state change:

```bash
curl -sS -X POST http://127.0.0.1:8395/api/capabilities/manage_demo_state \
  -H 'Content-Type: application/json' \
  -d '{"action":"set_inventory_unavailable","order_id":"ORD-1004","expected_version":1}'
```

Approve and resume. Resume must return HTTP 409 with
`status=replan_required`, `terminal_outcome=state_changed_during_hitl`, and
`retry_same_event=true`. The recovery command must not execute. The control
change leaves `ORD-1004` at version 2 with unavailable inventory and no recovery
record. Reposting the same event must plan from that current state.

If a short-lived decision also expires while a policy or order-state race is
already true, Commerce reports the concrete changed basis as the primary replan
reason. Expiry remains enforced before evidence reuse or mutation; this
precedence keeps the paired policy/state demonstrations deterministic without
weakening their fail-closed behavior.

## Alternate-domain switching

Activate the IT pack without rebuilding or restarting the gateway:

```bash
curl -sS -X POST http://127.0.0.1:8396/api/capabilities/manage_demo_domain_pack \
  -H 'Content-Type: application/json' \
  -d '{"action":"activate","pack_name":"it-access-demo"}'
curl -sS -X POST http://127.0.0.1:8396/api/capabilities/get_release_info \
  -H 'Content-Type: application/json' -d '{}'
curl -sS -X POST http://127.0.0.1:8396/api/capabilities/search_evidence \
  -H 'Content-Type: application/json' \
  -d '{"query":"departed employee access revocation deadline","tenant_id":"it-demo","jurisdiction":"global","channel":"internal","as_of":"2026-08-20T18:00:00Z","limit":3}'
curl -sS -X POST http://127.0.0.1:8396/api/capabilities/traverse_relations \
  -H 'Content-Type: application/json' \
  -d '{"section_ids":["departed-user-revocation"],"tenant_id":"it-demo","jurisdiction":"global","channel":"internal","as_of":"2026-08-20T18:00:00Z","max_depth":1,"limit":5}'
```

The same running gateway must report release `it-access-2026-08-20`, domain
`it-access-governance`, rank `departed-user-revocation` first, and traverse to
`offboarding-verification`. Registry and Jaeger must retain the IT release,
tenant, section, and relation identifiers. Reactivate `commerce-demo` after the
test so the Commerce agent and deterministic policy evaluator share one release.

## Observability pass criteria

For every reserved run, verify all of the following before declaring it passed:

- Registry request, task, skill version, phase DAG, resolved parameters, release
  identity, exact-section receipts, deterministic outcome, and checkpoint state
  agree with the service responses.
- Jaeger has the same request and trace identities and includes every expected
  tool call. Any error span is explained by the expected guarded outcome.
- Commerce state proves either the expected verified mutation or the required
  absence of a recovery mutation.
- Full documents, approval proofs, control keys, and provider credentials do
  not appear in Registry, traces, or logs.
