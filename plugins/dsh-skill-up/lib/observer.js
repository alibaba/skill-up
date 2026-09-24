import { createHash, randomUUID } from 'node:crypto'
import {
  chmodSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import { homedir } from 'node:os'
import { dirname, isAbsolute, join, relative, resolve } from 'node:path'

const SCHEMA_VERSION = 'v1alpha1'
const OBSERVATION_ID = /^obs_[a-f0-9]{24}$/
const SKILL_NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/
const CONTROL_SKILLS = new Set(['skill-upper', 'skill-up-observer', 'codex-skill-up', 'dsh-skill-up'])
const REVIEW_STATUSES = new Set(['candidate', 'approved', 'rejected'])
const FEEDBACK_SENTIMENTS = new Set(['', 'positive', 'negative', 'mixed', 'neutral'])
const FOLLOWUP_WINDOW_MS = 60 * 60 * 1000

const REDACTION_RULES = [
  ['bearer_token', /\bbearer\s+[a-z0-9._~+/=-]{12,}/giu],
  ['jwt', /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/gu],
  ['provider_key', /\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,}|AIza[A-Za-z0-9_-]{20,})\b/gu],
  ['aws_access_key', /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/gu],
  [
    'secret_assignment',
    /\b(?:[A-Za-z_][A-Za-z0-9_-]*)?(?:api[_-]?key|token|secret|password|passwd|pwd)[A-Za-z0-9_-]*\s*[:=]\s*(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s,;'"`]+)/giu,
  ],
]

function utcNow() {
  return new Date().toISOString()
}

function ensurePrivateDirectory(path) {
  mkdirSync(path, { recursive: true, mode: 0o700 })
  try { chmodSync(path, 0o700) } catch {}
}

function atomicWriteJson(path, value) {
  ensurePrivateDirectory(dirname(path))
  const temporary = join(dirname(path), `.observer-${randomUUID()}.json`)
  try {
    writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, { encoding: 'utf8', mode: 0o600, flag: 'wx' })
    renameSync(temporary, path)
    chmodSync(path, 0o600)
  } finally {
    rmSync(temporary, { force: true })
  }
}

function withFileLock(path, operation) {
  const lockPath = `${path}.lock`
  try {
    mkdirSync(lockPath, { mode: 0o700 })
  } catch (error) {
    if (error?.code === 'EEXIST') throw new Error(`observation is already being updated: ${path}`)
    throw error
  }
  try {
    return operation()
  } finally {
    rmSync(lockPath, { recursive: true, force: true })
  }
}

