import assert from 'node:assert/strict'
import test from 'node:test'
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  candidateCase,
  compareResults,
  ObservationCollector,
  ObservationStore,
  redact,
  stageCandidateCase,
  validateObservation,
} from '../lib/observer.js'

function event(type, turn, data = {}) {
  return { type, data: { ...(turn === undefined ? {} : { turn }), ...data } }
}

test('DSH explicit invocation produces a redacted durable observation', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-observer-'))
  const store = new ObservationStore(root)
  const collector = new ObservationCollector(store, { hostVersion: '0.1.5-rc.2' })
  const session = { id: 'session-1' }

  collector.handle(session, event('turn/start', 1))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'user' },
    content: [{ type: 'text', text: 'Use /demo-skill with OPENAI_API_KEY=plain-secret' }],
  }))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'skill-invocation', name: 'demo-skill' },
    content: [{ type: 'text', text: '<skill_content name="demo-skill">...</skill_content>' }],
  }))
  collector.handle(session, event('assistant/message', 1, {
    message: { content: [{ type: 'text', text: 'done with sk-abcdefghijklmnopqrstuvwxyz' }] },
  }))
  const saved = collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))

  assert.equal(saved.length, 1)
  assert.equal(saved[0].host.name, 'dsh')
  assert.equal(saved[0].skill.name, 'demo-skill')
  assert.equal(saved[0].attribution.method, 'explicit')
  assert.equal(saved[0].review.status, 'candidate')
  assert.doesNotMatch(JSON.stringify(saved[0]), /plain-secret|sk-abc/)
  assert.deepEqual(store.get(saved[0].id), saved[0])
  assert.equal(statSync(store.path(saved[0].id)).mode & 0o777, 0o600)
})

test('DSH validates the shared cross-host observation fixtures', () => {
  const fixtureRoot = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'skill-up-observer', 'tests', 'fixtures')
  for (const name of ['codex-observation.json', 'dsh-observation.json']) {
    validateObservation(JSON.parse(readFileSync(join(fixtureRoot, name), 'utf8')))
  }
})

test('DSH model-selected skill is recorded only after a successful tool result', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-observer-tool-'))
  const store = new ObservationStore(root)
  const collector = new ObservationCollector(store)
  const session = { id: 'session-tool' }

  collector.handle(session, event('turn/start', 2))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'user' },
    content: [{ type: 'text', text: 'Please help' }],
  }))
  collector.handle(session, event('tool/call', 2, {
    callId: 'call-1',
    name: 'skill',
    arguments: '{"name":"demo-skill"}',
  }))
  collector.handle(session, event('tool/result', 2, {
    message: { content: [{ type: 'tool-result', toolCallId: 'call-1', isError: false }] },
  }))
  const saved = collector.handle(session, event('turn/end', 2, { reason: { kind: 'completed' } }))

  assert.equal(saved.length, 1)
  assert.equal(saved[0].attribution.method, 'instrumented')

  collector.handle(session, event('turn/start', 3))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'user' },
    content: [{ type: 'text', text: 'Try again' }],
  }))
  collector.handle(session, event('tool/call', 3, {
    callId: 'call-2',
    name: 'skill',
    arguments: '{"name":"missing-skill"}',
  }))
  collector.handle(session, event('tool/result', 3, {
    message: { content: [{ type: 'tool-result', toolCallId: 'call-2', isError: true }] },
  }))
  assert.deepEqual(collector.handle(session, event('turn/end', 3, { reason: { kind: 'completed' } })), [])
})

test('unattributed turns and control Skills are not persisted', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-observer-control-'))
  const store = new ObservationStore(root)
  const collector = new ObservationCollector(store)
  const session = { id: 'session-control' }
  collector.handle(session, event('turn/start', 1))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'user' },
    content: [{ type: 'text', text: 'Use /skill-upper' }],
  }))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'skill-invocation', name: 'skill-upper' },
    content: [],
  }))
  assert.deepEqual(collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } })), [])
  assert.deepEqual(store.list(), [])
})

test('review and candidate write require approval and preserve file mode', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-observer-write-'))
  const skill = join(workspace, 'demo-skill')
  const root = join(workspace, 'observer-data')
  mkdirSync(join(skill, 'evals', 'cases'), { recursive: true })
  writeFileSync(join(skill, 'SKILL.md'), '---\nname: demo-skill\n---\n')
  const evalPath = join(skill, 'evals', 'eval.yaml')
  writeFileSync(evalPath, [
    'schema_version: v1alpha1',
    'cases:',
    '  files:',
    '    - evals/cases/existing.yaml',
    '  defaults:',
    '    timeout_seconds: 120',
    'report:',
    '  formats: [json]',
    '',
  ].join('\n'), { mode: 0o640 })
  chmodSync(evalPath, 0o640)

  const store = new ObservationStore(root)
  const collector = new ObservationCollector(store)
  const session = { id: 'session-write' }
  collector.handle(session, event('turn/start', 1))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'user' },
    content: [{ type: 'text', text: 'Use /demo-skill' }],
  }))
  collector.handle(session, event('user/message', undefined, {
    source: { kind: 'skill-invocation', name: 'demo-skill' },
    content: [],
  }))
  const [observation] = collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))

  assert.throws(() => stageCandidateCase(workspace, 'demo-skill', observation), /must be approved/)
  const approved = store.review(observation.id, 'approved')
  const staged = stageCandidateCase(workspace, 'demo-skill', approved)
  assert.match(readFileSync(staged.casePath, 'utf8'), /Use \/demo-skill/)
  const updatedEval = readFileSync(evalPath, 'utf8')
  assert.match(updatedEval, /observed-demo-skill/)
  assert.ok(updatedEval.indexOf('observed-demo-skill') < updatedEval.indexOf('  defaults:'))
  assert.equal(statSync(evalPath).mode & 0o777, 0o640)
  staged.rollback()
  assert.doesNotMatch(readFileSync(evalPath, 'utf8'), /observed-demo-skill/)
})

test('candidate case and result comparison use business status', () => {
  const observation = {
    schema_version: 'v1alpha1',
    id: 'obs_0123456789abcdef01234567',
    host: { name: 'dsh' },
    skill: { name: 'demo-skill' },
    attribution: { method: 'explicit', confidence: 1 },
    input: { text: 'new scenario' },
    outcome: { status: 'completed' },
    correlation: { session_id: 'session' },
    timing: { observed_at: '2026-09-21T00:00:00Z' },
    privacy: { storage: 'local', consent: 'test' },
    review: { status: 'candidate' },
  }
  validateObservation(observation)
  assert.match(candidateCase(observation).yaml, /new scenario/)

  const before = { case_results: [{ case_id: 'case-a', configuration: 'with_skill', status: 'FAIL' }] }
  const after = { case_results: [{ case_id: 'case-a', configuration: 'with_skill', status: 'PASS' }] }
  assert.deepEqual(compareResults(before, after, 'before.json', 'after.json'), {
    before_path: 'before.json',
    after_path: 'after.json',
    same_cases: true,
    cases: [{ id: 'case-a', before: 'FAIL', after: 'PASS', changed: true }],
  })
})

test('redaction covers credential assignments', () => {
  const result = redact('TOKEN=secret-value PASSWORD="correct horse battery staple"')
  assert.equal(result.text, '[REDACTED] [REDACTED]')
  assert.deepEqual(result.categories, ['secret_assignment'])
})
