# Agentic Commerce Control Tower tutorial

## Start here

The primary tutorial is the illustrated, interactive
[beginner tutorial](index.html). It starts with the business process and assumes
no knowledge of AI agents, tools, Kubernetes, Redis, Go, or graph databases.

To view it as a web page from the repository root, run:

```bash
python3 -m http.server 8088 --directory agentic-commerce/docs/tutorial
```

Then open <http://127.0.0.1:8088/>. You can also open `index.html` directly in a
browser. No package installation or build step is required.

The Markdown chapters below are the detailed reference version. Use them after
the web tutorial when you want more implementation detail.

This tutorial explains the capability demo in simple English. It is meant for a
reader who wants to understand the business idea before reading Go code,
Kubernetes manifests, or the larger solution blueprint.

The tutorial uses two kinds of examples:

- small conceptual examples that explain why a component exists;
- real execution IDs, trace IDs, and outcomes captured from the local Kind
  cluster on August 22, 2026.

Registry and Jaeger retain data for a limited time. The IDs may eventually
disappear from the live dashboards, so each chapter also includes a sanitized
summary of what was observed. Signed evidence receipts, decision tokens,
approval proofs, and provider credentials are deliberately not copied here.

## Recommended reading order

| Part | Document | Main question answered |
|---:|---|---|
| 1 | [The idea in plain English](01-the-idea.md) | What problem are we demonstrating? |
| 2 | [The parts we built](02-the-parts.md) | What runs in Kubernetes, and what does each part own? |
| 3 | [From evidence to safe action](03-evidence-to-action.md) | How do search, graph relations, the LLM, policy, and commands work together? |
| 4 | [Run the demo and follow one order](04-first-live-run.md) | How is a run triggered, and what should I see? |
| 5 | [Progressive decision scenarios](05-decision-scenarios.md) | How do success, denial, abstention, conflict, and duplicates differ? |
| 6 | [Human approval and change safety](06-human-approval.md) | What happens before, during, and after HITL? |
| 7 | [Using the pattern in another domain](07-domain-portability.md) | Which parts are domain-agnostic, and which must change? |
| 8 | [Reading the live evidence](08-observability.md) | How do I verify a claim in the task API, Registry, Jaeger, and business state? |
| 9 | [Domain Knowledge Gateway X-ray](09-domain-knowledge-gateway-xray.md) | What data is loaded, which live calls did the agent make, and how do direct gateway queries behave? |

You can read Parts 1–3 without running anything. Parts 4–9 are the operator's
guide to the deployed demo and its captured evidence.

## A small glossary

| Term | Meaning in this demo |
|---|---|
| Agent | The workload that interprets an order-risk task and coordinates tools. |
| Tool | A separately running service that exposes named, schema-backed capabilities. |
| Pod | One Kubernetes instance of a deployed service. A pod runs the service's Go process. |
| Worker | A lightweight Go goroutine inside the agent process that repeatedly takes tasks from Redis. It is not a pod or a dedicated OS thread. |
| Skill | A compact operating procedure that tells the agent how to do this kind of work safely. It is not the policy corpus. |
| Domain pack | A versioned bundle of normalized policy sections, sources, relations, aliases, and retrieval tests. |
| Evidence section | One exact, cited unit of policy knowledge from a domain pack. |
| Evidence receipt | A short-lived signed statement that an exact section was read in a specific scope and release. |
| Deterministic policy | Ordinary Go logic that returns the same decision for the same validated facts and evidence. |
| HITL | Human in the loop: execution pauses at a persisted checkpoint until a person approves, rejects, or aborts it. |
| Registry | The execution record: plans, tool steps, LLM interactions, skills, and checkpoints. |
| Jaeger | The distributed trace showing which services actually communicated. |

## Sources of truth

This tutorial is an explanation layer. The implementation remains authoritative:

- [Runnable example overview](../../README.md)
- [Tool capability contracts](../../contracts/tool-capabilities.md)
- [Scenario assertion manifest](../../scenarios/expected-results.json)
- [Manual scenario runbook](../../scenarios/README.md)
- [Implementation plan](../../../solution-blueprints/AGENTIC_COMMERCE_IMPLEMENTATION_PLAN.md)
- [Original proposal and preserved research](../../../solution-blueprints/AGENTIC_COMMERCE_CONTROL_TOWER.md)
- [Detailed observability audit](../audits/OBSERVABILITY_AUDIT_AGENTIC_COMMERCE_2026-08-21.md)

Start with the [illustrated beginner tutorial](index.html), then continue with
[Part 1: The idea in plain English](01-the-idea.md) when you need more detail.
