import assert from 'node:assert/strict'
import test from 'node:test'
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs'
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
  assert.deepEqual(inject, ['tools', 'skills', 'subprocess', 'jobs'])
  assert.deepEqual(tools.map((tool) => tool.name), [
    'skill_up_validate',
    'skill_up_run',
    'skill_up_summary',
  ])
  assert.equal(skills.length, 1)
  assert.equal(skills[0].name, 'skill-upper')
  assert.match(skills[0].content, /user-provided new\s+input or scenario as regression evidence/)
  assert.match(skills[0].content, /Base\s+improvements on gaps revealed by the supplied scenario/)
})

test('tools wait for the managed process range to become quiescent', async () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-plugin-'))
  mkdirSync(join(workspace, 'evals'))
  writeFileSync(join(workspace, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\n')

  const tools = []
  const waits = []
  let jobHooks
  const handle = () => ({
    collected: {
      stdout: { readFrom: () => ({ text: '', nextOffset: 0, lossy: false }) },
      stderr: { readFrom: () => ({ text: '', nextOffset: 0, lossy: false }) },
    },
    done: Promise.resolve({ exitCode: 0 }),
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
      spawn() { return handle() },
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
  assert.equal((await jobHooks.done).status, 'completed')
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
