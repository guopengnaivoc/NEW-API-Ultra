/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import fs from 'node:fs/promises'
import path from 'node:path'

// This script is executed from the web/ package root (see package.json script).
const LOCALES_DIR = path.resolve('src/i18n/locales')
const BASE_LOCALE = 'en'
const OBFUSCATED_KEYS = [
  {
    runtime: ['footer', 'new' + 'api', 'projectAttributionSuffix'].join('.'),
    serialized: 'footer.new\\u0061pi.projectAttributionSuffix',
  },
]

const BRAND_AND_LITERAL_KEYS = new Set([
  'AI Proxy',
  'AIGC2D',
  'Alipay',
  'Anthropic',
  'API URL',
  'API2GPT',
  'AccessKey / SecretAccessKey',
  'AZURE_OPENAI_ENDPOINT *',
  'Baidu V2',
  'CC Switch',
  'ChatGPT',
  'ChatGPT Subscription (Codex)',
  'Claude',
  'Client ID',
  'Client Secret',
  'Cloudflare',
  'Cohere',
  'DeepSeek',
  'Discord',
  'DoubaoVideo',
  'FastGPT',
  'Gemini',
  'Gemini Image 4K',
  'GitHub',
  'Jimeng',
  'JustSong',
  'LingYiWanWu',
  'LinuxDO',
  'MjProxy',
  'MjProxyPlus',
  'MiniMax',
  'Mistral',
  'MokaAI',
  'Moonshot',
  'New API',
  'New API <noreply@example.com>',
  'NewAPI',
  'OAuth Client Secret',
  'OhMyGPT',
  'Ollama',
  'One API',
  'OpenAI',
  'OpenAIMax',
  'OpenRouter',
  'Pancake',
  'Passkey',
  'Perplexity',
  'QuantumNous',
  'Quota:',
  'Replicate',
  'SiliconFlow',
  'Stripe',
  'Submodel',
  'SunoAPI',
  'Telegram',
  'Tencent',
  'TTFT P50',
  'TTFT P95',
  'TTFT P99',
  'Uptime Kuma',
  'Uptime Kuma URL',
  'Vertex AI',
  'VolcEngine',
  'Waffo Pancake Dashboard',
  'Waffo Pancake MoR',
  'WeChat',
  'WeChat Pay',
  'Webhook URL',
  'Webhook URL:',
  'Well-Known URL',
  'Worker URL',
  'Xinference',
  'Xunfei',
  'Zhipu V4',
  '"default": "us-central1", "claude-3-5-sonnet-20240620": "europe-west1"',
  'edit_this',
  'example.com\nblocked-site.com',
  'example.com\ncompany.com',
  'footer.columns.related.links.midjourney',
  'footer.columns.related.links.newApiKeyTool',
  'my-status',
  'new-api-key-tool',
  'price_xxx',
  'whsec_xxx',
])

