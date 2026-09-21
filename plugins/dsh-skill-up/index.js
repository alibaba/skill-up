import { randomUUID } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineTool } from '@deepseek-ai/dsh-tools'
import {
  buildRunArgs,
  forwardedEnvironment,
  prepareOutputDirectory,
  resolveWorkspaceFile,
  splitSkillDocument,
  summarizeResult,
} from './lib/core.js'

export const name = 'dsh-skill-up'
export const inject = ['tools', 'skills', 'subprocess', 'jobs']

const OUTPUT_LIMIT_BYTES = 1024 * 1024
const SPILL_LIMIT_BYTES = 16 * 1024 * 1024
const TOOL_GUIDANCE = `
## DeepSeek Harness integration

When the skill_up_validate, skill_up_run, and skill_up_summary tools are available,
prefer them over assembling equivalent shell commands. Treat a user-provided new
input or scenario as regression evidence for an existing Skill: add or refine a
case, run a baseline before editing the Skill, improve the Skill from observed
failures, and rerun the same case. Preserve the before/after reports. Base
improvements on gaps revealed by the supplied scenario, and never claim improvement
from process exit alone; inspect the per-case status in result.json.
`

function toolOutput(schema) {
  return {
    schema,
    render: (_args, value) => [{ type: 'text', text: JSON.stringify(value, null, 2) }],
  }
}

function collectOutput(handle) {
  const stdout = handle.collected.stdout?.readFrom(0)
  const stderr = handle.collected.stderr?.readFrom(0)
  return {
    stdout: stdout?.text || '',
    stderr: stderr?.text || '',
    truncated: Boolean(stdout?.lossy || stderr?.lossy),
  }
}

async function awaitProcess(handle) {
  try {
    const outcome = await handle.done
    await handle.waitForExit()
    return outcome
  } catch (error) {
    handle.terminate()
    try {
      await handle.waitForExit()
    } catch (cleanupError) {
      throw new AggregateError([error, cleanupError], 'subprocess failed and cleanup could not be confirmed')
    }
    throw error
  }
}

function createJobHooks(handle) {
  let stdoutOffset = 0
  let stderrOffset = 0
  return {
    cancel() {
      handle.terminate()
    },
    done: awaitProcess(handle).then((outcome) => {
      return {
        status: outcome.signal ? 'killed' : 'completed',
        detail: outcome.exitCode === 0
          ? 'evaluation completed'
          : outcome.signal
            ? `signal: ${outcome.signal}`
            : `exit code: ${outcome.exitCode}`,
      }
    }).catch((error) => ({
      status: 'failed',
      detail: `process error: ${error instanceof Error ? error.message : String(error)}`,
    })),
    readOutput() {
      const stdout = handle.collected.stdout?.readFrom(stdoutOffset)
      const stderr = handle.collected.stderr?.readFrom(stderrOffset)
      stdoutOffset = stdout?.nextOffset ?? stdoutOffset
      stderrOffset = stderr?.nextOffset ?? stderrOffset
      const parts = []
      if (stdout?.text) parts.push(stdout.text)
      if (stderr?.text) parts.push(`[stderr]\n${stderr.text}`)
      if (stdout?.lossy || stderr?.lossy) parts.push('[output truncated; inspect the generated report artifacts]')
      return parts.join('\n')
    },
  }
}

function spawnSpec(executable, args, workspace, env, signal) {
  return {
    argv: [executable, ...args],
    cwd: workspace,
    stdio: {
      stdin: 'ignore',
      stdout: { maxBytes: OUTPUT_LIMIT_BYTES, spill: { maxBytes: SPILL_LIMIT_BYTES } },
      stderr: { maxBytes: OUTPUT_LIMIT_BYTES, spill: { maxBytes: SPILL_LIMIT_BYTES } },
    },
    graceMs: 5000,
    ...(signal ? { signal } : {}),
    ...(Object.keys(env).length > 0 ? { env } : {}),
  }
}

