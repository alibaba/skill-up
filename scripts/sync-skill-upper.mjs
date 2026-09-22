import {
  cpSync,
  existsSync,
  readFileSync,
  readdirSync,
  rmSync,
} from 'node:fs'
import { dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = resolve(repoRoot, 'skills', 'skill-upper')
const targets = {
  observer: {
    path: resolve(repoRoot, 'plugins', 'skill-up-observer', 'skills', 'skill-upper'),
    include: (path) => relative(source, path).split(/[\\/]/)[0] !== 'evals',
  },
  dsh: {
    path: resolve(repoRoot, 'plugins', 'dsh-skill-up', 'dist', 'skill-upper'),
    include: () => true,
  },
}

const args = process.argv.slice(2)
const check = args.includes('--check')
const selection = args.find((arg) => !arg.startsWith('--')) ?? 'all'
const selectedTargets = selection === 'all' ? Object.keys(targets) : [selection]

if (selectedTargets.some((name) => !(name in targets))) {
  console.error('usage: node scripts/sync-skill-upper.mjs [all|observer|dsh] [--check]')
  process.exit(2)
}

function listFiles(root, include = () => true, base = root) {
  if (!existsSync(root)) return []

  const files = []
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const path = resolve(root, entry.name)
    if (!include(path)) continue
    if (entry.isDirectory()) {
      files.push(...listFiles(path, include, base))
    } else if (entry.isFile()) {
      files.push(relative(base, path))
    }
  }
  return files.sort()
}

function checkTarget(name, target) {
  const sourceFiles = listFiles(source, target.include)
  const targetFiles = listFiles(target.path)
  const errors = []

  if (sourceFiles.join('\n') !== targetFiles.join('\n')) {
    errors.push('file list differs')
  }

  for (const path of sourceFiles.filter((item) => targetFiles.includes(item))) {
    if (!readFileSync(resolve(source, path)).equals(readFileSync(resolve(target.path, path)))) {
      errors.push(`content differs: ${path}`)
    }
  }

  if (errors.length > 0) {
    console.error(`${name} skill-upper bundle is stale (${errors.join('; ')})`)
    console.error('run: make sync-skill-upper')
    process.exitCode = 1
    return
  }
  console.log(`${name} skill-upper bundle is current`)
}

function syncTarget(name, target) {
  rmSync(target.path, { recursive: true, force: true })
  cpSync(source, target.path, { recursive: true, filter: target.include })
  console.log(`synced skill-upper -> ${relative(repoRoot, target.path)} (${name})`)
}

for (const name of selectedTargets) {
  if (check) {
    checkTarget(name, targets[name])
  } else {
    syncTarget(name, targets[name])
  }
}
