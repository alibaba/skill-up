import assert from 'node:assert/strict'
import test from 'node:test'
import { chmodSync, existsSync, mkdtempSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  candidateCase,
  collectSkillFeedback,
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
  const fixtureRoot = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..', 'schemas', 'skill-observation', 'v1alpha1', 'fixtures')
  for (const name of ['codex-observation.json', 'dsh-observation.json']) {
    validateObservation(JSON.parse(readFileSync(join(fixtureRoot, name), 'utf8')))
  }
})

test('DSH bundles the canonical observation schema', () => {
  const testRoot = dirname(fileURLToPath(import.meta.url))
  const bundled = join(testRoot, '..', 'dist', 'skill-observation', 'v1alpha1', 'observation.schema.json')
  const canonical = join(testRoot, '..', '..', '..', 'schemas', 'skill-observation', 'v1alpha1', 'observation.schema.json')
  assert.equal(readFileSync(bundled, 'utf8'), readFileSync(canonical, 'utf8'))
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

test('next-turn user feedback is durably linked as an unclassified candidate', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-observer-followup-'))
  const store = new ObservationStore(root)
  const session = { id: 'followup-session' }
  const collector = new ObservationCollector(store)
  collector.handle(session, event('turn/start', 1))
  collector.handle(session, event('user/message', 1, {
    source: { kind: 'user' }, content: [{ type: 'text', text: 'Use code-stats on ./project' }],
  }))
  collector.handle(session, event('user/message', 1, {
    source: { kind: 'skill-invocation', name: 'code-stats' }, content: [],
  }))
  const [observation] = collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))

  // A restarted collector still finds the preceding observation in local storage.
  const resumed = new ObservationCollector(store)
  resumed.handle(session, event('turn/start', 2))
  resumed.handle(session, event('user/message', 2, {
    source: { kind: 'user' }, content: [{ type: 'text', text: 'It counted node_modules. Please exclude dependencies. OPENAI_API_KEY=plain-secret' }],
  }))
  resumed.handle(session, event('turn/end', 2, { reason: { kind: 'completed' } }))
  const recorded = store.get(observation.id)
  assert.equal(recorded.followups.length, 1)
  assert.equal(recorded.followups[0].turn_id, '2')
  assert.doesNotMatch(JSON.stringify(recorded), /plain-secret/)
  assert.equal(recorded.feedback, undefined)
  assert.equal(recorded.review.status, 'candidate')
  const collected = collectSkillFeedback(store, 'code-stats')
  assert.equal(collected.feedback_observations, 1)
  assert.equal(collected.observations[0].id, observation.id)
  resumed.handle(session, event('turn/start', 2))
  resumed.handle(session, event('user/message', 2, {
    source: { kind: 'user' }, content: [{ type: 'text', text: 'It counted node_modules.' }],
  }))
  resumed.handle(session, event('turn/end', 2, { reason: { kind: 'completed' } }))
  assert.equal(store.get(observation.id).followups.length, 1)
})

test('follow-ups are not assigned across sessions or ambiguous Skill calls', () => {
  const store = new ObservationStore(mkdtempSync(join(tmpdir(), 'dsh-observer-ambiguous-')))
  const collector = new ObservationCollector(store)
  const session = { id: 'multi-skill' }
  collector.handle(session, event('turn/start', 1))
  collector.handle(session, event('user/message', 1, {
    source: { kind: 'user' }, content: [{ type: 'text', text: 'Use both Skills' }],
  }))
  for (const name of ['skill-a', 'skill-b']) {
    collector.handle(session, event('user/message', 1, {
      source: { kind: 'skill-invocation', name }, content: [],
    }))
  }
  collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))
  for (const current of [session, { id: 'other-session' }]) {
    collector.handle(current, event('turn/start', 2))
    collector.handle(current, event('user/message', 2, {
      source: { kind: 'user' }, content: [{ type: 'text', text: 'This result is wrong' }],
    }))
    collector.handle(current, event('turn/end', 2, { reason: { kind: 'completed' } }))
  }
  assert.ok(store.list().every((item) => !item.followups))
})

test('feedback stays linked when the next turn also invokes a Skill', () => {
  const store = new ObservationStore(mkdtempSync(join(tmpdir(), 'dsh-observer-reinvoke-')))
  const collector = new ObservationCollector(store)
  const session = { id: 'reinvoke-session' }
  for (const turn of [1, 2]) {
    collector.handle(session, event('turn/start', turn))
    collector.handle(session, event('user/message', turn, {
      source: { kind: 'user' },
      content: [{ type: 'text', text: turn === 1 ? 'Count the files' : 'That included dependencies. Try again.' }],
    }))
    collector.handle(session, event('user/message', turn, {
      source: { kind: 'skill-invocation', name: 'code-stats' }, content: [],
    }))
    collector.handle(session, event('turn/end', turn, { reason: { kind: 'completed' } }))
  }
  const first = store.list().find((item) => item.correlation.turn_id === '1')
  assert.equal(first.followups[0].text, 'That included dependencies. Try again.')
  assert.equal(store.list().length, 2)
})

