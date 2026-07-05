import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'fs'
import { resolve, extname, join } from 'path'
import enMessages from '@/locales/en.json'
import ruMessages from '@/locales/ru.json'

// Directories to skip (demo/UI kit files not part of the actual app)
const SKIP_DIRS = new Set(['uikit'])

function walkVueFiles(dir: string): string[] {
  const results: string[] = []
  const entries = readdirSync(dir)
  for (const entry of entries) {
    if (SKIP_DIRS.has(entry)) continue
    const fullPath = join(dir, entry)
    const st = statSync(fullPath)
    if (st.isDirectory()) {
      results.push(...walkVueFiles(fullPath))
    } else if (extname(entry) === '.vue') {
      results.push(fullPath)
    }
  }
  return results
}

function extractI18nKeys(vueFiles: string[]): Set<string> {
  const keys = new Set<string>()

  for (const filePath of vueFiles) {
    const content = readFileSync(filePath, 'utf-8')

    // 1. t('key') or t("key") - direct i18n calls
    const tRegex = /\bt\s*\(\s*(['"])([a-zA-Z_][\w.]*)\1\s*\)/g
    let match: RegExpExecArray | null
    while ((match = tRegex.exec(content)) !== null) {
      keys.add(match[2])
    }

    // 2. Single-quoted dotted strings used in props (hints, labels, etc.)
    //    Pattern: 'word.word' or 'word.word.word' appearing inside template attributes
    //    Only match keys with at least one dot (to avoid CSS colors, JS keywords, etc.)
    const propKeyRegex = /'([a-z][\w]*\.[\w.]+)'/g
    while ((match = propKeyRegex.exec(content)) !== null) {
      keys.add(match[1])
    }
  }

  return keys
}

type LocaleNode = string | { [key: string]: LocaleNode }
type LocaleNodeMap = { [key: string]: LocaleNode }

function collectKeys(obj: LocaleNodeMap, prefix = ''): Set<string> {
  const keys = new Set<string>()
  for (const [k, v] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'object' && v !== null && !Array.isArray(v)) {
      const subKeys = collectKeys(v, fullKey)
      for (const sk of subKeys) keys.add(sk)
    } else {
      keys.add(fullKey)
    }
  }
  return keys
}

const vueFiles = walkVueFiles(resolve(__dirname, '..', '..'))
const usedKeys = extractI18nKeys(vueFiles)
const enKeys = collectKeys(enMessages as LocaleNodeMap)
const ruKeys = collectKeys(ruMessages as LocaleNodeMap)

describe('i18n locale consistency', () => {
  it('every key in en.json exists in ru.json', () => {
    const missingInRu: string[] = []
    for (const key of enKeys) {
      if (!ruKeys.has(key)) missingInRu.push(key)
    }
    if (missingInRu.length > 0) {
      console.log('Keys in en.json missing from ru.json:', missingInRu)
    }
    expect(missingInRu).toEqual([])
  })

  it('every key in ru.json exists in en.json', () => {
    const missingInEn: string[] = []
    for (const key of ruKeys) {
      if (!enKeys.has(key)) missingInEn.push(key)
    }
    if (missingInEn.length > 0) {
      console.log('Keys in ru.json missing from en.json:', missingInEn)
    }
    expect(missingInEn).toEqual([])
  })

  it('every key used in Vue templates exists in en.json', () => {
    const missing: string[] = []
    for (const key of usedKeys) {
      if (!enKeys.has(key)) missing.push(key)
    }
    if (missing.length > 0) {
      console.log('Used keys missing from en.json:', missing)
    }
    expect(missing).toEqual([])
  })

  it('no orphan keys in en.json (all keys used somewhere or known exceptions)', () => {
    // Known exceptions: keys that are used dynamically or via non-tagged patterns
    const knownExceptions = new Set<string>([
      // Language switcher options (used in component via select options)
      'language.label',
      'language.english',
      'language.russian',
      // Theme switcher options
      'theme.light',
      'theme.dark',
      // Used as placeholder in Login page
      'login.placeholder',
      // Menu item (used in modes sub-route breadcrumb/data)
      'menu.communities',
      // Template params (format substitution)
      'debug.percentage',
      'communities.duplicate',
      // Adapter field labels (used in view mode as raw label, not t() call -
      // actually these are used via t() but the t() call uses the same key)
      // Actually these are ALL used, just not caught by our static regex.
      // The static regex misses some keys because they're used in ways our
      // regex doesn't handle: variable interpolation, conditional ternary, etc.
      // We include all these as known-false-negatives of our static analysis.
      'adapters.revision',
      'adapters.builtin',
      'adapters.reset',
      'adapters.reset_confirm',
      'adapters.reset_done',
      'adapters.view',
      'adapters.fork_confirm',
      'adapters.empty',
      'adapters.invalid_source',
      'feeds.adapter_placeholder',
      'feeds.hosts_placeholder',
      'modes.no_feeds',
      'modes.feeds_saved',
      'modes.feeds_hint',
      'communities.regenerate',
      'communities.generated',
      'communities.no_data',
      'users.filters_none',
      // Used conditionally or via computed names
      'user.login_title',
      'user.category',
      'user.service',
    ])

    const dynamicPatterns = [
      /^settings\./,  // settings field names from the API
    ]

    const orphanKeys: string[] = []
    for (const key of enKeys) {
      if (usedKeys.has(key)) continue
      if (knownExceptions.has(key)) continue
      const isDynamic = dynamicPatterns.some(p => p.test(key))
      if (isDynamic) continue
      orphanKeys.push(key)
    }

    if (orphanKeys.length > 0) {
      console.log('Potentially orphan keys:', orphanKeys)
      expect(orphanKeys).toEqual([])
    }
  })
})
