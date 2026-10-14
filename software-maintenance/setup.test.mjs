// Offline setup contract checks. No containers, Kubernetes or AI calls.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';

const source = path.dirname(fileURLToPath(import.meta.url));
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'maintenance-setup-test.'));
const example = path.join(root, 'domain/software-maintenance');
const framework = path.join(root, 'framework');
const dirs = ['tools/maintenance-evidence-tool', 'tools/maintenance-source-tool',
  'tools/maintenance-workspace-tool', 'agents/maintenance-repair-agent',
  'agents/maintenance-review-agent', 'tools/maintenance-publication-tool',
  'tools/maintenance-assessment-tool', 'agents/maintenance-agent'];
const write = (p, value) => {fs.mkdirSync(path.dirname(p), {recursive: true}); fs.writeFileSync(p, value);};
let passed = 0;
try {
  fs.mkdirSync(example, {recursive: true});
  for (const name of ['setup.sh', 'setup-lib.sh', 'diagnostic-profile.sh', 'preflight.sh', '.env.example', '.dockerignore', 'go.mod', 'go.sum'])
    fs.copyFileSync(path.join(source, name), path.join(example, name));
  for (const dir of ['profiles', 'diagnostics/config-preflight'])
    fs.cpSync(path.join(source, dir), path.join(example, dir), {recursive: true});
  for (const dir of dirs) {
    fs.mkdirSync(path.join(example, dir), {recursive: true});
    for (const name of ['.env.example', 'Dockerfile', 'Dockerfile.workspace'])
      fs.copyFileSync(path.join(source, dir, name), path.join(example, dir, name));
    write(path.join(example, dir, 'main.go'), 'package main\nfunc main() {}\n');
  }
  write(path.join(example, 'internal/example/example.go'), 'package example\n');
  const helper = [
    'TRUVAG3_ALL_SECRET_KEYS="OPENAI_API_KEY OPENROUTER_API_KEY ANTHROPIC_API_KEY GEMINI_API_KEY"',
    'TRUVAG3_AI_PROVIDER_KEYS="$TRUVAG3_ALL_SECRET_KEYS"',
    'TRUVAG3_CONFIG_INCLUDE_VARS="APP_ENV DEV_MODE"',
    'truvag3_load_env() { set -a; source "$1"; set +a; }',
    'go() { if [[ "$*" == *"/diagnostics/config-preflight/main.go" ]]; then command go "$@"; else return 0; fi; }',
    'kubectl() { if [[ "$*" == "config current-context" ]]; then echo wrong-context; else echo "Unexpected cluster call" >&2; return 99; fi; }',
    'kind() { if [[ "${FAKE_KIND_CLUSTER:-}" == present ]]; then echo "$CLUSTER_NAME"; else echo "Unexpected Kind call" >&2; return 99; fi; }',
    'truvag3_build_docker() {',
    '  test -f "$3/.dockerignore"',
    '  if find "$3" -name ".env*" | grep -q .; then return 98; fi',
    '  if [ "${MAINTENANCE_FRAMEWORK_SOURCE:-release}" = workspace ]; then',
    '    test -f "$3/framework/core/go.mod"',
    '    test -f "$3/software-maintenance/go.mod"',
    '  else test -f "$3/go.mod"; fi',
    '  echo "CAPTURED_CONTEXT"',
    '}',
    'truvag3_create_configmap() {',
    '  ! grep -E "API_KEY|SERVICE_TOKEN|GITHUB_TOKEN" "$3"',
    '  grep -q "^TRUVAG3_PLAN_MAX_TOKENS=128000$" "$3"',
    '  grep -q "^MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT=30s$" "$3"',
    '  if [[ "$1" == maintenance-agent-env-config ]]; then grep -q "^MAINTENANCE_INVESTIGATION_WORKERS=3$" "$3"; fi',
    '  grep -q "^TRUVAG3_AI_RETRY_ATTEMPTS=1$" "$3"',
    '  grep -q "^TRUVAG3_SKILL_RESOURCE_SELECTION_ATTEMPT_TIMEOUT=30s$" "$3"',
    '}'
  ].join('\n');
  write(path.join(framework, 'examples/k8-deployment/setup-env-lib.sh'), helper);
  for (const module of ['ai', 'core', 'memory', 'orchestration', 'resilience', 'telemetry'])
    write(path.join(framework, module, 'go.mod'), 'module example/' + module + '\n');
  write(path.join(framework, 'core/.env'), 'DO_NOT_COPY=fixture\n');
  const env = {...process.env, TRUVAG3_ROOT: framework};
  for (const key of Object.keys(env))
    if (/^(MAINTENANCE_|TRUVAG3_(PLAN|SKILL|SHARED_MEMORY|ITERATIVE|AI))/.test(key) || /API_KEY$/.test(key)) delete env[key];
  const run = (args, overrides = {}) => spawnSync('bash', [path.join(example, 'setup.sh'), ...args],
    {cwd: example, env: {...env, ...overrides}, encoding: 'utf8'});
  const ok = result => assert.equal(result.status, 0, result.stdout + result.stderr);
  const fails = (result, text) => {assert.notEqual(result.status, 0); assert.match(result.stdout + result.stderr, text);};
  ok(run(['help'], {TRUVAG3_ROOT: path.join(root, 'missing')})); passed++;
  ok(run(['init-config'])); passed++;
  const token = n => String(n).repeat(40);
  const identity = key => {
    if (key === 'MAINTENANCE_AGENT_API_TOKEN') return token(9);
    if (key.endsWith('SOURCE_WORKSPACE_TOKEN')) return token(4);
    if (key.endsWith('WORKSPACE_PUBLICATION_TOKEN')) return token(5);
    if (key.endsWith('REVIEW_PUBLICATION_TOKEN')) return token(6);
    if (key.includes('COORDINATOR_TOKEN') || key === 'MAINTENANCE_AGENT_SERVICE_TOKEN') return token(1);
    if (key.endsWith('REPAIR_TOKEN') || key === 'MAINTENANCE_REPAIR_SERVICE_TOKEN') return token(2);
    if (key.endsWith('REVIEW_TOKEN') || key === 'MAINTENANCE_REVIEW_SERVICE_TOKEN') return token(3);
    return token(7);
  };
  for (const dir of dirs) {
    const p = path.join(example, dir, '.env');
    let text = fs.readFileSync(p, 'utf8').replace(/^([A-Z][A-Z0-9_]*TOKEN)=$/gm, (_, key) => key + '=' + identity(key));
    text = text.replace('OPENROUTER_API_KEY=', 'OPENROUTER_API_KEY=fixture-not-a-real-key')
      .replace('MAINTENANCE_NETWORK_ISOLATION_ATTESTED=false', 'MAINTENANCE_NETWORK_ISOLATION_ATTESTED=true')
      .replace('MAINTENANCE_PUBLICATION_ENABLE_WRITES=false', 'MAINTENANCE_PUBLICATION_ENABLE_WRITES=true');
    write(p, text);
  }
  ok(run(['validate'])); passed++;
  const assessmentConfig = path.join(example, 'tools/maintenance-assessment-tool/.env');
  const savedAssessment = fs.readFileSync(assessmentConfig, 'utf8');
  write(assessmentConfig, savedAssessment + '\nMAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES=12000\n');
  for (const args of [['validate'], ['deploy', 'coordinator'], ['rollout', 'assessment']]) {
    const result = run(args);
    fails(result, /Coordinator and assessment.*must match/);
    assert.doesNotMatch(result.stdout + result.stderr, /Unexpected cluster call|CAPTURED_CONTEXT/);
    passed++;
  }
  write(assessmentConfig, savedAssessment + '\nMAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES=+010000\n');
  ok(run(['validate'])); passed++;
  for (const value of ['', '0', '-1', '65537', '99999999999999999999999', '10k']) {
    write(assessmentConfig, savedAssessment + '\nMAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES=' + value + '\n');
    fails(run(['validate']), /must be an integer between/); passed++;
  }
  write(assessmentConfig, savedAssessment);
  const sourceProfile = path.join(example, 'profiles/retail-order-source.json');
  const savedSourceProfile = fs.readFileSync(sourceProfile, 'utf8');
  write(sourceProfile, savedSourceProfile + '\n');
  for (const args of [['validate'], ['deploy', 'evidence'], ['rebuild', 'source'], ['rollout', 'review']]) {
    const result = run(args);
    fails(result, /Stale expected_source_profile_sha256/);
    assert.doesNotMatch(result.stdout + result.stderr, /Unexpected cluster call|CAPTURED_CONTEXT/);
    passed++;
  }
  write(sourceProfile, savedSourceProfile);
  const repair = path.join(example, 'agents/maintenance-repair-agent/.env');
  const savedRepair = fs.readFileSync(repair, 'utf8');
  write(repair, savedRepair.replace('OPENROUTER_API_KEY=fixture-not-a-real-key', 'OPENROUTER_API_KEY='));
  fails(run(['validate']), /requires at least one/); write(repair, savedRepair); passed++;
  const pub = path.join(example, 'tools/maintenance-publication-tool/.env'), savedPub=fs.readFileSync(pub, 'utf8');
  write(pub, savedPub.replace('MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN=' + token(5), 'MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN=' + token(8)));
  fails(run(['validate']), /Shared credential.*differs/); write(pub, savedPub); passed++;
  write(pub, savedPub.replace('MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN=' + token(5), '  export MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN=' + token(8)));
  fails(run(['validate']), /Shared credential.*differs/); write(pub, savedPub); passed++;
  ok(run(['image', 'evidence'])); passed++;
  ok(run(['image', 'evidence'], {MAINTENANCE_FRAMEWORK_SOURCE: 'workspace'})); passed++;
  fails(run(['full-deploy']), /Shared infrastructure is owned/); passed++;
  for (const verb of ['deploy', 'rebuild', 'rollout', 'preflight']) {
    const result = run([verb, 'evidence']);
    fails(result, /Kind cluster .* is missing/);
    assert.doesNotMatch(result.stdout + result.stderr, /CAPTURED_CONTEXT|Unexpected cluster call/);
    passed++;
  }
  const coordinator = path.join(example, 'agents/maintenance-agent/.env');
  const savedCoordinator = fs.readFileSync(coordinator, 'utf8');
  for (const value of ['', '0', '-1', '101', '1.5', 'three', '99999999999999999999999']) {
    write(coordinator, savedCoordinator + '\nMAINTENANCE_INVESTIGATION_WORKERS=' + value + '\n');
    fails(run(['validate']), /MAINTENANCE_INVESTIGATION_WORKERS/); passed++;
  }
  write(coordinator, savedCoordinator.replace('MAINTENANCE_INVESTIGATION_WORKERS=1', 'MAINTENANCE_INVESTIGATION_WORKERS=3'));
  ok(run(['validate'])); passed++;
  const configScript = 'source "$1/setup.sh" help >/dev/null; source "$2/examples/k8-deployment/setup-env-lib.sh"; load_env coordinator; configure_runtime coordinator';
  // Stub namespace creation only; exercise the real public ConfigMap projection.
  write(path.join(framework, 'examples/k8-deployment/setup-env-lib.sh'), helper + '\n' +
    'kubectl() { if [[ "$*" == create\\ namespace* ]]; then echo fixture; elif [[ "$*" == "apply -f -" ]]; then :; else return 99; fi; }\n' +
    'truvag3_create_secret() { :; }\n');
  const projection = spawnSync('bash', ['-c', configScript, 'check', example, framework], {cwd: example, env, encoding: 'utf8'});
  ok(projection); passed++;
  write(path.join(framework, 'examples/k8-deployment/setup-env-lib.sh'), helper);
  write(coordinator, savedCoordinator);
  write(coordinator, savedCoordinator + '\nMAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES=12000\n');
  fails(run(['deploy', 'coordinator']), /Coordinator and assessment.*must match/); passed++;
  write(coordinator, savedCoordinator);
  const wrongContext = run(['preflight'], {FAKE_KIND_CLUSTER: 'present'});
  fails(wrongContext, /Select kubectl context/);
  assert.doesNotMatch(wrongContext.stdout + wrongContext.stderr, /Unexpected cluster call/); passed++;
  const coordinatorProfile = path.join(example, 'profiles/retail-order-maintenance.json');
  const savedCoordinatorProfile = fs.readFileSync(coordinatorProfile, 'utf8');
  const changedVersion = JSON.parse(savedCoordinatorProfile);
  changedVersion.expected_service_profile_version = 'sha256:' + '0'.repeat(64);
  write(coordinatorProfile, JSON.stringify(changedVersion));
  fails(run(['validate']), /Service profile version differs/); passed++;
  write(coordinatorProfile, savedCoordinatorProfile);
  write(coordinator, savedCoordinator.replace('MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT=', '  export MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT='));
  const configCheck=spawnSync('bash', ['-c',
    'source "$1/setup.sh" help >/dev/null; source "$2/examples/k8-deployment/setup-env-lib.sh"; load_env coordinator; maintenance_create_configmap sample software-maintenance unused MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT', 'check', example, framework],
    {cwd:example, env, encoding:'utf8'});
  ok(configCheck); write(coordinator, savedCoordinator); passed++;
  const rootConfig = path.join(example, '.env'), savedRoot = fs.readFileSync(rootConfig, 'utf8');
  write(rootConfig, savedRoot + '\n  export OPENAI_API_KEY=fixture-root-secret\n');
  const rootRejected = run(['validate']);
  fails(rootRejected, /Root .env contains credential/);
  assert.doesNotMatch(rootRejected.stdout + rootRejected.stderr, /fixture-root-secret/);
  write(rootConfig, savedRoot); passed++;
  write(rootConfig, savedRoot + '\nAWS_SECRET_ACCESS_KEY=fixture-root-secret\n');
  const awsRejected = run(['validate']);
  fails(awsRejected, /Root .env contains credential/);
  assert.doesNotMatch(awsRejected.stdout + awsRejected.stderr, /fixture-root-secret/);
  write(rootConfig, savedRoot); passed++;
  const legacy = fs.readFileSync(path.join(example, '.env'), 'utf8') + '\n' +
    dirs.map(dir=>fs.readFileSync(path.join(example, dir, '.env'),'utf8')).join('\n');
  for(const dir of dirs) fs.unlinkSync(path.join(example, dir, '.env'));
  write(path.join(example, '.env'), legacy);
  fails(run(['validate']), /Root .env contains credential/); passed++;
  ok(run(['migrate-config']));
  const archive = fs.readdirSync(example).find(p=>p.startsWith('.env-migration.'));
  assert.equal(fs.readFileSync(path.join(example, archive, 'original.env'),'utf8'), legacy);
  ok(run(['validate'])); passed++;
  fails(run(['migrate-config']), /Refusing to overwrite/); passed++;
  console.log(passed + ' offline setup checks passed; no external operations');
} finally {
  fs.rmSync(root, {recursive: true, force: true});
}