test('a late next turn is not linked to an old Skill observation', () => {
  const store = new ObservationStore(mkdtempSync(join(tmpdir(), 'dsh-observer-late-')))
  const collector = new ObservationCollector(store)
  const session = { id: 'late-session' }
  collector.handle(session, event('turn/start', 1))
  collector.handle(session, event('user/message', 1, {
    source: { kind: 'user' }, content: [{ type: 'text', text: 'Use demo-skill' }],
  }))
  collector.handle(session, event('user/message', 1, {
    source: { kind: 'skill-invocation', name: 'demo-skill' }, content: [],
  }))
  const [observation] = collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))
  store.update(observation.id, (item) => {
    item.timing.completed_at = '2020-01-01T00:00:00.000Z'
  })
  collector.handle(session, event('turn/start', 2))
  collector.handle(session, event('user/message', 2, {
    source: { kind: 'user' }, content: [{ type: 'text', text: 'Unrelated later request' }],
  }))
  collector.handle(session, event('turn/end', 2, { reason: { kind: 'completed' } }))
  assert.equal(store.get(observation.id).followups, undefined)
})

test('historical feedback collection can page through all matching observations', () => {
  const records = Array.from({ length: 21 }, (_, index) => ({
    id: `obs_${String(index).padStart(24, '0')}`,
    skill: { name: 'code-stats' },
    timing: { observed_at: '2026-09-24T00:00:00Z' },
    input: { text: `Run ${index}` },
    outcome: { status: 'completed' },
    followups: [{ turn_id: '2', text: `Feedback ${index}`, observed_at: '2026-09-24T00:01:00Z' }],
  }))
  const store = { list: () => records }
  const first = collectSkillFeedback(store, 'code-stats')
  assert.equal(first.observations.length, 20)
  assert.equal(first.next_offset, 20)
  const second = collectSkillFeedback(store, 'code-stats', { offset: first.next_offset })
  assert.equal(second.observations.length, 1)
  assert.equal(second.next_offset, null)
  assert.equal(second.observations[0].input, 'Run 20')
  assert.throws(() => collectSkillFeedback(store, 'code-stats', { limit: 0 }), /limit/)
})

test('DSH scopes reused tool call IDs to their session', () => {
  const store = new ObservationStore(mkdtempSync(join(tmpdir(), 'dsh-observer-concurrent-')))
  const collector = new ObservationCollector(store)
  const sessions = [{ id: 'session-a' }, { id: 'session-b' }]

  for (const [index, session] of sessions.entries()) {
    collector.handle(session, event('turn/start', 1))
    collector.handle(session, event('user/message', undefined, {
      source: { kind: 'user' },
      content: [{ type: 'text', text: `Prompt ${index}` }],
    }))
    collector.handle(session, event('tool/call', 1, {
      callId: 'call-1',
      name: 'skill',
      arguments: JSON.stringify({ name: `demo-skill-${index}` }),
    }))
  }

  for (const session of sessions) {
    collector.handle(session, event('tool/result', 1, {
      message: { content: [{ type: 'tool-result', toolCallId: 'call-1', isError: false }] },
    }))
    collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))
  }

  assert.deepEqual(store.list().map((item) => item.skill.name).sort(), ['demo-skill-0', 'demo-skill-1'])
})

