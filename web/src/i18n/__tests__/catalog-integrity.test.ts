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
import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { isDeepStrictEqual } from 'node:util'

import {
  parseSync,
  type CallExpression,
  type Expression,
  Visitor,
} from 'oxc-parser'

import { STATIC_I18N_KEYS } from '../static-keys'

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'] as const
const testDirectory = path.dirname(fileURLToPath(import.meta.url))
const i18nDirectory = path.resolve(testDirectory, '..')
const localesDirectory = path.join(i18nDirectory, 'locales')
const sourceDirectory = path.resolve(i18nDirectory, '..')
const machinePlaceholderPattern = /__\s*PH_\d+\s*__/i
const htmlEntityPattern = /&(?:#\d+|#x[0-9a-f]+|[a-z][a-z0-9]+);/i

type Locale = (typeof locales)[number]

type Catalog = {
  translation: Record<string, string>
}

async function loadCatalog(locale: Locale): Promise<Catalog> {
  const text = await readFile(
    path.join(localesDirectory, `${locale}.json`),
    'utf8'
  )
  return JSON.parse(text) as Catalog
}

function interpolationVariables(value: string): string[] {
  return [
    ...new Set(
      [...value.matchAll(/\{\{\s*([A-Za-z_][\w.]*)\s*\}\}/g)].map(
        (match) => match[1]
      )
    ),
  ].sort()
}

function describeKeySetMismatch(
  locale: Locale,
  englishKeys: string[],
  localeKeys: string[]
): string | undefined {
  if (isDeepStrictEqual(localeKeys, englishKeys)) return undefined

  const englishKeySet = new Set(englishKeys)
  const localeKeySet = new Set(localeKeys)
  const missing = englishKeys.filter((key) => !localeKeySet.has(key))
  const extra = localeKeys.filter((key) => !englishKeySet.has(key))

  return `${locale}: missing ${JSON.stringify(missing)}; extra ${JSON.stringify(extra)}`
}

async function listProductionSourceFiles(directory: string): Promise<string[]> {
  const files: string[] = []

  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.name === '__tests__' || entry.name === 'locales') continue

    const entryPath = path.join(directory, entry.name)
    if (entry.isDirectory()) {
      files.push(...(await listProductionSourceFiles(entryPath)))
    } else if (/\.(?:ts|tsx)$/.test(entry.name)) {
      files.push(entryPath)
    }
  }

  return files
}

