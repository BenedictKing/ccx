import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { generatePresetManifest } from './generate-preset-manifest.mjs'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const registryPath = join(root, 'shared/model-registry/ccx_model_registry.json')

export function validateModelRegistry(registry) {
  if (!registry || typeof registry !== 'object' || Array.isArray(registry)) {
    throw new Error('model registry must be a JSON object')
  }
  if (registry.schemaVersion !== 1) {
    throw new Error('model registry schemaVersion must be 1')
  }

  validatePatternEntries(registry.upstreamCapabilities, 'upstream capability', entry => entry.displayName || entry.provider)
  validatePatternEntries(registry.benchmarkProfiles, 'benchmark profile', entry => entry.canonicalModel)
}

function validatePatternEntries(entries, kind, labelFor) {
  for (const entry of entries || []) {
    if (!Array.isArray(entry.patterns) || entry.patterns.length === 0) {
      throw new Error(`${kind} ${labelFor(entry) || '<unknown>'} must contain patterns`)
    }
    for (const pattern of entry.patterns) {
      try {
        new RegExp(pattern, 'i')
      } catch (error) {
        throw new Error(`Invalid ${kind} regex for ${labelFor(entry) || '<unknown>'}: ${pattern}\n  ${error.message}`)
      }
    }
  }
}

export function validateAndGeneratePresetManifest() {
  const registry = JSON.parse(readFileSync(registryPath, 'utf8'))
  validateModelRegistry(registry)
  generatePresetManifest()
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  validateAndGeneratePresetManifest()
}
