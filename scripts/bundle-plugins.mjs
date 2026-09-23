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
const observationSchema = resolve(repoRoot, 'schemas', 'skill-observation', 'v1alpha1', 'observation.schema.json')
const codexSource = resolve(repoRoot, 'plugins', 'codex-skill-up')
const codexBundle = resolve(repoRoot, 'dist', 'plugins', 'codex-skill-up')
const dshSource = resolve(repoRoot, 'plugins', 'dsh-skill-up')
const dshSkillBundle = resolve(repoRoot, 'plugins', 'dsh-skill-up', 'dist', 'skill-upper')
const dshSchemaBundle = resolve(repoRoot, 'plugins', 'dsh-skill-up', 'dist', 'skill-observation', 'v1alpha1', 'observation.schema.json')
const dshPackageBundle = resolve(repoRoot, 'dist', 'plugins', 'dsh-skill-up')

const hostPackages = {
  codex: {
    source: codexSource,
    bundle: codexBundle,
    entries: ['.codex-plugin', '.mcp.json', 'README.md', 'hooks', 'scripts'],
    skillTarget: 'skills/skill-upper',
    schemaTarget: 'schemas/skill-observation/v1alpha1/observation.schema.json',
  },
  dsh: {
    source: dshSource,
    bundle: dshPackageBundle,
    entries: ['index.js', 'lib', 'cordis.patch.yml', 'package.json', 'README.md'],
    skillTarget: 'dist/skill-upper',
    schemaTarget: 'dist/skill-observation/v1alpha1/observation.schema.json',
  },
}

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

function stageHostPackage(config) {
  rmSync(config.bundle, { recursive: true, force: true })
  mkdirSync(config.bundle, { recursive: true })
  for (const entry of config.entries) {
    copy(resolve(config.source, entry), resolve(config.bundle, entry))
  }
  copy(canonicalSkill, resolve(config.bundle, config.skillTarget))
  copy(observationSchema, resolve(config.bundle, config.schemaTarget))
}

function bundleCodex() {
  stageHostPackage(hostPackages.codex)
  const version = process.env.SKILL_UP_CODEX_PLUGIN_VERSION
  if (version) {
    validateVersion(version, 'Codex plugin')
    const manifestPath = resolve(codexBundle, '.codex-plugin', 'plugin.json')
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
    manifest.version = version
    writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`)
  }
  console.log(`bundled Codex plugin -> ${relative(repoRoot, codexBundle)}`)
}

function bundleDSH() {
  rmSync(dshSkillBundle, { recursive: true, force: true })
  rmSync(resolve(dshSource, 'dist', 'skill-observation'), { recursive: true, force: true })
  copy(canonicalSkill, dshSkillBundle)
  copy(observationSchema, dshSchemaBundle)
  console.log(`bundled shared assets -> ${relative(repoRoot, resolve(dshSource, 'dist'))} (DSH)`)
}

function packageDSH() {
  stageHostPackage(hostPackages.dsh)

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

const targets = { codex: bundleCodex, dsh: bundleDSH, 'dsh-package': packageDSH }
const selection = process.argv[2] ?? 'all'
const selectedTargets = selection === 'all' ? ['codex', 'dsh'] : [selection]

if (selectedTargets.some((name) => !(name in targets))) {
  console.error('usage: node scripts/bundle-plugins.mjs [all|codex|dsh|dsh-package]')
  process.exit(2)
}

for (const name of selectedTargets) {
  targets[name]()
}
