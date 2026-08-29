# Commerce Core Tool

This tool provides six discoverable deterministic capabilities over eight
synthetic orders: state, bounded options, evidence-backed eligibility,
idempotent execution, verification, and a local notification audit record. It
also exposes two development-only capabilities hidden from LLM discovery:
`process_normal_order` proves the straight-through no-agent/no-LLM path, and
`manage_demo_state` resets or changes allow-listed synthetic state. It never
calls an external commerce system or sends a real notification.

Policy evaluation accepts only signed exact-read receipts from the gateway.
Execution rereads current state, reruns policy, binds idempotency keys to one
decision, requires runtime proof for approval-class decisions, and verifies
action-specific fulfillment, payment, inventory, or carrier-claim state.

Use the parent [`setup.sh`](../setup.sh) for every build and deployment action.
This directory was copied from `truva-g3/examples/my-tool`; the template was not modified.
