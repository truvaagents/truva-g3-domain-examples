# Order Operations Agent

This is the single agent in the Agentic Commerce capability demo. It receives
ordinary HTTP order-risk events, persists Redis tasks, binds the
`order-operations/evidence-grounded-recovery` skill, coordinates discovered
tools, and exposes framework HITL endpoints for the synthetic mutation. The
order-risk webhook is registered as an internal typed capability, while task
and HITL control routes remain infrastructure handlers.

Framework initialization owns discovery and starts one lifecycle-managed
runtime. That runtime initializes the orchestrator catalog before it starts the
worker pool, and `/ready` remains unavailable until this boundary succeeds.
Approved resume requests inject a checkpoint-bound proof understood by Commerce
Core and write the resumed outcome back to the original task; failed event
tasks can be safely requeued under the same deduplication identity.

Use the parent [`setup.sh`](../setup.sh) for every build and deployment action.
This directory was bootstrapped from `truva-g3/examples/my-async-agent`; the template was
not modified and this component deliberately has no child deployment script.
