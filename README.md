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

The original proposal, implementation plan, and supporting knowledge-foundry
research are preserved under [solution-blueprints](solution-blueprints/).

## Port registry

Ports in this repository continue after the framework example registry. Reserve
new values here before adding another service.

| Service | Port | Type |
|---|---:|---|
| `order-operations-agent` | 8394 | agent |
| `commerce-core` | 8395 | tool |
| `domain-knowledge-gateway` | 8396 | tool |

Shared services—including Redis, Registry Viewer, OpenTelemetry, Jaeger,
Prometheus, Grafana, ingress, and the Skills API—remain owned by the sibling
`truva-g3` checkout.

## Repository layout

```text
truva-g3-domain-examples/
├── agentic-commerce/       # runnable multi-service capability demo
├── solution-blueprints/    # preserved research and implementation plans
├── AGENTS.md               # coding-assistant rules for this repository
├── .gitignore
└── README.md
```

Start with the [Agentic Commerce README](agentic-commerce/README.md), or open
its [illustrated tutorial](agentic-commerce/docs/tutorial/index.html).
