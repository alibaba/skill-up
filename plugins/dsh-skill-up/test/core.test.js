import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readdirSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import {
  buildRunArgs,
  forwardedEnvironment,
  prepareOutputDirectory,
  resolveWorkspaceFile,
  splitSkillDocument,
  summarizeResult,
} from '../lib/core.js'

test('buildRunArgs maps structured options without shell interpolation', () => {
  assert.deepEqual(buildRunArgs('skill/evals/eval.yaml', '.skill-up-workspace/run-1', {
    engine: 'codex',
    provider: 'openai',
    model: 'gpt-5',
    runtime: 'none',
    include_case_name: ['new-input-*', 'regression'],
    iteration: 0,
    dry_run: true,
  }), [
    'run', 'skill/evals/eval.yaml',
    '--output-dir', '.skill-up-workspace/run-1',
    '--engine', 'codex',
    '--provider', 'openai',
    '--model', 'gpt-5',
    '--runtime', 'none',
    '--include-case-name', 'new-input-*',
    '--include-case-name', 'regression',
    '--iteration', '0',
    '--dry-run',
    '--format', 'json',
    '--format', 'html',
  ])
})

test('buildRunArgs rejects invalid iteration counts before execution', () => {
  assert.throws(
    () => buildRunArgs('skill/evals/eval.yaml', '.skill-up-workspace/run-1', { iteration: -1 }),
    /non-negative integer repeat count/,
  )
  assert.throws(
    () => buildRunArgs('skill/evals/eval.yaml', '.skill-up-workspace/run-1', { iteration: 1.5 }),
    /non-negative integer repeat count/,
  )
})

test('resolveWorkspaceFile rejects files outside the workspace including symlinks', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-workspace-'))
  const outside = mkdtempSync(join(tmpdir(), 'dsh-skill-up-outside-'))
  mkdirSync(join(workspace, 'evals'))
  writeFileSync(join(workspace, 'evals', 'eval.yaml'), 'schema_version: v1alpha1\n')
  writeFileSync(join(outside, 'eval.yaml'), 'schema_version: v1alpha1\n')
  symlinkSync(join(outside, 'eval.yaml'), join(workspace, 'evals', 'linked.yaml'))

  assert.equal(resolveWorkspaceFile(workspace, 'evals/eval.yaml', 'eval_path').relative, 'evals/eval.yaml')
  assert.throws(() => resolveWorkspaceFile(workspace, '../eval.yaml', 'eval_path'), /inside the DSH workspace|ENOENT/)
  assert.throws(() => resolveWorkspaceFile(workspace, 'evals/linked.yaml', 'eval_path'), /inside the DSH workspace/)
})

test('resolveWorkspaceFile returns a canonical relative path for a symlinked workspace', () => {
  const parent = mkdtempSync(join(tmpdir(), 'dsh-skill-up-workspace-link-'))
  const workspace = join(parent, 'workspace')
  const link = join(parent, 'workspace-link')
  mkdirSync(workspace)
  writeFileSync(join(workspace, 'result.json'), '{}\n')
  symlinkSync(workspace, link)

  assert.equal(resolveWorkspaceFile(link, 'result.json', 'result_path').relative, 'result.json')
})

test('prepareOutputDirectory confines generated reports despite symlinks', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-output-workspace-'))
  mkdirSync(join(workspace, 'evals'))
  const evalPath = join(workspace, 'evals', 'eval.yaml')
  writeFileSync(evalPath, 'schema_version: v1alpha1\n')
  assert.equal(
    prepareOutputDirectory(workspace, evalPath, '00000000-0000-0000-0000-000000000001'),
    join('evals', '.skill-up-workspace', '00000000-0000-0000-0000-000000000001'),
  )

  const escapedWorkspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-output-escape-'))
  const outside = mkdtempSync(join(tmpdir(), 'dsh-skill-up-output-outside-'))
  mkdirSync(join(escapedWorkspace, 'evals'))
  const escapedEvalPath = join(escapedWorkspace, 'evals', 'eval.yaml')
  writeFileSync(escapedEvalPath, 'schema_version: v1alpha1\n')
  symlinkSync(outside, join(escapedWorkspace, 'evals', '.skill-up-workspace'))
  assert.throws(
    () => prepareOutputDirectory(escapedWorkspace, escapedEvalPath, '00000000-0000-0000-0000-000000000002'),
    /output root must stay inside the DSH workspace|output root must stay inside the evals root/,
  )
  assert.deepEqual(readdirSync(outside), [])
})

test('prepareOutputDirectory keeps reports below the excluded evals tree of the Skill root', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-nested-skill-'))
  const skillRoot = join(workspace, 'skills', 'demo')
  mkdirSync(join(skillRoot, 'benchmarks'), { recursive: true })
  writeFileSync(join(skillRoot, 'SKILL.md'), '# Demo\n')
  const evalPath = join(skillRoot, 'benchmarks', 'eval.yaml')
  writeFileSync(evalPath, 'schema_version: v1alpha1\n')

  assert.equal(
    prepareOutputDirectory(workspace, evalPath, 'run-1'),
    join('skills', 'demo', 'evals', '.skill-up-workspace', 'run-1'),
  )
})

test('prepareOutputDirectory checks a workspace-root Skill before using the fallback', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'dsh-skill-up-root-skill-'))
  mkdirSync(join(workspace, 'custom', 'bench'), { recursive: true })
  writeFileSync(join(workspace, 'SKILL.md'), '# Root Skill\n')
  const evalPath = join(workspace, 'custom', 'bench', 'eval.yaml')
  writeFileSync(evalPath, 'schema_version: v1alpha1\n')

  assert.equal(
    prepareOutputDirectory(workspace, evalPath, 'run-1'),
    join('evals', '.skill-up-workspace', 'run-1'),
  )
})

test('forwardedEnvironment only forwards explicitly configured names', () => {
  assert.deepEqual(
    forwardedEnvironment(['OPENAI_API_KEY'], { OPENAI_API_KEY: 'secret', OTHER: 'ignored' }),
    { OPENAI_API_KEY: 'secret' },
  )
  assert.throws(() => forwardedEnvironment(['BAD-NAME'], {}), /invalid environment variable name/)
})

test('summarizeResult reports per-case business status', () => {
  assert.deepEqual(summarizeResult({
    case_results: [
      { case_id: 'a', title: 'A', configuration: 'with_skill', status: 'PASS' },
      { case_id: 'a', title: 'A', configuration: 'without_skill', status: 'FAIL' },
      { case_id: 'b', title: 'B', configuration: 'with_skill', status: 'FAIL' },
      { case_id: 'c', title: 'C', configuration: 'with_skill', status: 'ERROR' },
    ],
  }, 'out/result.json'), {
    result_path: 'out/result.json',
    total: 3,
    passed: 1,
    failed: 1,
    errors: 1,
    skipped: 0,
    other: 0,
    cases: [
      { id: 'a', title: 'A', configuration: 'with_skill', status: 'PASS' },
      { id: 'b', title: 'B', configuration: 'with_skill', status: 'FAIL' },
      { id: 'c', title: 'C', configuration: 'with_skill', status: 'ERROR' },
    ],
  })
})

test('splitSkillDocument removes only the frontmatter block', () => {
  assert.equal(splitSkillDocument('---\nname: demo\n---\n\n# Body\n'), '\n# Body\n')
})
