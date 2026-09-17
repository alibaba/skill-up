import { mkdirSync, realpathSync, statSync } from 'node:fs'
import { isAbsolute, relative, resolve } from 'node:path'

const DEFAULT_EVAL_PATH = 'evals/eval.yaml'
export const OUTPUT_ROOT = '.skill-up-workspace'
const SAFE_ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

function relativeInside(root, candidate, label) {
  const rel = relative(root, candidate)
  if (rel === '..' || rel.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`) || isAbsolute(rel)) {
    throw new Error(`${label} must stay inside the DSH workspace`)
  }
  return rel || '.'
}

export function resolveWorkspaceFile(workspace, input, label) {
  const root = realpathSync(workspace)
  const requested = input || DEFAULT_EVAL_PATH
  if (isAbsolute(requested)) {
    throw new Error(`${label} must be relative to the DSH workspace`)
  }

  const candidate = realpathSync(resolve(root, requested))
  const rel = relativeInside(root, candidate, label)
  if (!statSync(candidate).isFile()) {
    throw new Error(`${label} must point to a file`)
  }
  return { absolute: candidate, relative: rel || '.' }
}

export function prepareOutputDirectory(workspace, runId) {
  const root = realpathSync(workspace)
  const outputRoot = resolve(root, OUTPUT_ROOT)
  mkdirSync(outputRoot, { recursive: true })
  const confinedOutputRoot = realpathSync(outputRoot)
  relativeInside(root, confinedOutputRoot, 'output root')

  const runDirectory = resolve(confinedOutputRoot, runId)
  mkdirSync(runDirectory)
  return relativeInside(root, realpathSync(runDirectory), 'output directory')
}

export function buildRunArgs(evalPath, outputDir, input = {}) {
  if (typeof outputDir !== 'string' || outputDir.length === 0) {
    throw new Error('outputDir must identify an isolated workspace-relative run directory')
  }
  const args = ['run', evalPath, '--output-dir', outputDir]
  for (const flag of ['engine', 'provider', 'model', 'runtime']) {
    const value = input[flag]
    if (typeof value === 'string' && value.length > 0) {
      args.push(`--${flag}`, value)
    }
  }
  for (const pattern of input.include_case_name || []) {
    args.push('--include-case-name', pattern)
  }
  if (input.iteration !== undefined) {
    if (!Number.isInteger(input.iteration) || input.iteration < 0) {
      throw new Error('iteration must be a non-negative integer repeat count')
    }
    args.push('--iteration', String(input.iteration))
  }
  if (input.dry_run === true) {
    args.push('--dry-run')
  }
  args.push('--format', 'json', '--format', 'html')
  return args
}

export function forwardedEnvironment(names, source = process.env) {
  const env = {}
  for (const name of names || []) {
    if (!SAFE_ENV_NAME.test(name)) {
      throw new Error(`credentialEnv contains an invalid environment variable name: ${JSON.stringify(name)}`)
    }
    if (typeof source[name] === 'string') {
      env[name] = source[name]
    }
  }
  return env
}

export function summarizeResult(document, resultPath) {
  if (!document || !Array.isArray(document.case_results)) {
    throw new Error('result file does not contain case_results')
  }
  const order = []
  const primary = new Map()
  for (const item of document.case_results) {
    const id = String(item.case_id || item.id || '')
    const current = primary.get(id)
    if (!primary.has(id)) order.push(id)
    if (item.configuration === 'with_skill' || !current || current.configuration === 'without_skill') {
      primary.set(id, item)
    }
  }
  const cases = order.map((id) => primary.get(id)).map((item) => ({
    id: String(item.case_id || item.id || ''),
    title: String(item.title || ''),
    configuration: String(item.configuration || ''),
    status: String(item.status || 'UNKNOWN'),
  }))
  const counts = { PASS: 0, FAIL: 0, ERROR: 0, SKIP: 0, OTHER: 0 }
  for (const item of cases) {
    if (Object.hasOwn(counts, item.status)) counts[item.status] += 1
    else counts.OTHER += 1
  }
  return {
    result_path: resultPath,
    total: cases.length,
    passed: counts.PASS,
    failed: counts.FAIL,
    errors: counts.ERROR,
    skipped: counts.SKIP,
    other: counts.OTHER,
    cases,
  }
}

export function splitSkillDocument(markdown) {
  if (!markdown.startsWith('---\n')) return markdown
  const boundary = markdown.indexOf('\n---\n', 4)
  if (boundary < 0) throw new Error('bundled skill-upper has malformed frontmatter')
  return markdown.slice(boundary + 5)
}
