# TruvaG3 Domain Examples

Business-domain capability demonstrations built with the
[TruvaG3 framework](https://github.com/truvaagents/truva-g3).

This repository owns domain agents, tools, policies, fixtures, tutorials, and
solution blueprints. It does not duplicate TruvaG3's Kind cluster or shared
infrastructure installer.

## Workspace prerequisite

Clone the framework and this repository as siblings with their standard names:

```text
truvaagents/
├── truva-g3/
└── truva-g3-domain-examples/
```

Use the framework repository to create the local workspace once:

```bash
cd truva-g3/examples/travel-chat-agent
cp .env.example .env
# Add one supported AI provider key.
./setup.sh full-deploy
```

After that, each domain example installs only its own agents and tools through
its own `setup.sh`.

## Domain example catalog

| Example | Scenario | Entry point |
|---|---|---|
| [Agentic Commerce Control Tower](agentic-commerce/) | Evidence-grounded order recovery with deterministic enforcement, HITL, and a domain-neutral knowledge gateway | `cd agentic-commerce && ./setup.sh deploy` |
| [Autonomous Software Maintenance](software-maintenance/) | Scheduled observation, source-backed investigation, isolated candidate testing, independent review and draft-only PR publication; published v0.6.0 verified in existing Kind infrastructure; public/fork setup gates remain | `cd software-maintenance && ./setup.sh help`; follow its README before `./setup.sh deploy-all` |

The original proposal and supporting knowledge-foundry research are preserved
under [solution-blueprints](solution-blueprints/).

## Port registry

Ports in this repository continue after the framework example registry. Reserve
new values here before adding another service.

| Service | Port | Type |
|---|---:|---|
| `order-operations-agent` | 8394 | agent |
| `commerce-core` | 8395 | tool |
| `domain-knowledge-gateway` | 8396 | tool |
| `maintenance-evidence-tool` | 8397 | tool |
| `maintenance-agent` | 8398 | coordinator agent |
| `maintenance-source-tool` | 8399 | tool |
| `maintenance-repair-agent` | 8400 | agent |
| `maintenance-workspace-tool` | 8401 | tool |
| `maintenance-review-agent` | 8402 | agent |
| `maintenance-publication-tool` | 8403 | tool |
| `maintenance-assessment-tool` | 8404 | tool |

Shared services—including Redis, Registry Viewer, OpenTelemetry, Jaeger,
Prometheus, Grafana, ingress, and the Skills API—remain owned by the sibling
`truva-g3` checkout.

## Repository layout

```text
truva-g3-domain-examples/
├── agentic-commerce/       # runnable multi-service capability demo
├── software-maintenance/   # investigation and isolated checkout repair of the separate target
├── solution-blueprints/    # preserved architecture and research blueprints
├── AGENTS.md               # coding-assistant rules for this repository
├── .gitignore
└── README.md
```

Start with the [Agentic Commerce README](agentic-commerce/README.md), or open
its [illustrated tutorial](agentic-commerce/docs/tutorial/index.html).
