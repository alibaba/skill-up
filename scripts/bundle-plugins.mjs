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
const dshSource = resolve(repoRoot, 'plugins', 'dsh-skill-up')
const dshSkillBundle = resolve(repoRoot, 'plugins', 'dsh-skill-up', 'dist', 'skill-upper')
const dshPackageBundle = resolve(repoRoot, 'dist', 'plugins', 'dsh-skill-up')

const observerEntries = [
  '.codex-plugin',
  '.mcp.json',
  'README.md',
  'hooks',
  'schemas',
  'scripts',
]

const dshPackageEntries = [
  'index.js',
  'lib',
  'cordis.patch.yml',
  'package.json',
  'README.md',
]

function validateVersion(version, label) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error(`invalid ${label} version: ${version}`)
  }
}

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
    validateVersion(version, 'observer plugin')
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

function packageDSH() {
  rmSync(dshPackageBundle, { recursive: true, force: true })
  mkdirSync(dshPackageBundle, { recursive: true })
  for (const entry of dshPackageEntries) {
    copy(resolve(dshSource, entry), resolve(dshPackageBundle, entry))
  }
  copy(canonicalSkill, resolve(dshPackageBundle, 'dist', 'skill-upper'))

  const version = process.env.SKILL_UP_DSH_PLUGIN_VERSION
  if (version) {
    validateVersion(version, 'DSH plugin')
    const packagePath = resolve(dshPackageBundle, 'package.json')
    const packageJSON = JSON.parse(readFileSync(packagePath, 'utf8'))
    packageJSON.version = version
    writeFileSync(packagePath, `${JSON.stringify(packageJSON, null, 2)}\n`)
  }
  console.log(`bundled DSH package -> ${relative(repoRoot, dshPackageBundle)}`)
}

const targets = { observer: bundleObserver, dsh: bundleDSH, 'dsh-package': packageDSH }
const selection = process.argv[2] ?? 'all'
const selectedTargets = selection === 'all' ? ['observer', 'dsh'] : [selection]

if (selectedTargets.some((name) => !(name in targets))) {
  console.error('usage: node scripts/bundle-plugins.mjs [all|observer|dsh|dsh-package]')
  process.exit(2)
}

for (const name of selectedTargets) {
  targets[name]()
}
