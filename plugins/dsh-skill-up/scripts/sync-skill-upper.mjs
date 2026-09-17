import { cpSync, mkdirSync, rmSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const packageDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = resolve(packageDir, '..', '..', 'skills', 'skill-upper')
const target = resolve(packageDir, 'dist', 'skill-upper')

rmSync(target, { recursive: true, force: true })
mkdirSync(dirname(target), { recursive: true })
cpSync(source, target, { recursive: true })