export function apply(ctx, config = {}) {
  const skillUpBin = config.skillUpBin || 'skill-up'
  const credentialEnv = config.credentialEnv || []
  const skillFile = fileURLToPath(new URL('./dist/skill-upper/SKILL.md', import.meta.url))
  const skillDir = dirname(skillFile)
  const skillContent = splitSkillDocument(readFileSync(skillFile, 'utf8'))

  ctx.skills.register({
    name: 'skill-upper',
    description: 'Create, run, diagnose, and iteratively improve Agent Skill evaluations with skill-up.',
    source: 'bundled',
    resourceBase: { kind: 'directory', path: skillDir },
    content: `${TOOL_GUIDANCE}\n${skillContent}`,
  })

  ctx.tools.register(defineTool({
    name: 'skill_up_validate',
    description: 'Validate an existing skill-up evaluation suite before running it.',
    parameters: {
      eval_path: { type: 'string', description: 'Workspace-relative eval.yaml path. Defaults to evals/eval.yaml.' },
    },
    output: toolOutput({
      type: 'object',
      additionalProperties: false,
      properties: {
        exit_code: { type: 'integer', required: true },
        stdout: { type: 'string', required: true },
        stderr: { type: 'string', required: true },
        truncated: { type: 'boolean', required: true },
      },
    }),
    async execute(args, exec) {
      const workspace = exec.agent?.session.header.cwd || process.cwd()
      const evalFile = resolveWorkspaceFile(workspace, args.eval_path, 'eval_path')
      const env = forwardedEnvironment(credentialEnv)
      const executable = await ctx.subprocess.resolveExecutable(skillUpBin, env, exec.signal)
      const handle = ctx.subprocess.spawn(spawnSpec(executable, ['validate', evalFile.relative], workspace, env, exec.signal))
      const outcome = await awaitProcess(handle)
      return { exit_code: outcome.exitCode ?? -1, ...collectOutput(handle) }
    },
  }))

  ctx.tools.register(defineTool({
    name: 'skill_up_run',
    description: 'Start a skill-up evaluation as a background job. Use job_output to collect progress and completion.',
    parameters: {
      eval_path: { type: 'string', description: 'Workspace-relative eval.yaml path. Defaults to evals/eval.yaml.' },
      engine: { type: 'string', description: 'Optional Agent Engine override.' },
      provider: { type: 'string', description: 'Optional model provider override.' },
      model: { type: 'string', description: 'Optional model override.' },
      runtime: { type: 'string', description: 'Optional runtime override: none, opensandbox, or docker.' },
      include_case_name: {
        type: 'array',
        items: { type: 'string' },
        description: 'Optional case-name glob filters. Repeatable values are passed independently.',
      },
      iteration: {
        type: 'integer',
        description: 'Optional non-negative repeat count. Omit it or use 0 to run once and append the next available iteration without overwriting preserved reports.',
      },
      dry_run: { type: 'boolean', description: 'Validate and show what would run without executing agents.' },
    },
    output: toolOutput({
      type: 'object',
      additionalProperties: false,
      properties: {
        job_id: { type: 'string', required: true },
        eval_path: { type: 'string', required: true },
        output_dir: { type: 'string', required: true },
      },
    }),
    async execute(args, exec) {
      const workspace = exec.agent?.session.header.cwd || process.cwd()
      const evalFile = resolveWorkspaceFile(workspace, args.eval_path, 'eval_path')
      const env = forwardedEnvironment(credentialEnv)
      const executable = await ctx.subprocess.resolveExecutable(skillUpBin, env, exec.signal)
      const outputDir = prepareOutputDirectory(workspace, evalFile.absolute, randomUUID())
      const cliArgs = buildRunArgs(evalFile.relative, outputDir, args)
      const jobId = ctx.jobs.start({
        kind: 'skill-up',
        label: `skill-up run ${evalFile.relative}`,
        outputLimitBytes: OUTPUT_LIMIT_BYTES,
        ...(exec.agent ? { owner: exec.agent } : {}),
        run() {
          const handle = ctx.subprocess.spawn(spawnSpec(executable, cliArgs, workspace, env))
          return createJobHooks(handle)
        },
      })
      return { job_id: String(jobId), eval_path: evalFile.relative, output_dir: outputDir }
    },
  }))

  ctx.tools.register(defineTool({
    name: 'skill_up_summary',
    description: 'Read per-case statuses from an existing skill-up result.json report.',
    parameters: {
      result_path: { type: 'string', required: true, description: 'Workspace-relative path to result.json.' },
    },
    output: toolOutput({
      type: 'object',
      additionalProperties: false,
      properties: {
        result_path: { type: 'string', required: true },
        total: { type: 'integer', required: true },
        passed: { type: 'integer', required: true },
        failed: { type: 'integer', required: true },
        errors: { type: 'integer', required: true },
        skipped: { type: 'integer', required: true },
        other: { type: 'integer', required: true },
        cases: {
          type: 'array',
          required: true,
          items: {
            type: 'object',
            additionalProperties: false,
            properties: {
              id: { type: 'string', required: true },
              title: { type: 'string', required: true },
              configuration: { type: 'string', required: true },
              status: { type: 'string', required: true },
            },
          },
        },
      },
    }),
    async execute(args, exec) {
      const workspace = exec.agent?.session.header.cwd || process.cwd()
      const resultFile = resolveWorkspaceFile(workspace, args.result_path, 'result_path')
      const document = JSON.parse(readFileSync(resultFile.absolute, 'utf8'))
      return summarizeResult(document, resultFile.relative)
    },
  }))
}