function isPlainObject(v) {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function stableStringify(obj) {
  let text = JSON.stringify(obj, null, 2)
  for (const key of OBFUSCATED_KEYS) {
    text = text.replaceAll(`"${key.runtime}":`, `"${key.serialized}":`)
  }
  return text + '\n'
}

function reorderLikeBase(
  base,
  target,
  fill,
  extras,
  missing,
  currentPath = []
) {
  // If base is an object, we keep base's key order and recurse.
  if (isPlainObject(base)) {
    const out = {}
    const t = isPlainObject(target) ? target : {}
    const f = isPlainObject(fill) ? fill : {}

    for (const key of Object.keys(base)) {
      const nextPath = [...currentPath, key]
      if (Object.prototype.hasOwnProperty.call(t, key)) {
        out[key] = reorderLikeBase(
          base[key],
          t[key],
          f[key],
          extras,
          missing,
          nextPath
        )
      } else {
        missing.push(nextPath.join('.'))
        out[key] = reorderLikeBase(
          base[key],
          undefined,
          f[key],
          extras,
          missing,
          nextPath
        )
      }
    }

    for (const key of Object.keys(t)) {
      if (!Object.prototype.hasOwnProperty.call(base, key)) {
        const nextPath = [...currentPath, key].join('.')
        extras[nextPath] = t[key]
      }
    }

    return out
  }

  // For arrays: prefer target if it's also an array; otherwise use base.
  if (Array.isArray(base)) {
    if (Array.isArray(target)) return target
    if (Array.isArray(fill)) return fill
    return base
  }

  // For primitives: prefer target if defined, else base.
  return target === undefined ? (fill ?? base) : target
}

function isLikelyUntranslated({ locale, baseValue, value }) {
  if (typeof value !== 'string' || typeof baseValue !== 'string') return false
  if (value !== baseValue) return false

  // Skip short tokens / acronyms / ids
  const s = baseValue.trim()
  if (BRAND_AND_LITERAL_KEYS.has(s)) return false
  if (
    /^https?:\/\//.test(s) ||
    /^\/[\w/-]+/.test(s) ||
    /^[\w.-]+@[\w.-]+$/.test(s) ||
    /^smtp\./i.test(s) ||
    /^socks5:/i.test(s) ||
    /^org-/.test(s) ||
    /^gpt-/i.test(s) ||
    /^checkout\./.test(s) ||
    /^footer\./.test(s) ||
    /^[A-Z0-9_ *./:-]+$/.test(s) ||
    s.startsWith('{') ||
    s.startsWith('[') ||
    s.includes('\n')
  ) {
    return false
  }
  if (s.length < 6) return false
  if (!/[A-Za-z]{3,}/.test(s)) return false

  // For locales with non-latin scripts, equality with EN is a strong signal.
  if (locale === 'ja' || locale === 'zh') return true
  if (locale === 'ru') return true

  // For fr/vi: still useful but noisier; keep it conservative.
  if (locale === 'fr' || locale === 'vi') {
    return /\b(the|and|or|to|with|please)\b/i.test(s)
  }

  return false
}

async function main() {
  const entries = await fs.readdir(LOCALES_DIR, { withFileTypes: true })
  const localeFiles = entries
    .filter((e) => e.isFile() && e.name.endsWith('.json'))
    .map((e) => e.name)
    .sort((a, b) => a.localeCompare(b))

  const parsedByLocale = {}
  for (const filename of localeFiles) {
    const locale = filename.replace(/\.json$/i, '')
    const raw = await fs.readFile(path.join(LOCALES_DIR, filename), 'utf8')
    parsedByLocale[locale] = JSON.parse(raw)
  }

  const baseJson = parsedByLocale[BASE_LOCALE]
  if (!baseJson) {
    throw new Error('Required base locale en.json was not found.')
  }

  const report = {
    base: 'en.json',
    locales: {},
  }

  const extrasDir = path.join(LOCALES_DIR, '_extras')
  const reportsDir = path.join(LOCALES_DIR, '_reports')
  const desiredOutputs = new Map()
  const plannedDeletions = new Set()
  const extraFailures = []

  for (const filename of localeFiles) {
    const locale = filename.replace(/\.json$/i, '')
    const full = path.join(LOCALES_DIR, filename)
    const json = parsedByLocale[locale]

    const extras = {}
    const missing = []
    const fixed = reorderLikeBase(baseJson, json, baseJson, extras, missing)

    // Untranslated scan (translation namespace only)
    const untranslated = {}
    const compareTrans = baseJson?.translation ?? {}
    const trans = fixed?.translation ?? {}
    if (
      isPlainObject(compareTrans) &&
      isPlainObject(trans) &&
      locale !== BASE_LOCALE
    ) {
      for (const k of Object.keys(compareTrans)) {
        const baseValue = compareTrans[k]
        const value = trans[k]
        if (isLikelyUntranslated({ locale, baseValue, value })) {
          untranslated[k] = value
        }
      }
    }

    report.locales[locale] = {
      file: filename,
      missingCount: missing.length,
      extrasCount: Object.keys(extras).length,
      untranslatedCount: Object.keys(untranslated).length,
    }

    const extraKeys = Object.keys(extras).sort((a, b) => a.localeCompare(b))
    if (locale !== BASE_LOCALE && extraKeys.length > 0) {
      extraFailures.push({
        locale,
        keys: extraKeys,
      })
    }

    plannedDeletions.add(path.join(extrasDir, `${locale}.extras.json`))
    if (Object.keys(untranslated).length > 0) {
      desiredOutputs.set(
        path.join(reportsDir, `${locale}.untranslated.json`),
        stableStringify(untranslated)
      )
    } else {
      plannedDeletions.add(path.join(reportsDir, `${locale}.untranslated.json`))
    }

    desiredOutputs.set(full, stableStringify(fixed))
  }

  desiredOutputs.set(
    path.join(reportsDir, '_sync-report.json'),
    stableStringify(report)
  )

  if (extraFailures.length > 0) {
    extraFailures.sort((a, b) => a.locale.localeCompare(b.locale))
    const details = extraFailures
      .map(({ locale, keys }) => `${locale}: ${keys.join(', ')}`)
      .join('\n')
    throw new Error(
      `i18n sync rejected extra keys not present in en.json:\n${details}`
    )
  }

  const changedOutputs = []
  for (const [destination, contents] of [...desiredOutputs.entries()].sort(
    ([a], [b]) => a.localeCompare(b)
  )) {
    let currentContents
    try {
      currentContents = await fs.readFile(destination, 'utf8')
    } catch (error) {
      if (error.code !== 'ENOENT') throw error
    }
    if (currentContents !== contents) {
      changedOutputs.push({ destination, contents })
    }
  }

  const stagedOutputs = []
  const temporaryFiles = new Set()
  let publicationError

  try {
    const outputDirectories = [
      ...new Set(
        changedOutputs.map(({ destination }) => path.dirname(destination))
      ),
    ].sort((a, b) => a.localeCompare(b))
    for (const directory of outputDirectories) {
      await fs.mkdir(directory, { recursive: true })
    }

    for (const [index, output] of changedOutputs.entries()) {
      const temporaryPath = path.join(
        path.dirname(output.destination),
        `.${path.basename(output.destination)}.sync-i18n-${process.pid}-${index}.tmp`
      )
      const handle = await fs.open(temporaryPath, 'wx')
      temporaryFiles.add(temporaryPath)
      try {
        await handle.writeFile(output.contents, 'utf8')
      } finally {
        await handle.close()
      }
      stagedOutputs.push({
        destination: output.destination,
        temporaryPath,
      })
    }

    for (const output of stagedOutputs) {
      await fs.rename(output.temporaryPath, output.destination)
      temporaryFiles.delete(output.temporaryPath)
    }

    for (const deletion of [...plannedDeletions].sort((a, b) =>
      a.localeCompare(b)
    )) {
      await fs.rm(deletion, { force: true })
    }
  } catch (error) {
    publicationError = error
  }

  const cleanupResults = await Promise.allSettled(
    [...temporaryFiles].map((temporaryPath) =>
      fs.rm(temporaryPath, { force: true })
    )
  )
  const cleanupErrors = cleanupResults
    .filter((result) => result.status === 'rejected')
    .map((result) => result.reason)

  if (publicationError && cleanupErrors.length > 0) {
    throw new AggregateError(
      [publicationError, ...cleanupErrors],
      'i18n sync publication and temporary-file cleanup failed.'
    )
  }
  if (publicationError) throw publicationError
  if (cleanupErrors.length > 0) {
    throw new AggregateError(
      cleanupErrors,
      'i18n sync temporary-file cleanup failed.'
    )
  }

  console.log(
    `i18n sync done. Report: ${path.join(reportsDir, '_sync-report.json')}`
  )
}

main().catch((err) => {
  console.error(err)
  process.exitCode = 1
})
