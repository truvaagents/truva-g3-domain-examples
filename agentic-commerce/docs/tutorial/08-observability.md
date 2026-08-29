# Part 8: Reading the live evidence

A convincing live test needs more than a final sentence from an agent. This demo
checks four views of the same work.

## The four proof layers

| Layer | What it proves | What it cannot prove alone |
|---|---|---|
| Task API | The caller-visible business status and terminal outcome | Which tool calls actually happened |
| Registry | Plans, results, skills, LLM records, parameters, and checkpoints | The current external business state by itself |
| Jaeger | The actual cross-service call path and HTTP/error spans | Whether the business postcondition is semantically correct |
| Commerce state | Whether an order changed, how many times, and whether expected state exists | Why the agent and policy selected that outcome |

For HITL, the live checkpoint API is a fifth authoritative view because Registry
currently keeps interruption and resume snapshots separately.

## Start in Registry Viewer

Open [http://registry.localhost/](http://registry.localhost/) and select the
Execution DAG view. Search for a full `request_id`, for example:

```text
order-operations-1787446133236523689
```

For an ordinary automatic run, inspect:

1. the active skill name and version;
2. each phase plan;
3. capability names and dependencies;
4. resolved parameters, especially order ID and evidence scope;
5. exact-read results and release identity;
6. the deterministic policy outcome;
7. execute, verify, and notify results;
8. the trace ID.

The live automatic-recovery record shows skill
`order-operations/evidence-grounded-recovery` version 8, one activation, and
verified skill loads at the planning boundaries. This is the evidence that the
procedure was injected. The domain corpus does not appear as one giant skill
prompt; selected evidence appears in the gateway tool results.

## Use the Registry API safely

The unified endpoint is:

```text
GET /api/executions/<request-id>/unified
```

Prefer a narrow projection instead of copying the full response, which may
contain complete prompts, policy excerpts, signed receipts, and decision
tokens:

```bash
request_id='order-operations-1787446133236523689'

curl -sS \
  "http://registry.localhost/api/executions/${request_id}/unified" |
  jq '{
    request_id,
    trace_id,
    success,
    skills: [.skills.candidates[]? |
      {name:.ref.name,version:.resolved.version,status}],
    activation_count: (.skills.activations | length),
    capabilities: [.result.steps[]?.capability],
    checkpoints: [.checkpoints[]? | {checkpoint_id,status}]
  }'
```

To inspect deterministic decisions without displaying receipts or tokens:

```bash
curl -sS \
  "http://registry.localhost/api/executions/${request_id}/unified" |
  jq '[.result.steps[]? |
    select(.capability == "evaluate_recovery_policy") |
    ((.response | fromjson).data |
      {outcome,approval_required,order_id,option_id,order_version,
       release_id,evidence_section_ids})]'
```

## Follow the trace in Jaeger

Open `http://jaeger.localhost/trace/<trace-id>`. For the automatic run:

[Open the captured automatic-recovery trace](http://jaeger.localhost/trace/f3952cb932773e34bedaff89cd2df4b5).

The trace should connect:

```text
order-operations-agent
    -> commerce-core
    -> domain-knowledge-gateway
    -> commerce-core
```

The Registry proves what the orchestrator recorded; the tool-server spans prove
the downstream services actually received those calls.

A safe summary query is:

```bash
trace_id='f3952cb932773e34bedaff89cd2df4b5'

curl -sS "http://jaeger.localhost/api/traces/${trace_id}" |
  jq '{
    trace_id: .data[0].traceID,
    span_count: (.data[0].spans | length),
    services: ([.data[0].processes[]?.serviceName] | unique),
    error_span_count: ([.data[0].spans[] |
      select(any(.tags[]?; .key == "error" and .value == true))] | length)
  }'
```

An error span is not always a failed test. The stale policy and state scenarios
deliberately return HTTP 409; their client, server, and governed-step error spans
are evidence that the mutation guard fired.

## Confirm business state

For paired manual tests, port-forward Commerce Core and use its hidden control
to read state:

```bash
kubectl port-forward -n truvag3-examples \
  service/commerce-core-service 8395:80
```

```bash
curl -sS -X POST \
  http://127.0.0.1:8395/api/capabilities/manage_demo_state \
  -H 'Content-Type: application/json' \
  -d '{"action":"status","order_id":"ORD-1003"}' |
  jq '{action:.data.action,order:.data.order}'
```

For a successful replacement, check version 2 and the expected recovery record.
For denial or abstention, check version 1 and the absence of a recovery. For the
state-race scenario, version 2 is expected only because the explicit external
test control changed inventory; the recovery record must still be absent.

## Captured evidence catalog

| Behavior | Execution or trace to start with | Expected proof |
|---|---|---|
| Automatic recovery | execution `order-operations-1787446133236523689`; trace `f3952cb932773e34bedaff89cd2df4b5` | allowed, one execute, verified, notified |
| Approval pending | execution `order-operations-1787446198299432013`; checkpoint `cp-d038488b-15be-48` | checkpoint before execute, no mutation |
| Approved recovery | initial `order-operations-1787431818524531555`; resume `order-operations-1787431933621096526` | completed checkpoint and verified mutation |
| Denial | execution `order-operations-1787432041838325142` | denied, no execute span |
| Insufficient evidence | execution `order-operations-1787439551802679081` | missing facts recorded, no mutation |
| Conflicting evidence | execution `order-operations-1787439074847452187` | both conflict sections read, no mutation |
| Duplicate event | execution `order-operations-1787432364863440680` | one accepted task and one command |
| State changed during HITL | resume `order-operations-1787455319493659830`; trace `c05ae0bfcdf2b907eb7b565b63561ce7` | guarded HTTP 409, no recovery |
| Policy changed during HITL | resume `order-operations-1787455389634165126`; trace `80f5d8d9cede1f8ae010d0aba248e871` | guarded HTTP 409, no recovery |
| Alternate-domain gateway | traces `5eaa22e189a98203821891b6d8648a79`, `d1bb00a6b64dee8e15833dd4ed9a574e`, `8436859d5cfc213db601f98aefe722f4` | same pod serves IT release, search, and relation |

## What counted as a pass

A scenario was declared passed only when:

- task and deterministic terminal outcomes matched the scenario contract;
- Registry showed the expected skills, evidence, decision, and allowed steps;
- Jaeger showed the participating service calls and only expected errors;
- Commerce state showed the expected mutation or required absence of mutation;
- signed receipts, approval proofs, control keys, and provider credentials were
  absent from persisted or emitted observability data.

The final privacy scans found no proof-shaped values in the new Registry,
checkpoint, Jaeger, or current workload-log records.

## Known Registry Viewer limitations

The application behavior is complete for the agreed demo scope, but the
observability UI has known discrepancies. Do not use these fields as proof until
their audit findings are fixed:

- displayed total duration can double-count overlapping LLM time;
- backend DAG step durations can appear as zero;
- a multi-phase DAG can imply impossible cross-phase parallelism;
- the asynchronous submission trace is not automatically linked to the worker
  execution trace;
- an HITL interruption and its resume are separate snapshots and need a manual
  `original_request_id`/checkpoint join;
- a unified checkpoint projection can omit prior-phase `implicit_deps`;
- the LLM Calls view shows a raw synthesis draft, not the governed final
  application response;
- direct alternate-domain gateway calls create Jaeger spans but no Registry
  orchestration record.

These issues are recorded with reproduction evidence and acceptance criteria in
the [full observability audit](../audits/OBSERVABILITY_AUDIT_AGENTIC_COMMERCE_2026-08-21.md).

## The honest conclusion

The live tests prove the bounded functional behaviors and their enforcement in
the local Kind environment. They do not prove production scalability, complete
policy coverage, legal correctness, durable recovery after every infrastructure
failure, or the deferred Registry Viewer fixes.

Return to the [tutorial index](README.md), or continue with the
[implementation plan](../../../solution-blueprints/AGENTIC_COMMERCE_IMPLEMENTATION_PLAN.md)
for the production-shaped work that remains deliberately deferred.
