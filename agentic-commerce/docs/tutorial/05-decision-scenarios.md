# Part 5: Progressive decision scenarios

The scenarios are easiest to understand as a ladder. Each one adds a failure or
safety condition to the previous one.

## The outcome ladder

| Level | Scenario | Authoritative result | Mutation? |
|---:|---|---|---|
| 0 | Normal order | `normal_order_confirmed` | No; existing good state is confirmed |
| 1 | Automatic recovery | `recovered` / `allowed` | Yes, exactly once |
| 2 | Approval required | `approval_required` | No while pending |
| 3 | Denied option | `denied` | No |
| 4 | Missing facts | `insufficient_evidence` | No |
| 5 | Unresolved policy conflict | `conflicting_evidence` | No |
| 6 | Duplicate delivery | First run proceeds; later copies are `duplicate` | First accepted event mutates at most once |

Human approval and stale-basis cases are covered separately in Part 6.

## Level 0: Normal order

`ORD-1000` is confirmed, paid, and already has reserved inventory. The setup
driver calls `process_normal_order` directly.

Captured result:

```json
{
  "terminal_outcome": "normal_order_confirmed",
  "order_id": "ORD-1000",
  "agent_invoked": false,
  "llm_invoked": false
}
```

This is a useful control case. It proves the design does not insert AI into a
transaction that already has a deterministic happy path.

## Level 1: Automatic recovery

`ORD-1003` is a low-value, unrestricted missing order with replacement
inventory. Exact enterprise evidence authorizes a bounded same-SKU replacement.

Captured proof:

- task `order-event-demo-automatic-recovery-1787446133-56209`;
- execution `order-operations-1787446133236523689`;
- trace `f3952cb932773e34bedaff89cd2df4b5`;
- terminal `recovered`, policy `allowed`;
- order advanced once from version 1 to version 2;
- command, verification, and notification all completed.

Part 4 walks through this run in detail.

## Level 2: Approval required

`ORD-1004` is a higher-value missing shipment. The action is eligible but not
within autonomous authority.

The default run ended at checkpoint `cp-d038488b-15be-48` with
`approval_required`. No command was sent and no order mutation occurred. This
is not an incomplete automatic recovery; it is the intended terminal state for
the unattended demo.

Part 6 follows approved, rejected, expired, and stale approval paths.

## Level 3: Deterministic denial

`ORD-1005` contains a lithium-battery air-transport restriction. The operator
constrained the request to `same-sku-replacement`, so the agent could not hide
the denial by selecting another option.

A paired live run recorded:

| Evidence | Value |
|---|---|
| Task | `order-event-manual-denied-20260822-d1` |
| Execution | `order-operations-1787432041838325142` |
| Trace | `92f9a8a46fc8acfb587ee4f12de92668` |
| Result | `denied` / `denied` |
| Reason | `restricted merchandise blocks replacement: lithium_battery_air_transport` |

Registry contained eight successful read/evaluation steps and no execute,
verify, or notification step. `ORD-1005` remained version 1 with no recovery
record. Jaeger had no mutation operation.

This is stronger than an LLM saying “I would not do it.” The executable plan
was deterministically reduced to no steps after the denial.

## Level 4: Insufficient evidence

The carrier-claim option for `ORD-1004` requires payer, lane, ship-date, and
exclusion facts. The scenario deliberately removes them.

A passing live run recorded:

| Evidence | Value |
|---|---|
| Task | `order-event-live-insufficient-evidence-20260822-02` |
| Execution | `order-operations-1787439551802679081` |
| Trace | `d1cf189f6f71e0a3a4f09f60809bc7e0` |
| Exact evidence | `fedex-guarantee-service-scope` |
| Result | `insufficient_evidence` / `insufficient_evidence` |

The agent still called the deterministic evaluator. That matters because the
evaluator—not the model—recorded exactly which required facts were absent.
There was no execute, verify, notification, or order mutation.

## Level 5: Conflicting evidence

`ORD-1006` is scoped to a marketplace where two equally applicable replacement
rules disagree. A third evidence section describes precedence, but it cannot
resolve this pair.

A passing live run recorded:

| Evidence | Value |
|---|---|
| Task | `order-event-live-conflicting-evidence-20260822-02` |
| Execution | `order-operations-1787439074847452187` |
| Trace | `dbf7accb62950449cfce2ea032ceee61` |
| Exact evidence | `demo-replacement-conflict-allow`, `demo-replacement-conflict-deny`, `demo-evidence-precedence` |
| Result | `conflicting_evidence` / `conflicting_evidence` |

Registry showed the exact reads and evaluation but no execution chain.
`ORD-1006` remained version 1. Jaeger contained 111 spans across the three
services, with no error or mutation span.

This case also exposed and corrected an important grounding issue during
development: the model initially supplied a made-up evidence channel. The plan
hook now binds the search channel to authoritative order or selected-option
state.

## Level 6: Duplicate event

The same event was delivered three times:

1. the first delivery returned HTTP 202 and started work;
2. the immediate duplicate returned HTTP 409 with `task_status=running`;
3. the post-completion duplicate returned HTTP 409 with
   `task_status=completed`.

Captured proof:

- event `manual-duplicate-20260822-u1`;
- task `order-event-manual-duplicate-20260822-u1`;
- execution `order-operations-1787432364863440680`;
- trace `86950528276054615c029fda63d0dd2f`;
- exactly one command, verification, and notification;
- one order transition from version 1 to version 2.

There are two protections here:

- webhook deduplication prevents a second active or completed task for the same
  event ID;
- command idempotency prevents the same authorized business command from
  applying its mutation twice.

## Additional policy-grounding fixtures

Two individually runnable fixtures explore different source types without
adding another outcome class:

- `late-unshipped` constrains `ORD-1001` to `cancel-and-refund` after a promised
  ship date was missed without customer consent. It exercises FTC/eCFR delay,
  consent, cancellation, and prompt-refund evidence.
- `usps-express-late` constrains `ORD-1003` to `carrier-service-claim`. It
  exercises USPS eligibility, mailing-receipt, tracking, and filing-window
  evidence rather than the merchant replacement policy used in Part 4.

Run either through the same webhook/task path:

```bash
./setup.sh demo late-unshipped
./setup.sh demo usps-express-late
```

The original observability audit used the USPS fixture. It captured task
`order-event-demo-usps-express-late-1787325104`, execution
`order-operations-1787325104910014933`, execution trace
`a297affcd9f89557ec1552ca6c4c469a`, and trigger trace
`36be3fe5ef27ebaf134effbf1a0948ad`. The planned steps, runtime results, Registry
DAG steps, and traced tool-server steps contained the same nine step IDs. All
nine tool calls returned HTTP 200, and the run included command, verification,
and notification records.

## Retrieval tests behind the scenarios

The commerce pack contains seven reviewed retrieval gold cases. They cover a
late unshipped order, electronics return exceptions, USPS Express eligibility,
missing-merchandise refund, high-value replacement approval, a FedEx hard
negative, and the marketplace conflict.

Run them with the deterministic Commerce tests:

```bash
./setup.sh eval
```

These tests assert expected and forbidden evidence sections. They measure the
retrieval contract; a fluent LLM answer is not treated as an accuracy test.

The machine-readable scenario outcomes are in
[`expected-results.json`](../../scenarios/expected-results.json). Operator-paired
steps are in the [manual scenario runbook](../../scenarios/README.md).

Next: [Part 6: Human approval and change safety](06-human-approval.md).
