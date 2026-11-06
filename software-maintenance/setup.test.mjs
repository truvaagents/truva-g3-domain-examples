// Offline setup contract checks. No containers, Kubernetes or AI calls.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import {spawn, spawnSync} from 'node:child_process';

const source = path.dirname(fileURLToPath(import.meta.url));
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'maintenance-setup-test.'));
// Model public checkout files consistently; individual image tests use umask 077.
const fixtureUmask = process.umask(0o022);
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
    '  if [[ -n "${BUILD_CONTEXT_CHECK:-}" ]]; then node "$BUILD_CONTEXT_CHECK" "$3" "${MAINTENANCE_FRAMEWORK_SOURCE:-release}" "$(umask)"; fi',
    '  echo "CAPTURED_CONTEXT"',
    '}',
    'truvag3_create_configmap() {',
    '  ! grep -E "API_KEY|SERVICE_TOKEN|GITHUB_TOKEN" "$3"',
    '  grep -q "^TRUVAG3_PLAN_MAX_TOKENS=128000$" "$3"',
    '  grep -q "^MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT=30s$" "$3"',
    '  if [[ "$1" == maintenance-agent-env-config ]]; then grep -q "^MAINTENANCE_INVESTIGATION_WORKERS=3$" "$3"; fi',
    '  ! grep -q "^TRUVAG3_AI_RETRY_ATTEMPTS=" "$3"',
    '  ! grep -q "^TRUVAG3_SKILL_RESOURCE_SELECTION_ATTEMPT_TIMEOUT=" "$3"',
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
  const readinessFixture = path.join(root, 'readiness-fixture.mjs');
  const readinessRequests = path.join(root, 'readiness-requests.json');
  const readinessArgs = path.join(root, 'readiness-curl-args');
  write(readinessFixture, String.raw`
import fs from 'node:fs';
import http from 'node:http';
const requests = {};
const server = http.createServer((request, response) => {
  const key = request.method + ' ' + request.url;
  requests[key] = (requests[key] || 0) + 1;
  fs.writeFileSync(process.argv[2], JSON.stringify(requests));
  const status = request.url === '/transient' ? (requests[key] < 3 ? 502 : 200) :
    request.url === '/unauthorized' ? 401 : request.url === '/forbidden' ? 403 : 503;
  response.writeHead(status).end(status === 200 ? 'ready' : 'not ready');
});
server.listen(0, '127.0.0.1', () => process.send({port: server.address().port}));
`);
  const readinessServer = spawn(process.execPath, [readinessFixture, readinessRequests],
    {stdio: ['ignore', 'ignore', 'inherit', 'ipc']});
  try {
    const [address] = await Promise.race([
      once(readinessServer, 'message'),
      once(readinessServer, 'exit').then(([code]) => {throw new Error('readiness fixture exited: ' + code);})
    ]);
    const readinessScript = 'source "$1/setup.sh" help >/dev/null; curl() { printf "%s\\n" "$@" > "$READINESS_CURL_ARGS"; command curl "$@"; }; readiness_get "$2"';
    for (const [route, expectedRequests, succeeds] of [['transient', 3, true], ['persistent', 6, false],
      ['unauthorized', 1, false], ['forbidden', 1, false]]) {
      const result = spawnSync('bash', ['-c', readinessScript, 'check', example, 'http://127.0.0.1:' + address.port + '/' + route],
        {cwd: example, env: {...env, READINESS_CURL_ARGS: readinessArgs, NO_PROXY: '127.0.0.1', no_proxy: '127.0.0.1'}, encoding: 'utf8', timeout: 40000});
      if (succeeds) {ok(result); assert.equal(result.stdout, 'ready');}
      else assert.equal(result.status, 22, result.stdout + result.stderr);
      const requests = JSON.parse(fs.readFileSync(readinessRequests, 'utf8'));
      assert.equal(requests['GET /' + route], expectedRequests);
      assert.ok(Object.keys(requests).every(key => key.startsWith('GET ')));
      const flags = fs.readFileSync(readinessArgs, 'utf8').trim().split('\n');
      for (const [flag, maximum] of [['--retry', 10], ['--retry-max-time', 60], ['--connect-timeout', 5], ['--max-time', 10]]) {
        const value = Number(flags[flags.indexOf(flag) + 1]);
        assert.ok(flags.includes(flag) && value > 0 && value <= maximum, flag + ' must be bounded');
      }
      assert.ok(flags.includes('--retry-connrefused'));
      assert.ok(!flags.includes('--retry-all-errors')); passed++;
    }
  } finally {
    if (readinessServer.exitCode === null) {
      const stopped = once(readinessServer, 'exit');
      readinessServer.kill();
      await stopped;
    }
  }
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
    text = text.replace('OPENROUTER_API_KEY=', 'OPENROUTER_API_KEY=fixture-not-a-real-key');
    write(p, text);
  }
  ok(run(['validate'])); passed++;
  const workspaceConfig = path.join(example, 'tools/maintenance-workspace-tool/.env');
  const savedWorkspace = fs.readFileSync(workspaceConfig, 'utf8');
  write(workspaceConfig, savedWorkspace.replace('MAINTENANCE_FUNCTIONAL_ACCEPTANCE_MODE=true', 'MAINTENANCE_FUNCTIONAL_ACCEPTANCE_MODE=false'));
  fails(run(['validate']), /Ready workspace profile requires verified network isolation or explicit development acceptance mode/);
  write(workspaceConfig, savedWorkspace); passed++;
  const publicationConfig = path.join(example, 'tools/maintenance-publication-tool/.env');
  const savedPublication = fs.readFileSync(publicationConfig, 'utf8');
  write(publicationConfig, savedPublication.replace('MAINTENANCE_PUBLICATION_ENABLE_WRITES=true', 'MAINTENANCE_PUBLICATION_ENABLE_WRITES=false'));
  fails(run(['validate']), /Ready publication profile requires explicit MAINTENANCE_PUBLICATION_ENABLE_WRITES=true/);
  write(publicationConfig, savedPublication); passed++;
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
  const contextCheck = path.join(root, 'check-build-context.mjs');
  write(contextCheck, String.raw`
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
const [context, mode, mask] = process.argv.slice(2);
const permissions = p => fs.statSync(p).mode & 0o777;
assert.equal(permissions(context), 0o700, 'temporary context root must stay private');
assert.equal(Number.parseInt(mask, 8), 0o077, 'preparation must preserve the caller umask');
const base = mode === 'workspace' ? path.join(context, 'software-maintenance') : context;
const checkPublic = directory => {
  assert.equal(permissions(directory) & 0o005, 0o005, directory + ' must allow traversal and reading');
  for (const entry of fs.readdirSync(directory, {withFileTypes: true})) {
    const p = path.join(directory, entry.name);
    if (entry.isDirectory()) checkPublic(p);
    else assert.equal(permissions(p) & 0o004, 0o004, p + ' must be readable by the image user');
  }
};
checkPublic(path.join(base, 'profiles'));
if (mode === 'workspace') {
  for (const directory of [base, path.join(context, 'framework')])
    assert.equal(permissions(directory) & 0o005, 0o005);
}
const checkPrivacy = directory => {
  for (const entry of fs.readdirSync(directory, {withFileTypes: true})) {
    assert.ok(!entry.name.startsWith('.env'), 'environment file entered build context');
    if (entry.isDirectory()) checkPrivacy(path.join(directory, entry.name));
  }
};
checkPrivacy(context);
fs.writeFileSync(process.env.BUILD_CONTEXT_REPORT, JSON.stringify({mode, root_mode: permissions(context), caller_umask: mask, profiles_readable: true, environment_files_absent: true}));
`);
  const privateInputs = [path.join(example, '.env'), path.join(example, 'tools/maintenance-evidence-tool/.env'),
    path.join(framework, 'core/.env')];
  const publicInputs = [path.join(example, 'profiles'), path.join(example, 'profiles/retail-order-service.json'),
    path.join(example, 'go.mod')];
  const inputState = () => [...publicInputs, ...privateInputs].map(p => ({
    path: p, mode: fs.statSync(p).mode & 0o777,
    sha256: fs.statSync(p).isFile() ? createHash('sha256').update(fs.readFileSync(p)).digest('hex') : null
  }));
  const sourceBeforePrivateBuild = inputState();
  for (const mode of ['release', 'workspace']) {
    const report = path.join(root, 'build-context-' + mode + '.json');
    const privateImage = spawnSync('bash', ['-c',
      'set -e; umask 077; bash "$1/setup.sh" image evidence; test "$(umask)" = 0077', 'check', example],
      {cwd: example, env: {...env, MAINTENANCE_FRAMEWORK_SOURCE: mode, BUILD_CONTEXT_CHECK: contextCheck, BUILD_CONTEXT_REPORT: report}, encoding: 'utf8'});
    ok(privateImage);
    assert.equal(JSON.parse(fs.readFileSync(report, 'utf8')).profiles_readable, true);
    assert.deepEqual(inputState(), sourceBeforePrivateBuild, 'image preparation changed source modes or contents'); passed++;
  }
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
  write(coordinator, savedCoordinator + '\nMAINTENANCE_INVESTIGATION_WORKERS=3\n');
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
  const cleanupCalls = path.join(root, 'cleanup-calls');
  write(path.join(framework, 'examples/k8-deployment/setup-env-lib.sh'), helper + '\n' + [
    'kubectl() {',
    '  case "$*" in',
    '    "config current-context") echo "kind-$CLUSTER_NAME" ;;',
    '    "get namespace truvag3-examples"|"get service redis -n truvag3-examples") : ;;',
    '    delete*)',
    '      printf "%s\\n" "$*" >> "$CLEANUP_CALLS"',
    '      if [[ "$2" == jobs ]]; then',
    '        if [[ "${CLEANUP_FAIL_JOBS:-false}" == true ]]; then echo "Fixture Job cleanup failed" >&2; return 42; fi',
    '        if [[ "${CLEANUP_EMPTY_JOBS:-false}" == true ]]; then echo "No resources found"; fi',
    '      fi',
    '      ;;',
    '    *) echo "Unexpected cluster call" >&2; return 99 ;;',
    '  esac',
    '}'
  ].join('\n'));
  const cleanup = (component, overrides = {}) => {
    write(cleanupCalls, '');
    const result = run(['cleanup', component], {FAKE_KIND_CLUSTER: 'present', CLEANUP_CALLS: cleanupCalls, ...overrides});
    return {result, calls: fs.readFileSync(cleanupCalls, 'utf8').trim().split('\n').filter(Boolean)};
  };
  const expectedWorkspaceCleanup = [
    'delete jobs -n software-maintenance -l app=maintenance-workspace-tool,maintenance-sandbox=true --cascade=foreground --wait=true --timeout=120s --ignore-not-found',
    'delete -f ' + path.join(example, 'deploy/maintenance-workspace-tool.yaml') + ' --ignore-not-found',
    'delete configmap maintenance-workspace-tool-env-config -n software-maintenance --ignore-not-found',
    'delete secret maintenance-workspace-tool-secrets -n software-maintenance --ignore-not-found'
  ];
  const workspaceCleanup = cleanup('workspace');
  ok(workspaceCleanup.result);
  assert.deepEqual(workspaceCleanup.calls, expectedWorkspaceCleanup); passed++;
  const emptyWorkspaceCleanup = cleanup('workspace', {CLEANUP_EMPTY_JOBS: 'true'});
  ok(emptyWorkspaceCleanup.result);
  assert.deepEqual(emptyWorkspaceCleanup.calls, expectedWorkspaceCleanup); passed++;
  const failedWorkspaceCleanup = cleanup('workspace', {CLEANUP_FAIL_JOBS: 'true'});
  fails(failedWorkspaceCleanup.result, /Fixture Job cleanup failed/);
  assert.deepEqual(failedWorkspaceCleanup.calls, [expectedWorkspaceCleanup[0]]); passed++;
  for (const component of ['evidence', 'source', 'publication', 'repair', 'review', 'assessment', 'coordinator']) {
    const otherCleanup = cleanup(component);
    ok(otherCleanup.result);
    assert.equal(otherCleanup.calls.length, 3);
    assert.ok(otherCleanup.calls[0].startsWith('delete -f '));
    assert.ok(otherCleanup.calls.every(call => !call.startsWith('delete jobs ')));
    assert.ok(otherCleanup.calls.every(call => !call.includes('maintenance-workspace-tool'))); passed++;
  }
  write(path.join(framework, 'examples/k8-deployment/setup-env-lib.sh'), helper);
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
  process.umask(fixtureUmask);
}