function relativeInside(root, candidate, label) {
  const rel = relative(root, candidate)
  if (rel === '..' || rel.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`) || isAbsolute(rel)) {
    throw new Error(`${label} must stay inside the DSH workspace`)
  }
  return rel || '.'
}

function textContent(content) {
  if (!Array.isArray(content)) return ''
  return content
    .filter((item) => item && item.type === 'text' && typeof item.text === 'string')
    .map((item) => item.text)
    .join('\n')
}

export function redact(value) {
  let text = value == null ? '' : String(value)
  const categories = []
  for (const [name, pattern] of REDACTION_RULES) {
    pattern.lastIndex = 0
    if (pattern.test(text)) {
      pattern.lastIndex = 0
      text = text.replace(pattern, '[REDACTED]')
      categories.push(name)
    }
  }
  return { text, categories }
}

function mergeStrings(...groups) {
  return [...new Set(groups.flat())]
}

export function validateObservation(observation) {
  if (observation?.schema_version !== SCHEMA_VERSION) throw new Error('schema_version must be v1alpha1')
  if (!OBSERVATION_ID.test(String(observation?.id || ''))) throw new Error('invalid observation id')
  if (!['codex', 'dsh'].includes(observation?.host?.name)) throw new Error('host.name must be codex or dsh')
  if (!SKILL_NAME.test(String(observation?.skill?.name || ''))) throw new Error('invalid skill.name')
  if (!['explicit', 'instrumented'].includes(observation?.attribution?.method)) {
    throw new Error('attribution.method must be explicit or instrumented')
  }
  if (!String(observation?.input?.text || '')) throw new Error('input.text is required for replayable candidate cases')
  if (!['completed', 'interrupted'].includes(observation?.outcome?.status)) throw new Error('invalid outcome.status')
  if (!String(observation?.correlation?.session_id || '')) throw new Error('correlation.session_id is required')
  if (observation?.privacy?.storage !== 'local') throw new Error('privacy.storage must be local')
  if (!REVIEW_STATUSES.has(observation?.review?.status)) throw new Error('invalid review.status')
  if (observation.feedback && !FEEDBACK_SENTIMENTS.has(observation.feedback.sentiment || '')) {
    throw new Error('invalid feedback.sentiment')
  }
  if (observation.followups && (!Array.isArray(observation.followups) || observation.followups.some((item) => (
    !String(item?.turn_id || '') || !String(item?.text || '') || !String(item?.observed_at || '')
  )))) throw new Error('invalid followups')
}

function observationId(observation) {
  const material = [
    observation.host.name,
    observation.skill.name,
    observation.skill.version || '',
    observation.attribution.method,
    observation.input.text,
    observation.outcome.status,
    observation.outcome.final_message || '',
    observation.correlation.session_id,
    observation.correlation.turn_id || '',
  ].join('\0')
  return `obs_${createHash('sha256').update(material).digest('hex').slice(0, 24)}`
}

export function defaultObservationDirectory(configured, environment = process.env) {
  if (configured) return resolve(configured)
  return resolve(environment.DSH_HOME || join(homedir(), '.dsh'), 'plugin-data', 'skill-up-observer')
}

export class ObservationStore {
  constructor(root) {
    this.root = resolve(root)
  }

  path(id) {
    if (!OBSERVATION_ID.test(id)) throw new Error('invalid observation id')
    return join(this.root, `${id}.json`)
  }

  save(observation) {
    validateObservation(observation)
    ensurePrivateDirectory(this.root)
    const path = this.path(observation.id)
    const lockPath = `${path}.lock`
    try {
      mkdirSync(lockPath, { mode: 0o700 })
    } catch (error) {
      if (error?.code === 'EEXIST') return false
      throw error
    }
    try {
      if (existsSync(path)) return false
      atomicWriteJson(path, observation)
      return true
    } finally {
      rmSync(lockPath, { recursive: true, force: true })
    }
  }

  get(id) {
    const observation = JSON.parse(readFileSync(this.path(id), 'utf8'))
    validateObservation(observation)
    return observation
  }

  list() {
    if (!existsSync(this.root)) return []
    return readdirSync(this.root)
      .filter((name) => /^obs_[a-f0-9]{24}\.json$/.test(name))
      .map((name) => this.get(name.slice(0, -5)))
      .sort((a, b) => b.timing.observed_at.localeCompare(a.timing.observed_at))
  }

  update(id, mutate) {
    const path = this.path(id)
    return withFileLock(path, () => {
      const observation = this.get(id)
      mutate(observation)
      validateObservation(observation)
      atomicWriteJson(path, observation)
      return observation
    })
  }

  review(id, status) {
    if (!['approved', 'rejected'].includes(status)) throw new Error('status must be approved or rejected')
    return this.update(id, (observation) => {
      observation.review = { status, reviewed_at: utcNow() }
    })
  }

  feedback(id, sentiment, comment) {
    if (!FEEDBACK_SENTIMENTS.has(sentiment || '')) throw new Error('invalid feedback sentiment')
    const redacted = redact(comment)
    return this.update(id, (observation) => {
      observation.feedback = { ...(sentiment ? { sentiment } : {}), ...(redacted.text ? { comment: redacted.text } : {}) }
      observation.privacy.redactions = mergeStrings(observation.privacy.redactions || [], redacted.categories)
      observation.review = { status: 'candidate' }
    })
  }

  addFollowup(id, turnId, text, observedAt) {
    const redacted = redact(text)
    if (!redacted.text) return this.get(id)
    return this.update(id, (observation) => {
      observation.followups ||= []
      if (observation.followups.some((item) => item.turn_id === String(turnId))) return
      observation.followups.push({ turn_id: String(turnId), text: redacted.text, observed_at: observedAt })
      observation.privacy.redactions = mergeStrings(observation.privacy.redactions || [], redacted.categories)
    })
  }

  addEvidence(id, evidence) {
    const kind = redact(evidence.kind)
    const reference = redact(evidence.ref)
    const summary = redact(evidence.summary)
    if (!kind.text) throw new Error('evidence kind is required')
    return this.update(id, (observation) => {
      if (observation.review.status !== 'approved') {
        throw new Error('observation must be approved before linking evaluation reports')
      }
      observation.evidence ||= []
      const item = { kind: kind.text }
      if (reference.text) item.ref = reference.text
      if (summary.text) item.summary = summary.text
      if (!observation.evidence.some((current) => (
        current.kind === item.kind && current.ref === item.ref && current.summary === item.summary
      ))) {
        observation.evidence.push(item)
      }
      observation.privacy.redactions = mergeStrings(
        observation.privacy.redactions || [],
        kind.categories,
        reference.categories,
        summary.categories,
      )
    })
  }

  commitApprovedCandidate(id, expectedYaml, operation) {
    const path = this.path(id)
    return withFileLock(path, () => {
      const observation = this.get(id)
      if (observation.review.status !== 'approved') {
        throw new Error('observation approval changed while validating the candidate case')
      }
      if (candidateCase(observation).yaml !== expectedYaml) {
        throw new Error('observation content changed while validating the candidate case')
      }
      return operation()
    })
  }
}

export class ObservationCollector {
  constructor(store, { adapterVersion = 'v1alpha1', hostVersion = '' } = {}) {
    this.store = store
    this.adapterVersion = adapterVersion
    this.hostVersion = hostVersion
    this.turns = new Map()
    this.calls = new Map()
    this.activeTurns = new Map()
  }

  key(session, turn) {
    return `${session.id}:${turn}`
  }

  callKey(session, callId) {
    return JSON.stringify([session.id, callId])
  }

  turn(session, turn) {
    const key = this.key(session, turn)
    if (!this.turns.has(key)) {
      this.turns.set(key, {
        prompt: '',
        finalMessage: '',
        skills: new Map(),
        redactions: [],
        observedAt: utcNow(),
      })
    }
    return this.turns.get(key)
  }

  handle(session, event) {
    if (event?.type === 'turn/start') {
      const turnNumber = event?.data?.turn
      if (!Number.isInteger(turnNumber)) return []
      this.activeTurns.set(session.id, turnNumber)
      this.turn(session, turnNumber)
      return []
    }
    const turnNumber = Number.isInteger(event?.data?.turn)
      ? event.data.turn
      : this.activeTurns.get(session.id)
    if (!Number.isInteger(turnNumber)) return []
    const turn = this.turn(session, turnNumber)

    if (event.type === 'user/message') {
      const source = event.data.source || {}
      if (source.kind === 'user' && !turn.prompt) {
        const value = redact(textContent(event.data.content))
        turn.prompt = value.text
        turn.redactions = mergeStrings(turn.redactions, value.categories)
      } else if (source.kind === 'skill-invocation') {
        this.mark(turn, source.name, 'explicit', `DSH user invocation loaded /${source.name}`)
      }
      return []
    }

    if (event.type === 'tool/call' && event.data.name === 'skill') {
      try {
        const name = JSON.parse(event.data.arguments).name
        if (SKILL_NAME.test(String(name || ''))) {
          this.calls.set(this.callKey(session, event.data.callId), { sessionId: session.id, turn: turnNumber, name })
        }
      } catch {}
      return []
    }

    if (event.type === 'tool/result') {
      const block = event.data.message?.content?.find((item) => item?.type === 'tool-result')
      const key = this.callKey(session, block?.toolCallId)
      const call = this.calls.get(key)
      if (call) {
        this.calls.delete(key)
        if (call.sessionId === session.id && call.turn === turnNumber && block.isError !== true) {
          this.mark(turn, call.name, 'instrumented', `DSH skill tool loaded ${call.name}`)
        }
      }
      return []
    }

    if (event.type === 'assistant/message') {
      const value = redact(textContent(event.data.message?.content))
      if (value.text) turn.finalMessage = value.text
      turn.redactions = mergeStrings(turn.redactions, value.categories)
      return []
    }

    if (event.type !== 'turn/end') return []
    this.activeTurns.delete(session.id)
    this.turns.delete(this.key(session, turnNumber))
    for (const [key, call] of this.calls) {
      if (call.sessionId === session.id && call.turn === turnNumber) this.calls.delete(key)
    }
    if (!turn.prompt) return []

    const previous = this.store.list().filter((item) => (
      item.correlation.session_id === String(session.id)
      && item.correlation.turn_id === String(turnNumber - 1)
      && item.outcome.status === 'completed'
      && Date.parse(turn.observedAt) - Date.parse(item.timing.completed_at) >= 0
      && Date.parse(turn.observedAt) - Date.parse(item.timing.completed_at) <= FOLLOWUP_WINDOW_MS
    ))
    if (previous.length === 1) {
      this.store.addFollowup(previous[0].id, turnNumber, turn.prompt, turn.observedAt)
    }
    if (turn.skills.size === 0) return []

    const saved = []
    for (const [name, attribution] of turn.skills) {
      const completed = event.data.reason?.kind === 'completed'
      const observation = {
        schema_version: SCHEMA_VERSION,
        id: '',
        host: {
          name: 'dsh',
          ...(this.hostVersion ? { version: this.hostVersion } : {}),
          adapter_version: this.adapterVersion,
        },
        skill: { name },
        attribution,
        input: { text: turn.prompt },
        outcome: {
          status: completed ? 'completed' : 'interrupted',
          ...(turn.finalMessage ? { final_message: turn.finalMessage } : {}),
        },
        evidence: [{ kind: 'dsh_session', ref: String(session.id), summary: `turn ${turnNumber}` }],
        correlation: { session_id: String(session.id), turn_id: String(turnNumber) },
        timing: { observed_at: turn.observedAt, completed_at: utcNow() },
        privacy: {
          storage: 'local',
          consent: 'dsh_plugin_observer_enabled',
          redactions: turn.redactions,
        },
        review: { status: 'candidate' },
      }
      observation.id = observationId(observation)
      if (this.store.save(observation)) saved.push(observation)
    }
    return saved
  }

  mark(turn, rawName, method, evidence) {
    const name = String(rawName || '').toLowerCase()
    if (!SKILL_NAME.test(name) || CONTROL_SKILLS.has(name)) return
    const current = turn.skills.get(name)
    if (!current || (current.method === 'instrumented' && method === 'explicit')) {
      turn.skills.set(name, { method, confidence: 1, evidence: [evidence] })
    }
  }
}

export function collectSkillFeedback(store, skillName, { offset = 0, limit = 20 } = {}) {
  if (!SKILL_NAME.test(String(skillName || ''))) throw new Error('invalid skill name')
  if (!Number.isInteger(offset) || offset < 0) throw new Error('offset must be a non-negative integer')
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) throw new Error('limit must be between 1 and 100')
  const observations = store.list().filter((item) => item.skill.name === skillName)
  const withFeedback = observations.filter((item) => item.feedback || item.followups?.length)
  return {
    skill: skillName,
    total_observations: observations.length,
    feedback_observations: withFeedback.length,
    next_offset: offset + limit < withFeedback.length ? offset + limit : null,
    observations: withFeedback.slice(offset, offset + limit).map((item) => ({
      id: item.id,
      observed_at: item.timing.observed_at,
      input: item.input.text,
      outcome: item.outcome,
      feedback: item.feedback || null,
      followup_candidates: item.followups || [],
    })),
  }
}

export function candidateCase(observation) {
  validateObservation(observation)
  const base = observation.skill.name.replaceAll('_', '-').replace(/[^a-z0-9-]+/g, '-').replace(/^-|-$/g, '') || 'skill'
  const id = `observed-${base}-${observation.id.slice(4, 12)}`
  let description = `Candidate regression case from local observation ${observation.id}. Review the prompt and add concrete expectations before relying on it as a release gate.`
  if (observation.feedback?.comment) description += ` User feedback: ${observation.feedback.comment}`
  const yaml = [
    `id: ${JSON.stringify(id)}`,
    `title: ${JSON.stringify(`Observed ${observation.skill.name} interaction`)}`,
    `description: ${JSON.stringify(description)}`,
    'tag: "functional_test"',
    'input:',
    `  prompt: ${JSON.stringify(observation.input.text)}`,
    '',
  ].join('\n')
  return { id, yaml }
}

function appendCaseReference(evalText, relativePath) {
  const lines = evalText.split(/(?<=\n)/)
  const casesIndex = lines.findIndex((line) => /^cases:\s*(?:#.*)?(?:\r?\n)?$/.test(line))
  if (casesIndex < 0) throw new Error('eval.yaml must contain a top-level cases mapping')
  let end = lines.length
  for (let i = casesIndex + 1; i < lines.length; i += 1) {
    if (lines[i].trim() && !lines[i].trim().startsWith('#') && lines[i].length === lines[i].trimStart().length) {
      end = i
      break
    }
  }
  let filesIndex = -1
  let filesIndent = 0
  let inline = ''
  for (let i = casesIndex + 1; i < end; i += 1) {
    const match = lines[i].match(/^(\s+)files:\s*(.*?)\s*(?:#.*)?(?:\r?\n)?$/)
    if (match) {
      filesIndex = i
      filesIndent = match[1].length
      inline = match[2]
      break
    }
  }
  if (filesIndex < 0) throw new Error('eval.yaml cases must contain files')
  if (inline === '[]') {
    const reference = `${' '.repeat(filesIndent + 2)}- ${relativePath}\n`
    lines[filesIndex] = `${' '.repeat(filesIndent)}files:\n`
    lines.splice(filesIndex + 1, 0, reference)
    return lines.join('')
  }
  if (inline) throw new Error('flow-style non-empty cases.files is not supported; use a block sequence')
  let filesEnd = end
  let itemIndent = filesIndent + 2
  let foundItem = false
  for (let i = filesIndex + 1; i < end; i += 1) {
    if (lines[i].trim() && !lines[i].trim().startsWith('#')) {
      const indent = lines[i].length - lines[i].trimStart().length
      if (lines[i].trimStart().startsWith('-')) {
        if (!foundItem) {
          itemIndent = indent
          foundItem = true
        }
      } else if (indent <= filesIndent) {
        filesEnd = i
        break
      }
    }
    if (lines[i].trim().startsWith('-') && lines[i].trim().slice(1).trim().replace(/^['"]|['"]$/g, '') === relativePath) {
      throw new Error(`eval.yaml already references ${relativePath}`)
    }
  }
  const separator = filesEnd === lines.length && lines.length > 0 && !lines.at(-1).endsWith('\n') ? '\n' : ''
  const reference = `${separator}${' '.repeat(itemIndent)}- ${relativePath}\n`
  lines.splice(filesEnd, 0, reference)
  return lines.join('')
}

export function stageCandidateCase(workspace, skillRootInput, observation) {
  if (observation.review.status !== 'approved') throw new Error('observation must be approved before writing a case')
  const root = realpathSync(workspace)
  if (isAbsolute(skillRootInput)) throw new Error('skill_root must be relative to the DSH workspace')
  const skillRoot = realpathSync(resolve(root, skillRootInput || '.'))
  relativeInside(root, skillRoot, 'skill_root')
  const skillDocumentPath = realpathSync(join(skillRoot, 'SKILL.md'))
  relativeInside(skillRoot, skillDocumentPath, 'SKILL.md')
  if (!statSync(skillDocumentPath).isFile()) throw new Error('skill_root must contain SKILL.md')
  const skillDocument = readFileSync(skillDocumentPath, 'utf8').replace(/\r\n?/g, '\n')
  const frontmatterEnd = skillDocument.startsWith('---\n') ? skillDocument.indexOf('\n---\n', 4) : -1
  const frontmatter = frontmatterEnd >= 0 ? skillDocument.slice(4, frontmatterEnd) : ''
  const nameMatch = frontmatter.match(/^name:\s*['"]?([a-z0-9][a-z0-9_-]{0,63})['"]?\s*(?:#.*)?$/mu)
  if (!nameMatch) throw new Error('SKILL.md must declare a valid frontmatter name')
  if (nameMatch[1] !== observation.skill.name) {
    throw new Error(`skill_root belongs to ${nameMatch[1]}, not ${observation.skill.name}`)
  }
  const evalPath = realpathSync(join(skillRoot, 'evals', 'eval.yaml'))
  relativeInside(skillRoot, evalPath, 'eval.yaml')
  const lockPath = join(dirname(evalPath), '.skill-up-observer.lock')
  try {
    mkdirSync(lockPath, { mode: 0o700 })
  } catch (error) {
    if (error?.code === 'EEXIST') throw new Error('another observation case write is already in progress')
    throw error
  }
  const casesDir = resolve(skillRoot, 'evals', 'cases')
  let casePath
  let caseCreated = false
  try {
    mkdirSync(casesDir, { recursive: true })
    relativeInside(skillRoot, realpathSync(casesDir), 'cases directory')

    const candidate = candidateCase(observation)
    const relativePath = `evals/cases/${candidate.id}.yaml`
    casePath = join(casesDir, `${candidate.id}.yaml`)
    const original = readFileSync(evalPath, 'utf8')
    const updated = appendCaseReference(original, relativePath)
    writeFileSync(casePath, candidate.yaml, { encoding: 'utf8', mode: 0o600, flag: 'wx' })
    caseCreated = true
    atomicWriteText(evalPath, updated)
    let closed = false
    const close = () => {
      if (closed) return
      rmSync(lockPath, { recursive: true, force: true })
      closed = true
    }
    return {
      casePath,
      evalPath,
      candidateYaml: candidate.yaml,
      relativeEvalPath: relativeInside(root, evalPath, 'eval.yaml'),
      commit: close,
      rollback() {
        if (closed) return
        atomicWriteText(evalPath, original)
        rmSync(casePath, { force: true })
        close()
      },
    }
  } catch (error) {
    if (caseCreated) rmSync(casePath, { force: true })
    rmSync(lockPath, { recursive: true, force: true })
    throw error
  }
}

function atomicWriteText(path, value) {
  const temporary = join(dirname(path), `.observer-${randomUUID()}`)
  const mode = statSync(path).mode & 0o777
  try {
    writeFileSync(temporary, value, { encoding: 'utf8', mode, flag: 'wx' })
    renameSync(temporary, path)
  } finally {
    rmSync(temporary, { force: true })
  }
}

export function compareResults(before, after, beforePath, afterPath) {
  const primary = (document) => {
    if (!Array.isArray(document?.case_results)) throw new Error('result file does not contain case_results')
    const selected = new Map()
    for (const item of document.case_results) {
      const id = String(item.case_id || item.id || '')
      const current = selected.get(id)
      if (item.configuration === 'with_skill' || !current || current.configuration === 'without_skill') selected.set(id, item)
    }
    return selected
  }
  const left = primary(before)
  const right = primary(after)
  const ids = [...new Set([...left.keys(), ...right.keys()])].sort()
  return {
    before_path: beforePath,
    after_path: afterPath,
    same_cases: [...left.keys()].sort().join('\0') === [...right.keys()].sort().join('\0'),
    cases: ids.map((id) => {
      const beforeStatus = String(left.get(id)?.status || 'MISSING')
      const afterStatus = String(right.get(id)?.status || 'MISSING')
      return { id, before: beforeStatus, after: afterStatus, changed: beforeStatus !== afterStatus }
    }),
  }
}
