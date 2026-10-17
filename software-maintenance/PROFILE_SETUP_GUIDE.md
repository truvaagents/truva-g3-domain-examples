# Prepare profiles for your Retail Order Service fork

This guide explains the configuration files that connect the maintenance agents
to **your service and your GitHub repository**. Start with the
[README setup](README.md#setup) for framework bootstrap and credentials, then
use this guide before deploying the complete maintenance solution.

This walkthrough covers a fork retaining the supplied Retail Order Service's Go
layout, contract and evidence APIs. A different service may need endpoint/evidence
adapters, assessment rules and sandbox capabilities as well as new profiles; see
[Extensibility and remaining boundaries](README.md#extensibility-and-remaining-boundaries).

**Current prerequisite:** the target service remains private until its maintainers
approve public release. Its repository-identity support is published on `main`
in commit `c714bf1`; use a target-service revision containing that change before following
the [fork checklist](README.md#use-your-own-repository-for-the-demo). Older
revisions still hardcode the upstream identity. The walkthrough does not change
repository visibility or claim that full user-owned-fork acceptance is complete.

## Contents

1. [Locate the supplied profile files](#locate-the-supplied-profile-files)
2. [What is a profile?](#what-is-a-profile)
3. [Which files do I change?](#which-files-do-i-change)
4. [Review the configuration fields](#review-the-configuration-fields)
5. [Understand the fingerprints](#understand-the-fingerprints)
6. [Worked example: your own fork](#worked-example-your-own-fork)
7. [Which components need rebuilding?](#which-components-need-rebuilding)
8. [Updating profiles later](#updating-profiles-later)
9. [Troubleshooting](#troubleshooting)

## Locate the supplied profile files

Start in the **domain-examples repository root**, then enter this example:

```bash
cd software-maintenance
ls profiles/retail-order-*.json
```

The seven starting configurations are already checked into
[`software-maintenance/profiles/`](profiles/):

```text
software-maintenance/
└── profiles/
    ├── retail-order-service.json
    ├── retail-order-source.json
    ├── retail-order-workspace.json
    ├── retail-order-repair.json
    ├── retail-order-review.json
    ├── retail-order-publication.json
    └── retail-order-maintenance.json
```

Edit these existing JSON files after taking the backup in step 1 below. They are
the profiles consumed by the supplied deployments. `./setup.sh init-config`
creates missing root/component `.env` files from `.env.example`; it does not
create or reset profiles. `profiles/diagnostics/expanded-budget.env` is an optional
agent-budget overlay, separate from these seven policy profiles.

All remaining paths and commands in this guide are relative to
`truva-g3-domain-examples/software-maintenance/`. Use the same Bash session for
the worked example so its `service_version` and backup variables remain available.

## What is a profile?

A profile is an operator-owned JSON file that tells a component what it is
allowed to work on. Think of it as a signed-off instruction sheet:

- “Read source from this repository.”
- “Use this document as the functional contract.”
- “Run these permitted checks in the sandbox.”
- “Open draft PRs in this repository, against this branch.”

Profiles do **not** prescribe the model's sequence of tool calls or supply a
diagnosis. The model chooses actions using discovered capabilities and skills.
The tools use profiles to enforce scope and evidence checks.

Profiles are not secret stores. Keep tokens and API keys in the appropriate
component `.env` files. The supplied container images include the profiles, so a
profile edit requires an image rebuild; configuration-only `rollout` is not enough.

## Which files do I change?

All seven files are under `profiles/`. Follow the filename links to inspect the
complete starting configurations; later references to a role such as `source`
mean its corresponding file below.

| File | Plain-English job | Typical change for a fork |
| --- | --- | --- |
| [`retail-order-service.json`](profiles/retail-order-service.json) | Where is the running service, and where are its requirements? | GitHub repository name, contract commit, and service URL if it changed |
| [`retail-order-source.json`](profiles/retail-order-source.json) | Which repository and refs may the source tool read? | GitHub owner/repository and base branch |
| [`retail-order-workspace.json`](profiles/retail-order-workspace.json) | How may code be staged and tested? | Usually unchanged for the same Go service; review runner, storage and isolation policy |
| [`retail-order-repair.json`](profiles/retail-order-repair.json) | What evidence and workspace policy may the repair agent use? | Update prerequisite fingerprints |
| [`retail-order-review.json`](profiles/retail-order-review.json) | What evidence must an independent review use? | Update prerequisite fingerprints |
| [`retail-order-publication.json`](profiles/retail-order-publication.json) | Where may a draft PR be created? | GitHub owner/repository, base branch, bot author and prerequisite fingerprints |
| [`retail-order-maintenance.json`](profiles/retail-order-maintenance.json) | How does the coordinator collect and assess observations? | Update prerequisite fingerprints; keep collection disabled during setup |

`repository_id: "retail-order-service"` is a **logical name**, not a GitHub URL.
You can retain it, and the existing profile IDs, for this single-service example.
The GitHub address is carried by `owner`/`repository` or `repository.name`.
Changing logical IDs requires changing all references to them, so it is not
needed just to use a fork.

The supplied Kubernetes manifests set profile paths explicitly. Changing a local
`*_PROFILE_FILE` or `*_PROFILES_FILE` environment variable alone will not redirect
a deployed component. Use the supplied files and rebuild for this walkthrough;
see [Configure credentials and policy](README.md#configure-credentials-and-policy)
for local-run versus deployment behavior.

## Review the configuration fields

For a fork with the same service behavior, retain `schema_version`, logical IDs,
caller lists, denied paths and limits unless you have reviewed a reason to change
them. Make edits in the worked example's order: service first, then obtain its
version, then edit other profiles and propagate pins. The table below is a
configuration checklist, not an instruction to edit every file immediately.

| Profile and fields | How to configure them |
| --- | --- |
| Service: `repository`, `contract_references` | Name your fork and default branch. Pin the reviewed requirements document to an available full commit SHA and repository-relative path. The contract must apply to the deployed release. |
| Service: `upstream.base_url`, `upstream.endpoints` | Keep the supplied in-cluster address and endpoint paths when the service topology/API is unchanged. A different address must be reachable from the evidence pod; changing paths does not adapt an incompatible response schema. |
| Source: `owner`, `repository`, `default_ref`, `allowed_named_refs` | Point all four at the intended fork and permitted branches. Keep `main` for this walkthrough; supplied smoke probes also use it. |
| Workspace: `runner_image`, `workspace_pvc` | Retain the digest-pinned Go image and `maintenance-workspaces` PVC for the supplied deployment. A replacement image must contain the required toolchain; a changed PVC name must match the deployed claim and mount. |
| Workspace: `experiment_commands`, `reproduction_command_id`, `verification_command_ids` | Commands are argument arrays. The supplied `go-test-all` runs `go test -json -count=1 ./...`; `go-vet-all` runs `go vet ./...`. Reproduction and verification IDs must refer to commands in `experiment_commands`. Retain these for an unchanged Go service. |
| Repair/review: profile IDs and `expected_*` pins | Keep IDs referring to the supplied service/workspace. Update pins after their prerequisites are final, using step 5. These pins identify the policy/evidence the agents must use. |
| Publication: `owner`, `repository`, `base_ref`, `branch_prefix`, `commit_author_*` | Select your fork, its base branch, a repair-branch prefix and a bot identity you control. Publication creates draft PRs within that repository. Author metadata does not supply GitHub authorization. |
| Maintenance: `assessment_policy` | Retain the supplied checkout rules for the same evidence schema. `signature_paths` and `comparison_paths` select facts for case linking/comparison; `allow_empty_string_paths` permits specified empty values. A new service needs reviewed rules matching its evidence, not just renamed IDs. |
| Maintenance: `collection` | Keep `enabled=false` through setup. Defaults read a 300-second window with 60-second overlap, up to 50 items, with successful observations sampled using modulus 5. The supported cron expression is `* * * * *`; admission budgets are separate coordinator `.env` settings. |
| All: caller lists, denied paths and `limits` | Caller lists contain authenticated identity names, not tokens. Preserve the supplied restrictions for this topology. Limits bound requests, files, execution and retention; use the units in each field name and review downstream capacity before increasing them. |
| All: readiness flags and `limitations` | The checked-in profiles include readiness decisions and limitation text from development acceptance. Review their applicability to your environment. A `*_ready=true` value or descriptive text does not grant permission, attest isolation or enable collection. |

Tokens and runtime gates belong in component `.env` files. In particular,
`verification_ready` and `publication_ready` need the separate environment gates
in step 1. See [Admission environment variables](README.md#admission-environment-variables)
before enabling collection, and retain finite run quotas.

## Understand the fingerprints

A fingerprint is a hash that answers: **“Is this the exact configuration I
approved?”** It detects a change; it does not prove the configuration is safe.

| Value | Where it comes from | Where it goes |
| --- | --- | --- |
| `expected_service_profile_version` | Evidence tool's `get_service_profile` response: `data.profile_version`, including `sha256:` | Repair, review and maintenance profiles |
| `expected_*_profile_sha256` | SHA-256 of the named profile's exact file bytes; 64 hex characters, without `sha256:` | The dependent profiles shown below |
| Coordinator runtime profile identity | Coordinator/assessment code combines maintenance profile bytes with the resolved summary limit | Computed by the application; do not manually invent it |

The service version hashes the tool's parsed, validated configuration. Do not
replace it with `shasum` of the service JSON, or try to reproduce it using
`jq -S`. Read it from the tool. Conversely, raw-file hashes change even when you
only reformat JSON. Finish editing a file before calculating its raw-file hash.

Update prerequisites before their consumers:

```text
Service version ──────────► repair, review, maintenance
Source file hash ─────────► repair, review, publication
Workspace file hash ──────► repair, review, publication
Review file hash ─────────► publication, maintenance
Repair file hash ─────────► maintenance
Publication file hash ────► maintenance
```

For example, changing the bot author changes the publication file hash. The
maintenance profile must then pin that new hash. Editing it once more afterward
makes the pin stale again.

## Worked example: your own fork

We will use **`YOUR_OWNER/retail-order-service`**, branch **`main`**. Replace
`YOUR_OWNER` with your account or organization. Repair branches and draft PRs
will stay in that fork, not in the upstream Truva repository.

Run shell examples from `truva-g3-domain-examples/software-maintenance/`, in
the same Bash session. JSON fragments below show fields to edit in the existing
files; they are **not complete replacement profiles**.

### 1. Start from a safe baseline

- Complete [framework bootstrap](README.md#clone-and-bootstrap-the-framework-once).
  For an existing installation, stop admitting new work and let active cases
  finish before replacing profiles. A local file edit does not stop a running pod.
- Prepare environment files with `./setup.sh init-config` (or follow the README's
  `migrate-config` instructions first for legacy configuration). It preserves
  existing files. Fill component credentials using [Identity wiring](README.md#identity-wiring):
  GitHub tokens must authorize your fork, and service tokens must match their
  caller/receiver peers. Agent AI keys belong in each agent's `.env`.
- Keep `collection.enabled=false` in `retail-order-maintenance.json`.
- Review [Readiness and safe profile changes](README.md#readiness-and-safe-profile-changes).
  The shipped workspace/publication profiles are ready, while their environment
  templates keep isolation acceptance and writes disabled. Filling tokens alone
  will therefore not make `validate` pass. Configure the following gates only
  after the corresponding operator decision:

| Component environment file | Setting | Required decision |
| --- | --- | --- |
| `tools/maintenance-workspace-tool/.env` | `MAINTENANCE_NETWORK_ISOLATION_ATTESTED=true` | Actual runner network isolation has been verified. A manifest alone is insufficient. |
| `tools/maintenance-workspace-tool/.env` | `MAINTENANCE_FUNCTIONAL_ACCEPTANCE_MODE=true` | Alternative for explicitly authorized development acceptance when production isolation is not established. This is not an isolation attestation. |
| `tools/maintenance-publication-tool/.env` | `MAINTENANCE_PUBLICATION_ENABLE_WRITES=true` | The operator authorizes draft creation in the selected repository and has reviewed GitHub permissions. |

For the ready workspace, use the applicable isolation/acceptance gate; enabling
both is unnecessary. If the required authorization is unavailable, stop before
deploying the corresponding ready workload. Do not turn on gates merely to
silence validation. Paid investigations require separate authorization later.

Check the unedited baseline after configuring credentials and gates:

```bash
./setup.sh validate
```

The command must exit successfully; its final message is:

```text
Configuration, profile pins and cross-component identities verified; no cluster changes made
```

If it fails, resolve the named credential, gate or pin issue before continuing.
Validation checks local configuration; it does not grant permission or establish
live GitHub access. Preserve a local copy of the profiles before editing:

```bash
profile_backup_dir="$(mktemp -d "${TMPDIR:-/tmp}/maintenance-profiles.XXXXXX")"
cp profiles/retail-order-*.json "$profile_backup_dir/"
printf 'Profile backup: %s\n' "$profile_backup_dir"
```

This copies profiles only, not credentials. Keep your backup until verification
is complete. Do not reset Git or delete existing evidence to prepare a new run.

### 2. Check the deployed service and its contract

Deploy the target from committed source in your fork through its own `setup.sh`.
Its setup derives the compiled repository identity from a GitHub HTTPS or SSH
`origin`. Confirm that origin names your fork. For a mirror or a deliberately
different build identity, set `ORDER_BUILD_REPOSITORY=YOUR_OWNER/retail-order-service`
in the target's `.env` before building. The override is validated and compiled;
changing a running pod's environment or using `rollout` does not change it.
Read its provenance:

```bash
curl --fail --silent --show-error http://retail-order-service.localhost/build-info |
  jq '{repository, git_revision, source_matches_commit, revision}'
```

Check all four facts:

- `repository` is `YOUR_OWNER/retail-order-service`.
- `git_revision` is a commit available in that fork.
- `source_matches_commit` is `true`.
- `revision` is a `build-...` identity, not the Git SHA.

If the repository still reports upstream, stop and check the target-service
revision, Git origin, any explicit build override, and whether the new image was
deployed. Do not weaken the evidence check or label upstream provenance as a fork.

Choose the commit containing the requirements you want agents to evaluate.
For a fork retaining the upstream history, the existing pinned contract commit
may still be correct. Verify its contents and availability rather than replacing
it with the latest commit automatically. The contract commit and running-code
commit may differ, provided the contract really applies to that release.

Edit `profiles/retail-order-service.json` first:

```json
{
  "repository": {
    "name": "YOUR_OWNER/retail-order-service",
    "default_branch": "main"
  },
  "contract_references": [
    {
      "kind": "git",
      "revision": "REPLACE_WITH_THE_VERIFIED_40_HEX_CONTRACT_COMMIT",
      "path": "README.md"
    }
  ]
}
```

Keep the existing `upstream.base_url` if the service still runs at the supplied
in-cluster address. `localhost` inside a tool pod means that pod—not your laptop
or the retail-service pod. Leave other profiles unchanged until the next step.

### 3. Obtain the new service version

With collection disabled, rebuild only the evidence tool. Its consumers can
remain stopped or on their old configuration while you finish preparation.
The `rebuild` command also deploys this component if it is not installed yet;
shared infrastructure must already be available.

```bash
# Use the example's pinned, published framework modules (v0.6.1).
export MAINTENANCE_FRAMEWORK_SOURCE=release
./setup.sh rebuild evidence
```

The setup validator checks raw-file dependency pins. Editing only the service
profile at this point leaves those pins intact. If you already edited source,
workspace or other profiles, restore a consistent prepared baseline or complete
their dependency updates first; do not bypass validation.

Read the new version using the evidence tool's coordinator credential. The
subshell below reads your trusted local `.env`, disables shell tracing, and sends
the header over stdin rather than embedding the token in curl's argument list:

```bash
service_version="$(
  set -euo pipefail
  set +x
  source tools/maintenance-evidence-tool/.env
  : "${MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN:?Configure the evidence credential}"
  printf 'Authorization: Bearer %s\n' "$MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN" |
    curl --fail --silent --show-error \
      --header @- --header 'Content-Type: application/json' \
      --data '{"profile_id":"retail-order-service"}' \
      http://maintenance-evidence-tool.localhost/api/capabilities/get_service_profile |
    jq -er '.success as $ok | .data.profile_version |
      select($ok == true and type == "string") |
      select(test("^sha256:[a-f0-9]{64}$"))'
)"
```

Stop if the request fails or the value is empty. This is a read-only tool call,
not a model call. It identifies the deployed evidence configuration; it does
not establish GitHub write permissions or prove the service is correct.
As a checkpoint, `printf '%s\n' "$service_version"` should show `sha256:` followed
by 64 lowercase hex characters. Keep this Bash session open for step 5.

### 4. Set the fork and author in source and publication

Edit these fields in `profiles/retail-order-source.json`:

```json
{
  "owner": "YOUR_OWNER",
  "repository": "retail-order-service",
  "default_ref": "main",
  "allowed_named_refs": ["main"]
}
```

Edit these fields in `profiles/retail-order-publication.json`:

```json
{
  "owner": "YOUR_OWNER",
  "repository": "retail-order-service",
  "base_ref": "main",
  "commit_author_name": "Your Maintenance Bot",
  "commit_author_email": "REPLACE_WITH_YOUR_BOT_EMAIL"
}
```

Use an identity you control; do not copy another developer's identity. Author
text does not authenticate the write—the publication tool uses its configured
GitHub credential. Keep denied paths, caller restrictions and draft-only policy.
Review any workspace-policy changes now, before calculating hashes.

### 5. Update the dependent fingerprints

You can edit the pins manually using the dependency map above. For example:

```bash
shasum -a 256 profiles/retail-order-source.json
```

Copy only the 64-character hash, not the filename, into the source-pin fields
in repair, review and publication. Then continue in dependency order.

Alternatively, the following complete block updates all dependent pins from
the files you just reviewed. It changes local profiles only: no deployment,
Git commit, PR, or model call. It requires `service_version` from step 3 and
the backup from step 1. If it fails midway, keep collection disabled and review
the partially updated files; it is not a multi-file transaction.

```bash
# BEGIN profile pin update
(
  set -euo pipefail
  [[ "${service_version:-}" =~ ^sha256:[a-f0-9]{64}$ ]] || {
    printf 'Obtain the service version in step 3 first.\n' >&2
    exit 1
  }
  edit_profile() {
    local file="$1" tmp
    shift
    tmp="$(mktemp "${file}.tmp.XXXXXX")"
    if jq "$@" "$file" > "$tmp"; then
      mv "$tmp" "$file"
    else
      rm -f "$tmp"
      return 1
    fi
  }
  file_hash() { shasum -a 256 "$1" | awk '{print $1}'; }
  source_hash="$(file_hash profiles/retail-order-source.json)"
  workspace_hash="$(file_hash profiles/retail-order-workspace.json)"
  for role in repair review; do
    edit_profile "profiles/retail-order-$role.json" \
      --arg service "$service_version" --arg source "$source_hash" \
      --arg workspace "$workspace_hash" \
      '.expected_service_profile_version = $service |
       .expected_source_profile_sha256 = $source |
       .expected_workspace_profile_sha256 = $workspace'
  done
  repair_hash="$(file_hash profiles/retail-order-repair.json)"
  review_hash="$(file_hash profiles/retail-order-review.json)"
  edit_profile profiles/retail-order-publication.json \
    --arg source "$source_hash" --arg workspace "$workspace_hash" \
    --arg review "$review_hash" \
    '.expected_source_profile_sha256 = $source |
     .expected_workspace_profile_sha256 = $workspace |
     .expected_review_profile_sha256 = $review'
  publication_hash="$(file_hash profiles/retail-order-publication.json)"
  edit_profile profiles/retail-order-maintenance.json \
    --arg service "$service_version" --arg repair "$repair_hash" \
    --arg review "$review_hash" --arg publication "$publication_hash" \
    '.expected_service_profile_version = $service |
     .expected_repair_profile_sha256 = $repair |
     .expected_review_profile_sha256 = $review |
     .expected_publication_profile_sha256 = $publication'
)
# END profile pin update
```

Do not reformat dependency files afterward without repeating this step. The
coordinator and assessment tool must also resolve the same
`MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES` setting: absent means 10,000 bytes.
That setting changes their runtime identity independently of these file pins.

### 6. Validate, deploy and inspect

```bash
git diff -- profiles/
./setup.sh validate
```

Review the diff, especially repository destinations and permissions. For files
not yet tracked in Git, compare against the backup; `git diff` does not show
untracked files. Expect the same successful validation message as in step 1,
now with your fork's reviewed profile bytes and updated pins. Validation checks
pin consistency and local configuration, not live source availability, network
isolation or authorization to publish.

For a first installation, follow the README's `deploy-all` then `test-all`
sequence. For an existing installation, rebuild each component whose embedded
profile changed. In this fork example, rebuild all eight so no old repository
configuration remains, with collection still disabled. Use `setup.sh rebuild`
per component in this order: evidence, source, workspace, repair, review,
publication, assessment, coordinator. Then run `./setup.sh test-all`.
Use the [rebuild map](#which-components-need-rebuilding) for later targeted updates.

Both evidence and assessment embed the service profile. Both coordinator and
assessment embed the maintenance profile. Updating only the coordinator leaves
assessment checks on old policy.

Finish with the README's [automatic investigation](README.md#run-an-automatic-investigation)
only after the operator authorizes paid work and draft creation. Confirm the PR
is in **your fork**, has the right diff and verification evidence, and is a draft.
Leave it open for a separately authorized duplicate-avoidance run.

The supplied smoke probes assume these profile IDs and source branch `main`.
If you choose a different base branch or rename profiles, review the probe
requests in `setup.sh` too; editing profile JSON alone does not update them.
The example above deliberately retains those defaults.

Before enabling collection, verify these checkpoints:

- `/build-info` names your fork, a committed source revision and matching build
  provenance, as checked in step 2.
- The authenticated service version from step 3 equals the declarations in
  repair, review and maintenance.
- `./setup.sh validate` and `./setup.sh test-all` both exit successfully after
  the affected components are rebuilt. Smoke tests check endpoints, skills and
  preflight; an end-to-end repair still requires the separately authorized run.
- `collection.enabled` remains `false` in the deployed maintenance profile until
  that run is authorized. A local edit alone does not update deployed pods.

## Which components need rebuilding?

This table identifies profile consumers in the supplied deployments. Include
components whose own profile changed and those whose prerequisite pins you
updated in step 5. Use `./setup.sh rebuild <component>` for each affected
component, in the deployment order from step 6.

| Changed profile | Components using that profile | Downstream profiles to update before deployment |
| --- | --- | --- |
| `retail-order-service.json` | `evidence`, `assessment` | Obtain the new service version; update repair, review and maintenance, then propagate repair/review/publication hashes. |
| `retail-order-source.json` | `source` | Repair, review, publication and maintenance. |
| `retail-order-workspace.json` | `workspace` | Repair, review, publication and maintenance. |
| `retail-order-repair.json` | `repair` | Maintenance. |
| `retail-order-review.json` | `review` | Publication and maintenance. |
| `retail-order-publication.json` | `publication` | Maintenance. |
| `retail-order-maintenance.json` | `assessment`, `coordinator` | None; keep their maintenance profile bytes and resolved summary limits identical. |

For example, a bot-author change requires updating publication's hash in
maintenance, then rebuilding `publication`, `assessment` and `coordinator`.
An environment-only change uses `rollout` for affected components; it does not
replace embedded profile bytes.

## Updating profiles later

| Change | What to update next |
| --- | --- |
| Service URL, contract or repository metadata | Rebuild evidence, obtain the new service version, update repair/review/maintenance, then propagate their raw-file hashes |
| Source scope or workspace policy | Update their raw-file pins in repair/review/publication, then propagate downstream hashes |
| Review policy | Rehash review; update publication and maintenance; rehash publication and update maintenance |
| Publication bot author or branch policy | Rehash publication and update maintenance |
| Collection settings only | Rebuild coordinator and assessment; no external profile pins point to the maintenance file |
| Coordinator summary limit only | Match coordinator and assessment environments and roll out both; no image change is needed for this environment-only setting |

Drain active work before policy changes. Historical receipts are not permission
to publish under a different current policy. Do not delete history or silently
rewrite stored receipts to make new settings match.

## Troubleshooting

| Message or symptom | Meaning and next action |
| --- | --- |
| Profile files are missing | Confirm you are in `truva-g3-domain-examples/software-maintenance/` and using a checkout containing `profiles/`. `init-config` creates environment files only. |
| `Ready workspace profile requires...` | Review step 1 and configure the authorized isolation/acceptance gate in `tools/maintenance-workspace-tool/.env`. |
| `Ready publication profile requires...` | Review draft-publication authorization before setting `MAINTENANCE_PUBLICATION_ENABLE_WRITES=true` in `tools/maintenance-publication-tool/.env`. |
| `Stale expected_source_profile_sha256` | Source bytes changed after the pin was calculated. Review the change and repeat step 5. |
| Service-version declarations differ | Repair, review and maintenance must use the same full `sha256:...` value from step 3. |
| All declarations match but runtime rejects evidence | Matching declarations alone do not prove they match the running evidence tool. Re-read its version and check rebuilt images and repository provenance. |
| Profile edit has no effect | Profiles are embedded in images. Use `rebuild`, not only `rollout`, and check the supplied manifest's profile path. |
| `/build-info` names upstream instead of the fork | Use a target revision with repository-identity support, check Git origin and ORDER_BUILD_REPOSITORY, then rebuild/deploy the target. Preserve the evidence comparison. |
| GitHub returns 403 or 404 | Check the actual fork, credential scope, organization policy and source/contract commit access. A public repo does not grant branch-write permission. |
| Evidence profile read returns 401/403 | Check the evidence coordinator token and caller policy; never put the GitHub token in that request. |
| Summary settings match locally but records disagree | Check both live deployments and profile bytes; local validation does not prove both pods have rolled out. |

The application code remains authoritative: service version calculation lives
in `internal/serviceevidence/profile.go`, raw-pin preflight in `setup-lib.sh`,
and coordinator runtime identity in `internal/maintenanceprofile/profile.go`.
