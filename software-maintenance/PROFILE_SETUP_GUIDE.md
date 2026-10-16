# Prepare profiles for your own retail service

This guide explains the configuration files that connect the maintenance agents
to **your service and your GitHub repository**. Start with the
[README setup](README.md#setup) for framework bootstrap and credentials, then
use this guide before deploying the complete maintenance solution.

**Current prerequisite:** the target service is awaiting safety review and public
release. Its hardcoded build repository identity also needs the portability fix
described in the [fork checklist](README.md#use-your-own-repository-for-the-demo).
The fork walkthrough below applies after those prerequisites are met. It does
not bypass them or claim that the fork path is already live-verified.

## Contents

1. [What is a profile?](#what-is-a-profile)
2. [Which files do I change?](#which-files-do-i-change)
3. [Understand the fingerprints](#understand-the-fingerprints)
4. [Worked example: your own fork](#worked-example-your-own-fork)
5. [Updating profiles later](#updating-profiles-later)
6. [Troubleshooting](#troubleshooting)

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

All seven files are under `profiles/`. The shorter names below omit the
`retail-order-` prefix and `.json` suffix.

| File | Plain-English job | Typical change for a fork |
| --- | --- | --- |
| `service` | Where is the running service, and where are its requirements? | GitHub repository name, contract commit, and service URL if it changed |
| `source` | Which repository and refs may the source tool read? | GitHub owner/repository and base branch |
| `workspace` | How may code be staged and tested? | Usually unchanged for the same Go service; review runner, storage and isolation policy |
| `repair` | What evidence and workspace policy may the repair agent use? | Update prerequisite fingerprints |
| `review` | What evidence must an independent review use? | Update prerequisite fingerprints |
| `publication` | Where may a draft PR be created? | GitHub owner/repository, base branch, bot author and prerequisite fingerprints |
| `maintenance` | How does the coordinator collect and assess observations? | Update prerequisite fingerprints; keep collection disabled during setup |

`repository_id: "retail-order-service"` is a **logical name**, not a GitHub URL.
You can retain it, and the existing profile IDs, for this single-service example.
The GitHub address is carried by `owner`/`repository` or `repository.name`.
Changing logical IDs requires changing all references to them, so it is not
needed just to use a fork.

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

- Complete the README bootstrap and component credential setup. GitHub tokens
  must authorize your fork; service tokens must match their caller/receiver peers.
- Keep `collection.enabled=false` in `retail-order-maintenance.json`.
- For an existing installation, stop admitting new work and let active cases
  finish before replacing profiles. A local file edit does not stop a running pod.
- Begin with a consistent profile set: `./setup.sh validate` should pass before
  starting this example. Readiness/write settings need the explicit authorizations
  described in the README; validation does not grant those permissions.
- Preserve a local copy of the profiles before editing:

```bash
profile_backup_dir="$(mktemp -d "${TMPDIR:-/tmp}/maintenance-profiles.XXXXXX")"
cp profiles/retail-order-*.json "$profile_backup_dir/"
printf 'Profile backup: %s\n' "$profile_backup_dir"
```

This copies profiles only, not credentials. Keep your backup until verification
is complete. Do not reset Git or delete existing evidence to prepare a new run.

### 2. Check the deployed service and its contract

Deploy the target from committed source in your fork through its own `setup.sh`.
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

If the repository still reports `truvaagents/retail-order-service`, stop: the
fork-provenance prerequisite is not complete. Do not weaken the evidence check.

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
untracked files. Validation checks pin consistency and local configuration, not
live source availability, network isolation or authorization to publish.

For a first installation, follow the README's `deploy-all` then `test-all`
sequence. For an existing installation, rebuild each component whose embedded
profile changed. In this fork example, rebuild all eight so no old repository
configuration remains, with collection still disabled. Use `setup.sh rebuild`
per component in this order: evidence, source, workspace, repair, review,
publication, assessment, coordinator. Then run `./setup.sh test-all`.

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
| `Stale expected_source_profile_sha256` | Source bytes changed after the pin was calculated. Review the change and repeat step 5. |
| Service-version declarations differ | Repair, review and maintenance must use the same full `sha256:...` value from step 3. |
| All declarations match but runtime rejects evidence | Matching declarations alone do not prove they match the running evidence tool. Re-read its version and check rebuilt images and repository provenance. |
| Profile edit has no effect | Profiles are embedded in images. Use `rebuild`, not only `rollout`, and check the supplied manifest's profile path. |
| `/build-info` names upstream instead of the fork | Complete the target-service provenance fix; do not change the profile to conceal the mismatch. |
| GitHub returns 403 or 404 | Check the actual fork, credential scope, organization policy and source/contract commit access. A public repo does not grant branch-write permission. |
| Evidence profile read returns 401/403 | Check the evidence coordinator token and caller policy; never put the GitHub token in that request. |
| Summary settings match locally but records disagree | Check both live deployments and profile bytes; local validation does not prove both pods have rolled out. |

The application code remains authoritative: service version calculation lives
in `internal/serviceevidence/profile.go`, raw-pin preflight in `setup-lib.sh`,
and coordinator runtime identity in `internal/maintenanceprofile/profile.go`.