function literalTranslationKeys(source: string): string[] {
  const keys: string[] = []
  const patterns = [
    /\bt\(\s*'((?:\\.|[^'\\])*)'/g,
    /\bt\(\s*"((?:\\.|[^"\\])*)"/g,
    /\bt\(\s*`((?:\\.|[^`\\])*)`/g,
  ]

  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      keys.push(match[1])
    }
  }

  return keys
}

function isTranslationCallee(callee: Expression): boolean {
  if (callee.type === 'Identifier') return callee.name === 't'
  return (
    callee.type === 'MemberExpression' &&
    !callee.computed &&
    callee.property.type === 'Identifier' &&
    callee.property.name === 't'
  )
}

function staticTranslationArgument(
  argument: CallExpression['arguments'][number] | undefined
): string | undefined {
  if (!argument || argument.type === 'SpreadElement') return undefined
  if (argument.type === 'Literal' && typeof argument.value === 'string') {
    return argument.value
  }
  if (
    argument.type === 'TemplateLiteral' &&
    argument.expressions.length === 0 &&
    argument.quasis.length === 1
  ) {
    return argument.quasis[0].value.cooked ?? undefined
  }
  return undefined
}

function sourceTranslationKeys(filename: string, source: string): string[] {
  const result = parseSync(filename, source)
  if (result.errors.length > 0) {
    throw new Error(
      `${filename}: ${result.errors.map((error) => error.message).join('; ')}`
    )
  }

  const keys: string[] = []
  const visitor = new Visitor({
    CallExpression(node) {
      if (!isTranslationCallee(node.callee)) return
      const key = staticTranslationArgument(node.arguments[0])
      if (key !== undefined) keys.push(key)
    },
  })
  visitor.visit(result.program)
  return keys
}

describe('i18n catalog integrity', async () => {
  const catalogs = Object.fromEntries(
    await Promise.all(
      locales.map(async (locale) => [locale, await loadCatalog(locale)])
    )
  ) as Record<Locale, Catalog>

  test('detects different key sets when newline-delimited strings collide', () => {
    const mismatch = describeKeySetMismatch('zh', ['a', 'b\nc'], ['a\nb', 'c'])

    assert.equal(mismatch, 'zh: missing ["a","b\\nc"]; extra ["a\\nb","c"]')
  })

  test('keeps every locale key set aligned with English', () => {
    const englishKeys = Object.keys(catalogs.en.translation).sort()
    const mismatches: string[] = []

    for (const locale of locales) {
      const localeKeys = Object.keys(catalogs[locale].translation).sort()
      const mismatch = describeKeySetMismatch(locale, englishKeys, localeKeys)
      if (mismatch) mismatches.push(mismatch)
    }

    assert.deepEqual(mismatches, [])
  })

  test('preserves the Simplified Chinese value of a canonicalized raw key', () => {
    assert.equal(
      catalogs.zh.translation["Click save when you're done."],
      '完成后点击保存。'
    )
  })

  test('preserves the English interpolation variable set', () => {
    const mismatches: string[] = []

    for (const [key, englishValue] of Object.entries(catalogs.en.translation)) {
      const expected = interpolationVariables(englishValue)

      for (const locale of locales) {
        const actual = interpolationVariables(catalogs[locale].translation[key])
        if (actual.join('\n') !== expected.join('\n')) {
          mismatches.push(
            `${locale}: ${JSON.stringify(key)} expected ${JSON.stringify(expected)} but found ${JSON.stringify(actual)}`
          )
        }
      }
    }

    assert.deepEqual(mismatches, [])
  })

  test('rejects machine translation placeholders', () => {
    const violations: string[] = []

    for (const locale of locales) {
      for (const [key, value] of Object.entries(catalogs[locale].translation)) {
        if (
          machinePlaceholderPattern.test(key) ||
          machinePlaceholderPattern.test(value)
        ) {
          violations.push(`${locale}: ${JSON.stringify(key)}`)
        }
      }
    }

    assert.deepEqual(violations, [])
  })

  test('rejects HTML entities in catalogs and translation call keys', async () => {
    const violations: string[] = []

    for (const locale of locales) {
      for (const [key, value] of Object.entries(catalogs[locale].translation)) {
        if (htmlEntityPattern.test(key) || htmlEntityPattern.test(value)) {
          violations.push(`catalog ${locale}: ${JSON.stringify(key)}`)
        }
      }
    }

    for (const file of await listProductionSourceFiles(sourceDirectory)) {
      const source = await readFile(file, 'utf8')
      for (const key of literalTranslationKeys(source)) {
        if (htmlEntityPattern.test(key)) {
          violations.push(
            `source ${path.relative(sourceDirectory, file)}: ${JSON.stringify(key)}`
          )
        }
      }
    }

    assert.deepEqual(violations, [])
  })

  test('covers every static production translation key', async () => {
    const englishKeys = new Set(Object.keys(catalogs.en.translation))
    const locations = new Map<string, Set<string>>()
    const record = (key: string, location: string) => {
      const keyLocations = locations.get(key) ?? new Set<string>()
      keyLocations.add(location)
      locations.set(key, keyLocations)
    }

    for (const file of await listProductionSourceFiles(sourceDirectory)) {
      const source = await readFile(file, 'utf8')
      const relativePath = path.relative(sourceDirectory, file)
      for (const key of sourceTranslationKeys(file, source)) {
        record(key, relativePath)
      }
    }
    for (const key of STATIC_I18N_KEYS) {
      record(key, 'i18n/static-keys.ts')
    }

    const missing = [...locations]
      .filter(([key]) => !englishKeys.has(key))
      .sort(([left], [right]) => left.localeCompare(right))
      .map(
        ([key, keyLocations]) =>
          `${JSON.stringify(key)}: ${[...keyLocations].sort().join(', ')}`
      )

    assert.deepEqual(missing, [])
  })
})
