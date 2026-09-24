import assert from 'node:assert/strict'
import test from 'node:test'
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { apply, inject, name } from '../index.js'

test('plugin registers the bundled skill and structured tools', () => {
  const tools = []
  const skills = []
  const ctx = {
    tools: { register(tool) { tools.push(tool) } },
    skills: { register(skill) { skills.push(skill) } },
    subprocess: {},
    jobs: {},
  }

  apply(ctx)

  assert.equal(name, 'dsh-skill-up')
  assert.deepEqual(inject, ['tools', 'skills', 'subprocess', 'jobs', 'sessions'])
  assert.deepEqual(tools.map((tool) => tool.name), [
    'skill_up_validate',
    'skill_up_run',
    'skill_up_summary',
    'skill_up_compare',
  ])
  assert.equal(skills.length, 1)
  assert.equal(skills[0].name, 'skill-upper')
  assert.match(skills[0].content, /user-provided new\s+input or scenario as regression evidence/)
  assert.match(skills[0].content, /Base\s+improvements on gaps revealed by the supplied scenario/)
})

test('observer tools and durable event listener are opt-in', async () => {
  const tools = []
  const listeners = new Map()
  const dataDir = mkdtempSync(join(tmpdir(), 'dsh-skill-up-observer-plugin-'))
  const ctx = {
    tools: { register(tool) { tools.push(tool) } },
    skills: { register() {} },
    subprocess: {},
    jobs: {},
    sessions: {},
    on(name, listener, options) { listeners.set(name, { listener, options }) },
  }

  apply(ctx, { observer: { enabled: true, dataDir } })

  assert.equal(listeners.has('session/event'), true)
  assert.deepEqual(listeners.get('session/event').options, { global: true })
  assert.deepEqual(tools.slice(0, 8).map((tool) => tool.name), [
    'list_skill_observations',
    'get_skill_observation',
    'collect_skill_feedback',
    'record_observation_feedback',
    'link_observation_report',
    'review_skill_observation',
    'preview_observation_case',
    'write_observation_case',
  ])
  assert.deepEqual(tools.slice(8).map((tool) => tool.name), [
    'skill_up_validate',
    'skill_up_run',
    'skill_up_summary',
    'skill_up_compare',
  ])

  const session = { id: 'plugin-session' }
  const observe = listeners.get('session/event').listener
  observe(session, { type: 'turn/start', data: { turn: 1 } })
  observe(session, {
    type: 'user/message',
    data: { source: { kind: 'user' }, content: [{ type: 'text', text: 'Use /demo-skill' }] },
  })
  observe(session, {
    type: 'user/message',
    data: { source: { kind: 'skill-invocation', name: 'demo-skill' }, content: [] },
  })
  observe(session, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  const listed = await tools.find((tool) => tool.name === 'list_skill_observations').execute({}, {})
  assert.equal(listed.observations.length, 1)
  assert.equal(listed.observations[0].skill, 'demo-skill')
  observe(session, { type: 'turn/start', data: { turn: 2 } })
  observe(session, {
    type: 'user/message',
    data: { source: { kind: 'user' }, content: [{ type: 'text', text: 'The count included dependencies.' }] },
  })
  observe(session, { type: 'turn/end', data: { turn: 2, reason: { kind: 'completed' } } })
  const collected = await tools.find((tool) => tool.name === 'collect_skill_feedback').execute({ skill_name: 'demo-skill' }, {})
  assert.equal(collected.observations[0].followup_candidates[0].text, 'The count included dependencies.')
})

test('approved observation case write rolls back failed validation', async () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-observer-write-'))
  const dataDir = join(workspace, '.observer')
  mkdirSync(join(workspace, 'skill', 'evals', 'cases'), { recursive: true })
  writeFileSync(join(workspace, 'skill', 'SKILL.md'), '---\nname: demo-skill\n---\n')
  const evalPath = join(workspace, 'skill', 'evals', 'eval.yaml')
  const original = 'schema_version: v1alpha1\ncases:\n  files: []\n'
  writeFileSync(evalPath, original)
  const tools = []
  let listener
  let exitCode = 1
  const ctx = {
    tools: { register(tool) { tools.push(tool) } },
    skills: { register() {} },
    sessions: {},
    jobs: {},
    on(_name, value) { listener = value },
    subprocess: {
      async resolveExecutable() { return '/usr/local/bin/skill-up' },
      spawn() {
        return {
          collected: {
            stdout: { readFrom: () => ({ text: exitCode === 0 ? 'valid' : '', lossy: false }) },
            stderr: { readFrom: () => ({ text: exitCode === 0 ? '' : 'invalid suite', lossy: false }) },
          },
          done: Promise.resolve({ exitCode }),
          terminate() {},
          async waitForExit() {},
        }
      },
    },
  }
  apply(ctx, { observer: { enabled: true, dataDir } })
  const session = { id: 'write-session' }
  listener(session, { type: 'turn/start', data: { turn: 1 } })
  listener(session, {
    type: 'user/message',
    data: { source: { kind: 'user' }, content: [{ type: 'text', text: 'Use /demo-skill' }] },
  })
  listener(session, {
    type: 'user/message',
    data: { source: { kind: 'skill-invocation', name: 'demo-skill' }, content: [] },
  })
  listener(session, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  const listTool = tools.find((tool) => tool.name === 'list_skill_observations')
  const reviewTool = tools.find((tool) => tool.name === 'review_skill_observation')
  const writeTool = tools.find((tool) => tool.name === 'write_observation_case')
  const feedbackTool = tools.find((tool) => tool.name === 'record_observation_feedback')
  const [{ id }] = (await listTool.execute({}, {})).observations
  await reviewTool.execute({ observation_id: id, status: 'approved' }, {})
  const exec = { agent: { session: { header: { cwd: workspace } } } }

  await assert.rejects(
    writeTool.execute({ observation_id: id, skill_root: 'skill' }, exec),
    /invalid suite/,
  )
  assert.equal(readFileSync(evalPath, 'utf8'), original)
  assert.deepEqual(readdirSync(join(workspace, 'skill', 'evals', 'cases')), [])

  exitCode = 0
  let releaseValidation
  ctx.subprocess.spawn = () => ({
    collected: {
      stdout: { readFrom: () => ({ text: 'valid', lossy: false }) },
      stderr: { readFrom: () => ({ text: '', lossy: false }) },
    },
    done: new Promise((resolve) => { releaseValidation = () => resolve({ exitCode: 0 }) }),
    terminate() {},
    async waitForExit() {},
  })
  const staleWrite = writeTool.execute({ observation_id: id, skill_root: 'skill' }, exec)
  await new Promise((resolve) => setImmediate(resolve))
  await feedbackTool.execute({ observation_id: id, sentiment: 'negative', comment: 'Needs revision' }, {})
  releaseValidation()
  await assert.rejects(staleWrite, /approval changed/)
  assert.equal(readFileSync(evalPath, 'utf8'), original)
  assert.deepEqual(readdirSync(join(workspace, 'skill', 'evals', 'cases')), [])

  await reviewTool.execute({ observation_id: id, status: 'approved' }, {})
  ctx.subprocess.spawn = () => ({
    collected: {
      stdout: { readFrom: () => ({ text: 'valid', lossy: false }) },
      stderr: { readFrom: () => ({ text: '', lossy: false }) },
    },
    done: Promise.resolve({ exitCode: 0 }),
    terminate() {},
    async waitForExit() {},
  })
  const result = await writeTool.execute({ observation_id: id, skill_root: 'skill' }, exec)
  assert.equal(result.validation, 'passed')
  assert.equal(readdirSync(join(workspace, 'skill', 'evals', 'cases')).length, 1)

  const baselinePath = join(workspace, 'baseline-result.json')
  const postChangePath = join(workspace, 'post-change-result.json')
  writeFileSync(baselinePath, JSON.stringify({
    case_results: [{ case_id: 'observed-case', configuration: 'with_skill', status: 'FAIL' }],
  }))
  writeFileSync(postChangePath, JSON.stringify({
    case_results: [{ case_id: 'observed-case', configuration: 'with_skill', status: 'PASS' }],
  }))
  const linkTool = tools.find((tool) => tool.name === 'link_observation_report')
  await linkTool.execute({ observation_id: id, phase: 'baseline', result_path: 'baseline-result.json' }, exec)
  await linkTool.execute({ observation_id: id, phase: 'post_change', result_path: 'post-change-result.json' }, exec)
  const getTool = tools.find((tool) => tool.name === 'get_skill_observation')
  const linked = await getTool.execute({ observation_id: id }, {})
  assert.deepEqual(linked.evidence.slice(-2).map((item) => item.kind), [
    'skill_up_baseline_report',
    'skill_up_post_change_report',
  ])

  const compareTool = tools.find((tool) => tool.name === 'skill_up_compare')
  const comparison = await compareTool.execute({
    before_result_path: 'baseline-result.json',
    after_result_path: 'post-change-result.json',
  }, exec)
  assert.deepEqual(comparison.cases, [
    { id: 'observed-case', before: 'FAIL', after: 'PASS', changed: true },
  ])
})

test('tools wait for the managed process range to become quiescent', async () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-plugin-'))
  mkdirSync(join(workspace, 'evals'))
  writeFileSync(join(workspace, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\n')

  const tools = []
  const waits = []
  let jobHooks
  let spawnCount = 0
  const handle = (exitCode) => ({
    collected: {
      stdout: { readFrom: () => ({ text: '', nextOffset: 0, lossy: false }) },
      stderr: { readFrom: () => ({ text: '', nextOffset: 0, lossy: false }) },
    },
    done: Promise.resolve({ exitCode }),
    terminate() {},
    async waitForExit() {
      waits.push('waited')
      return true
    },
  })
  const ctx = {
    tools: { register(tool) { tools.push(tool) } },
    skills: { register() {} },
    subprocess: {
      async resolveExecutable() { return '/usr/local/bin/skill-up' },
      spawn() {
        spawnCount += 1
        return handle(spawnCount === 1 ? 0 : 1)
      },
    },
    jobs: {
      start(spec) {
        jobHooks = spec.run()
        return 'job-1'
      },
    },
  }
  apply(ctx)
  const exec = { agent: { session: { header: { cwd: workspace } } } }

  const validation = await tools[0].execute({ eval_path: 'evals/eval.yaml' }, exec)
  assert.equal(validation.exit_code, 0)
  const run = await tools[1].execute({ eval_path: 'evals/eval.yaml' }, exec)
  assert.equal(run.job_id, 'job-1')
  assert.match(run.output_dir, /^evals\/\.skill-up-workspace\/[0-9a-f-]{36}$/)
  assert.deepEqual(await jobHooks.done, { status: 'completed', detail: 'exit code: 1' })
  assert.equal(waits.length, 2)
})

test('tools clean up the managed process range when outcome observation fails', async () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-plugin-failure-'))
  mkdirSync(join(workspace, 'evals'))
  writeFileSync(join(workspace, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\n')

  const tools = []
  let jobHooks
  let terminations = 0
  let waits = 0
  const failingHandle = () => ({
    collected: {},
    done: Promise.reject(new Error('provider failed')),
    terminate() { terminations += 1 },
    async waitForExit() {
      waits += 1
      return true
    },
  })
  const ctx = {
    tools: { register(tool) { tools.push(tool) } },
    skills: { register() {} },
    subprocess: {
      async resolveExecutable() { return '/usr/local/bin/skill-up' },
      spawn() { return failingHandle() },
    },
    jobs: {
      start(spec) {
        jobHooks = spec.run()
        return 'job-2'
      },
    },
  }
  apply(ctx)
  const exec = { agent: { session: { header: { cwd: workspace } } } }

  await assert.rejects(
    tools[0].execute({ eval_path: 'evals/eval.yaml' }, exec),
    /provider failed/,
  )
  await tools[1].execute({ eval_path: 'evals/eval.yaml' }, exec)
  assert.deepEqual(await jobHooks.done, { status: 'failed', detail: 'process error: provider failed' })
  assert.equal(terminations, 2)
  assert.equal(waits, 2)
})

test('tools terminate the managed process range when quiescence confirmation fails', async () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-plugin-quiescence-'))
  mkdirSync(join(workspace, 'evals'))
  writeFileSync(join(workspace, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\n')

  const tools = []
  let terminations = 0
  let waits = 0
  const ctx = {
    tools: { register(tool) { tools.push(tool) } },
    skills: { register() {} },
    subprocess: {
      async resolveExecutable() { return '/usr/local/bin/skill-up' },
      spawn() {
        return {
          collected: {},
          done: Promise.resolve({ exitCode: 0 }),
          terminate() { terminations += 1 },
          async waitForExit() {
            waits += 1
            if (waits === 1) throw new Error('quiescence unavailable')
            return true
          },
        }
      },
    },
    jobs: {},
  }
  apply(ctx)
  const exec = { agent: { session: { header: { cwd: workspace } } } }

  await assert.rejects(
    tools[0].execute({ eval_path: 'evals/eval.yaml' }, exec),
    /quiescence unavailable/,
  )
  assert.equal(terminations, 1)
  assert.equal(waits, 2)
})
