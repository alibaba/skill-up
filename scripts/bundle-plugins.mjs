import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import { dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const canonicalSkill = resolve(repoRoot, 'skills', 'skill-upper')
const observerSource = resolve(repoRoot, 'plugins', 'skill-up-observer')
const observerBundle = resolve(repoRoot, 'dist', 'plugins', 'skill-up-observer')
const dshSkillBundle = resolve(repoRoot, 'plugins', 'dsh-skill-up', 'dist', 'skill-upper')

const observerEntries = [
  '.codex-plugin',
  '.mcp.json',
  'README.md',
  'hooks',
  'schemas',
  'scripts',
]

function includeInBundle(path) {
  const relativePath = relative(repoRoot, path)
  return !relativePath.split(/[\\/]/).includes('__pycache__') && !relativePath.endsWith('.pyc')
}

function copy(source, target) {
  if (!existsSync(source)) {
    throw new Error(`bundle source does not exist: ${relative(repoRoot, source)}`)
  }
  mkdirSync(dirname(target), { recursive: true })
  cpSync(source, target, { recursive: true, filter: includeInBundle })
}

function bundleObserver() {
  rmSync(observerBundle, { recursive: true, force: true })
  mkdirSync(observerBundle, { recursive: true })
  for (const entry of observerEntries) {
    copy(resolve(observerSource, entry), resolve(observerBundle, entry))
  }
  copy(canonicalSkill, resolve(observerBundle, 'skills', 'skill-upper'))
  const version = process.env.SKILL_UP_PLUGIN_VERSION
  if (version) {
    if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
      throw new Error(`invalid observer plugin version: ${version}`)
    }
    const manifestPath = resolve(observerBundle, '.codex-plugin', 'plugin.json')
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
    manifest.version = version
    writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`)
  }
  console.log(`bundled Codex observer -> ${relative(repoRoot, observerBundle)}`)
}

function bundleDSH() {
  rmSync(dshSkillBundle, { recursive: true, force: true })
  copy(canonicalSkill, dshSkillBundle)
  console.log(`bundled skill-upper -> ${relative(repoRoot, dshSkillBundle)} (DSH)`)
}

const targets = { observer: bundleObserver, dsh: bundleDSH }
const selection = process.argv[2] ?? 'all'
const selectedTargets = selection === 'all' ? Object.keys(targets) : [selection]

if (selectedTargets.some((name) => !(name in targets))) {
  console.error('usage: node scripts/bundle-plugins.mjs [all|observer|dsh]')
  process.exit(2)
}

for (const name of selectedTargets) {
  targets[name]()
}