test('DSH scopes tool calls without delimiter collisions', () => {
  const store = new ObservationStore(mkdtempSync(join(tmpdir(), 'dsh-observer-call-key-')))
  const collector = new ObservationCollector(store)
  const calls = [
    { session: { id: 'session:a' }, callId: 'call', skill: 'demo-skill-0' },
    { session: { id: 'session' }, callId: 'a:call', skill: 'demo-skill-1' },
  ]

  for (const { session, callId, skill } of calls) {
    collector.handle(session, event('turn/start', 1))
    collector.handle(session, event('user/message', undefined, {
      source: { kind: 'user' },
      content: [{ type: 'text', text: `Use ${skill}` }],
    }))
    collector.handle(session, event('tool/call', 1, {
      callId,
      name: 'skill',
      arguments: JSON.stringify({ name: skill }),
    }))
  }

  for (const { session, callId } of calls) {
    collector.handle(session, event('tool/result', 1, {
      message: { content: [{ type: 'tool-result', toolCallId: callId, isError: false }] },
    }))
    collector.handle(session, event('turn/end', 1, { reason: { kind: 'completed' } }))
  }

  assert.deepEqual(store.list().map((item) => item.skill.name).sort(), ['demo-skill-0', 'demo-skill-1'])
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

test('feedback invalidates approval and a duplicate write preserves the existing case', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-observer-retry-'))
  const skill = join(workspace, 'demo-skill')
  const root = join(workspace, 'observer-data')
  mkdirSync(join(skill, 'evals', 'cases'), { recursive: true })
  writeFileSync(join(skill, 'SKILL.md'), '---\nname: demo-skill\n---\n')
  writeFileSync(join(skill, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\ncases:\n  files: []\n')

  const store = new ObservationStore(root)
  const collector = new ObservationCollector(store)
  const session = { id: 'session-retry' }
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

  const approved = store.review(observation.id, 'approved')
  const staged = stageCandidateCase(workspace, 'demo-skill', approved)
  staged.commit()
  assert.throws(() => stageCandidateCase(workspace, 'demo-skill', approved), /already references/)
  assert.equal(existsSync(staged.casePath), true)

  const updated = store.feedback(observation.id, 'negative', 'Needs another assertion')
  assert.deepEqual(updated.review, { status: 'candidate' })
  assert.throws(() => stageCandidateCase(workspace, 'demo-skill', updated), /must be approved/)
})

test('candidate staging rejects a root for a different Skill', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-observer-wrong-skill-'))
  const skill = join(workspace, 'other-skill')
  mkdirSync(join(skill, 'evals', 'cases'), { recursive: true })
  writeFileSync(join(skill, 'SKILL.md'), '---\nname: other-skill\n---\n')
  writeFileSync(join(skill, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\ncases:\n  files: []\n')
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
    review: { status: 'approved' },
  }

  assert.throws(() => stageCandidateCase(workspace, 'other-skill', observation), /belongs to other-skill/)
})

test('candidate staging preserves indentationless case file sequences', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-observer-indentationless-'))
  const skill = join(workspace, 'demo-skill')
  mkdirSync(join(skill, 'evals', 'cases'), { recursive: true })
  writeFileSync(join(skill, 'SKILL.md'), '---\nname: demo-skill\n---\n')
  const evalPath = join(skill, 'evals', 'eval.yaml')
  writeFileSync(evalPath, [
    'schema_version: v1alpha1',
    'cases:',
    '  files:',
    '  - evals/cases/existing.yaml',
    '  defaults:',
    '    timeout_seconds: 120',
    '',
  ].join('\n'))
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
    review: { status: 'approved' },
  }

  const staged = stageCandidateCase(workspace, 'demo-skill', observation)
  assert.match(readFileSync(evalPath, 'utf8'), /  - evals\/cases\/observed-demo-skill-01234567\.yaml\n  defaults:/)
  staged.commit()
})

test('candidate staging appends after an eval file without a trailing newline', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-observer-no-final-newline-'))
  const skill = join(workspace, 'demo-skill')
  mkdirSync(join(skill, 'evals', 'cases'), { recursive: true })
  writeFileSync(join(skill, 'SKILL.md'), '---\nname: demo-skill\n---\n')
  const evalPath = join(skill, 'evals', 'eval.yaml')
  writeFileSync(evalPath, 'schema_version: v1alpha1\ncases:\n  files:\n    - evals/cases/existing.yaml')
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
    review: { status: 'approved' },
  }

  const staged = stageCandidateCase(workspace, 'demo-skill', observation)
  assert.match(
    readFileSync(evalPath, 'utf8'),
    /    - evals\/cases\/existing\.yaml\n    - evals\/cases\/observed-demo-skill-01234567\.yaml\n$/,
  )
  staged.commit()
})

test('candidate staging accepts CRLF Skill frontmatter', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-observer-crlf-'))
  const skill = join(workspace, 'demo-skill')
  mkdirSync(join(skill, 'evals', 'cases'), { recursive: true })
  writeFileSync(join(skill, 'SKILL.md'), '---\r\nname: demo-skill\r\n---\r\n')
  writeFileSync(join(skill, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\ncases:\n  files: []\n')
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
    review: { status: 'approved' },
  }

  const staged = stageCandidateCase(workspace, 'demo-skill', observation)
  assert.equal(existsSync(staged.casePath), true)
  staged.commit()
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
  const result = redact('TOKEN=secret-value PASSWORD="correct horse battery staple" AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI')
  assert.equal(result.text, '[REDACTED] [REDACTED] [REDACTED]')
  assert.deepEqual(result.categories, ['secret_assignment'])
})
