# Software Maintenance Development

Inherits this domain-examples repository's root instructions. Develop the
solution here; shared infrastructure and framework modules remain in the separate
TruvaG3 checkout. Do not modify the target-service repository as part of an
unrelated change here.

## Set up this example

Read [README.md](README.md) first, especially Setup, repository ownership,
profile changes, and Public-release readiness. The README is the operator guide;
this file gives coding assistants the execution order and safety boundaries.

1. **Check release and repository access before promising a working install.**
   The current module pins are published v0.6.1; use Go 1.27.2+ and the release
   build path below. Keep `GOWORK=off` for published-dependency verification and
   do not silently add Go replacements. Shared setup helpers should come from
   v0.6.1 or a reviewed compatible newer framework checkout. Pin only a fresh
   clone without local work; do not switch an existing dirty checkout merely
   to run this example. Workspace images are an explicit
   framework-development option, not an installation prerequisite. The retail
   service is intended to become public only after its maintainers complete its
   safety review and explicitly authorize publication. Until then, access is restricted to
   authorized users; report unavailable access as a release prerequisite, not
   a setup failure. Do not change repository visibility, grant access, or publish
   the service as part of setting up this example.
2. **Clone and bootstrap before deploying domain workloads.** Follow
   [Clone and bootstrap the framework once](README.md#clone-and-bootstrap-the-framework-once)
   and the framework's [Getting Started guide](https://github.com/truvaagents/truva-g3/blob/main/GETTING_STARTED.md).
   For a new workspace, clone `https://github.com/truvaagents/truva-g3.git` and
   `https://github.com/truvaagents/truva-g3-domain-examples.git` as siblings.
   Preserve existing checkouts and local changes. From their parent directory:

   ```bash
   cd truva-g3/examples/travel-chat-agent
   ./setup.sh help
   if [ ! -f .env ]; then cp .env.example .env; fi
   # Have the operator configure one supported provider key in this .env.
   # Cold start only, after configuring credentials:
   ./setup.sh full-deploy
   # After successful bootstrap and infrastructure checks:
   cd ../../../truva-g3-domain-examples/software-maintenance
   ```

   Check the intended context, shared pod readiness and Registry Viewer/Jaeger
   access as described in the README. If healthy infrastructure already exists,
   skip the bootstrap deployment and reuse it. Travel is only the maintained
   bootstrap entry point; maintenance does not require its chat workflow or
   travel tools. Do not invoke a paid travel demo just to check infrastructure.
   For a nonstandard layout, export `TRUVAG3_ROOT` to the framework checkout
   and use the actual paths. This example refuses `full-deploy`; never recreate
   the cluster or duplicate its infrastructure installer here.
3. **Choose a writable target owned by the operator.** For external users, use a
   personal/organization fork of Retail Order Service after its public release.
   Both repair branches and draft PRs belong inside that fork; the base is its
   own `main`, not `main` in `truvaagents/retail-order-service`.
   Current publication code supports same-repository PRs,
   not cross-fork PR submission to upstream. Do not request upstream write access.
   Follow [the fork preparation checklist](README.md#use-your-own-repository-for-the-demo).
   The service's hardcoded build repository identity is a known prerequisite to
   resolve before fork acceptance; changing profiles or making the repository
   public alone is insufficient. Complete and verify that portability work before
   presenting the fork-based walkthrough as ready for external users.
4. **Prepare configuration without exposing secrets.** From this directory run
   `./setup.sh help`, then `./setup.sh init-config`. Root `.env` holds shared
   non-secret settings; each of the three agents and five tools owns its own
   `.env`. Fill the identity wiring and credentials from the README. Never print
   `.env` contents or include credentials in a transcript. The travel agent's
   credentials are not inherited by these components. For legacy all-in-one
   configuration, use `migrate-config` before `init-config`; preserve its backup.
5. **Prepare the target and profiles.** Read and follow
   [PROFILE_SETUP_GUIDE.md](PROFILE_SETUP_GUIDE.md), including the worked fork
   example, canonical service-version lookup and dependency-ordered pin update.
   Review scope changes before updating hashes; a matching hash is not approval.
   Deploy the operator's target through its
   own `setup.sh`, verify `/build-info` against the selected repository/commit,
   configure the service/source/publication profiles, and propagate their pins.
   Keep `collection.enabled=false`. Confirm sandbox isolation (or explicitly
   authorized development acceptance), draft-publication permission, and the
   Viewer `software-maintenance` memory domain. Do not assert isolation merely
   because a NetworkPolicy exists. Profile and permission changes require review.
6. **Validate and deploy all eight components**, not just the coordinator:

   ```bash
   # Run from software-maintenance/ using published v0.6.1 dependencies.
   export MAINTENANCE_FRAMEWORK_SOURCE=release
   (
     set -e
     ./setup.sh validate
     ./setup.sh deploy-all
     ./setup.sh test-all
   )
   ```

   A failed prerequisite stops the sequence. `validate` is offline configuration
   validation, not proof of GitHub access or runtime health. `test-all` checks
   endpoints, skills and preflight; it is not an end-to-end repair test.
   Without a component argument, `deploy` targets evidence only.

Use `setup.sh` for every image build/deployment. Do not hand-run Docker/Podman,
Kind image loading or `kubectl apply`. `rebuild <component>` is for code/profile
changes; `rollout <component>` refreshes configuration without rebuilding.
`skills-sync` and `skills-check` are explicit strict commands; automatic skill
preparation before agent deployment is best-effort. Read `setup.sh help` for the
actual command set. Do not run cleanup as part of routine setup: workspace
cleanup can delete retained candidate and Job data.

## Execute and verify the use case

The acceptance path is **ordinary service traffic → observation feed → scheduled
collection → bounded admission → agent investigation → verified draft PR**.
Manual task submission is not proof of that automatic path.

1. Obtain the operator's authorization for paid model calls and draft creation
   in the selected repository. Choose a fresh admission run ID and finite quotas
   in the coordinator `.env`; distinguish full runs from individual model calls.
   Reuse neither an exhausted allowance nor a historical authorization.
2. Follow [Run an automatic investigation](README.md#run-an-automatic-investigation):
   enable collection only after setup checks pass, rebuild assessment and
   coordinator with identical profile bytes, and run `./setup.sh preflight`.
   Treat enabling collection as capable of starting paid work immediately.
3. Generate ordinary requests through the target service, using the documented
   `diagnostics/automatic-pilot` traffic helper and fresh order IDs. Supply its
   order API token privately. Successful requests are sampled, so one order
   may not produce a case. Observe admission before generating more traffic;
   do not raise quotas or change the service to force a desired diagnosis.
4. Monitor to a terminal case outcome. Inspect all relevant Registry Viewer
   executions, prompts, skills, memory, logs and Jaeger traces. Verify the actual
   candidate diff, baseline failure, candidate test results, independent review,
   publication receipt, and draft PR in the operator's repository. If no repair
   is justified or the run fails, report it accurately; do not call that a
   successful PR-creation test. No automatic merge or repair deployment.
5. With separate run authorization, leave the draft open and repeat a matching
   workload. Verify fresh PR discovery and that no duplicate is created.
   A recalled memory claim alone does not establish current GitHub state.
6. Stop admitting new work within the agreed budget, let active work finish,
   and disable collection following the README. Preserve required evidence
   before TTL expiry. Closing PRs, deleting branches, resetting Redis, or
   changing target code is separate work requiring explicit scope.

Record what was implemented, locally checked, deployed and live-verified
separately. For setup-only edits, run `bash -n` on the shell scripts and
`node setup.test.mjs`; these tests use synthetic credentials and no paid calls.
Before live verification, review the actual changes against the guides below.
Do not commit documentation without human sign-off.

## Required agent and tool patterns

- Follow the `my-async-agent`, `my-streaming-agent`, and `my-tool` bootstrap
  processes for the corresponding component. Preserve their review checkpoints.
  The scaffolds are reference inputs, not files to edit.
- Read the Agent Development Guide or Tool Development Guide before changing
  the corresponding Go component. Read the affected module architecture docs,
  Framework Design Principles, Distributed Tracing Guide, and Logging
  Implementation Guide for the APIs being used.
- Use `devops-chat-agent` as the reference for skills and a public,
  non-streaming agent capability. An HTTP JSON agent does not require SSE.
- Initialize and manage the real orchestration harness. Submit the task or
  request through its normal planning entry point with discovered capabilities.
  Do not substitute manually assembled plans, fixed self-call stages, or
  direct model calls wrapped in an execution graph for autonomous agents.
- Keep domain guidance in explicitly bound skill packages, following the
  agent-owned skills publication and setup conventions in the framework's
  `examples/AGENTS.md`.
  Do not replace a hardcoded Go workflow with equally rigid skill instructions.
- Keep tools passive and discoverable. Authorization, idempotency, sandbox
  boundaries, verification evidence, and draft-only publication restrictions
  remain deterministic controls even when planning is autonomous.
- Use framework-managed lifecycle and background work, including
  `core.Runnable` where applicable. Do not fabricate execution or debug records.

## Evidence and delivery

- Use real services and real model calls for end-to-end acceptance. Local Kind
  verification must include the Registry Viewer in a real browser, plus logs
  and traces; an HTTP success alone is not proof of the complete solution.
- Keep runtime packages, prompts, images, and agent-accessible source free of
  predetermined diagnoses, case-specific conclusions, or proposed fixes that
  were not established from attributable runtime evidence.
- Never copy `.env` files into tracked assets or expose their contents in
  diagnostics. Commit only credential-free configuration examples.
- Deploy only through the solution's setup script. Preserve
  existing workloads until an explicit deployment or replacement is in scope.
- Report implementation, deployment, and verification status separately.
