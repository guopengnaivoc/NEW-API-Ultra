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
import { spawnSync } from 'node:child_process'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

const LOCALES = ['en', 'fr', 'ja', 'ru', 'vi', 'zh-TW', 'zh']
const SYNC_SCRIPT = fileURLToPath(new URL('../sync-i18n.mjs', import.meta.url))

function stableStringify(value) {
  return `${JSON.stringify(value, null, 2)}\n`
}

async function createFixture() {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'sync-i18n-authority-'))
  await fs.mkdir(path.join(root, 'src/i18n/locales'), { recursive: true })
  return root
}

async function writeLocale(root, locale, translation) {
  await fs.writeFile(
    path.join(root, 'src/i18n/locales', `${locale}.json`),
    stableStringify({ translation }),
    'utf8'
  )
}

async function snapshotTree(root, directory = root, snapshot = {}) {
  const entries = await fs.readdir(directory, { withFileTypes: true })
  entries.sort((a, b) => a.name.localeCompare(b.name))

  for (const entry of entries) {
    const entryPath = path.join(directory, entry.name)
    const relativePath = path.relative(root, entryPath)

    if (entry.isDirectory()) {
      snapshot[relativePath] = { type: 'directory' }
      await snapshotTree(root, entryPath, snapshot)
      continue
    }

    snapshot[relativePath] = {
      type: 'file',
      bytes: (await fs.readFile(entryPath)).toString('base64'),
    }
  }

  return snapshot
}

function temporaryPaths(snapshot) {
  return Object.keys(snapshot).filter((entryPath) =>
    /\.sync-i18n-.*\.tmp$/.test(path.basename(entryPath))
  )
}

function runSync(root) {
  return spawnSync('node', [SYNC_SCRIPT], {
    cwd: root,
    encoding: 'utf8',
  })
}

describe('i18n sync authority', () => {
  test('rejects a larger foreign catalog without changing any fixture byte', async () => {
    const root = await createFixture()

    try {
      for (const locale of LOCALES) {
        const translation = {
          Alpha: locale === 'en' ? 'Alpha' : `${locale} Alpha`,
        }
        if (locale === 'fr') translation['Foreign typo'] = 'Valeur'
        await writeLocale(root, locale, translation)
      }

      const reportsDirectory = path.join(root, 'src/i18n/locales/_reports')
      const extrasDirectory = path.join(root, 'src/i18n/locales/_extras')
      await fs.mkdir(reportsDirectory, { recursive: true })
      await fs.mkdir(extrasDirectory, { recursive: true })
      await fs.writeFile(
        path.join(reportsDirectory, '_sync-report.json'),
        'sentinel sync report\n',
        'utf8'
      )
      await fs.writeFile(
        path.join(reportsDirectory, 'fr.untranslated.json'),
        'sentinel untranslated report\n',
        'utf8'
      )
      await fs.writeFile(
        path.join(extrasDirectory, 'fr.extras.json'),
        'sentinel extras report\n',
        'utf8'
      )

      const before = await snapshotTree(root)
      const result = runSync(root)
      const after = await snapshotTree(root)

      assert.ifError(result.error)
      assert.notEqual(result.status, 0, result.stdout)
      assert.match(result.stderr, /fr/)
      assert.match(result.stderr, /Foreign typo/)
      assert.deepEqual(after, before)
      assert.deepEqual(temporaryPaths(after), [])
    } finally {
      await fs.rm(root, { recursive: true, force: true })
    }
  })

  test('rejects a fixture without en.json before changing reports', async () => {
    const root = await createFixture()

    try {
      await writeLocale(root, 'fr', { Alpha: 'Alpha français' })
      const reportsDirectory = path.join(root, 'src/i18n/locales/_reports')
      await fs.mkdir(reportsDirectory, { recursive: true })
      await fs.writeFile(
        path.join(reportsDirectory, '_sync-report.json'),
        'sentinel sync report\n',
        'utf8'
      )

      const before = await snapshotTree(root)
      const result = runSync(root)
      const after = await snapshotTree(root)

      assert.ifError(result.error)
      assert.notEqual(result.status, 0, result.stdout)
      assert.match(result.stderr, /en\.json/)
      assert.deepEqual(after, before)
      assert.deepEqual(temporaryPaths(after), [])
    } finally {
      await fs.rm(root, { recursive: true, force: true })
    }
  })

  test('fills missing English keys and converges reports deterministically', async () => {
    const root = await createFixture()

    try {
      for (const locale of LOCALES) {
        if (locale === 'en') {
          await writeLocale(root, locale, {
            Alpha: 'Alpha',
            Beta: 'Beta',
          })
          continue
        }
        if (locale === 'fr') {
          await writeLocale(root, locale, { Alpha: 'Alpha français' })
          continue
        }
        await writeLocale(root, locale, {
          Alpha: `${locale} Alpha`,
          Beta: `${locale} Beta`,
        })
      }

      const reportsDirectory = path.join(root, 'src/i18n/locales/_reports')
      const extrasDirectory = path.join(root, 'src/i18n/locales/_extras')
      await fs.mkdir(reportsDirectory, { recursive: true })
      await fs.mkdir(extrasDirectory, { recursive: true })
      const staleUntranslatedPath = path.join(
        reportsDirectory,
        'fr.untranslated.json'
      )
      const staleExtrasPath = path.join(extrasDirectory, 'fr.extras.json')
      await fs.writeFile(
        staleUntranslatedPath,
        'stale untranslated report\n',
        'utf8'
      )
      await fs.writeFile(staleExtrasPath, 'stale extras report\n', 'utf8')

      const first = runSync(root)
      assert.ifError(first.error)
      assert.equal(first.status, 0, first.stderr)

      const localesDirectory = path.join(root, 'src/i18n/locales')
      const french = JSON.parse(
        await fs.readFile(path.join(localesDirectory, 'fr.json'), 'utf8')
      )
      const firstReport = JSON.parse(
        await fs.readFile(
          path.join(reportsDirectory, '_sync-report.json'),
          'utf8'
        )
      )
      assert.equal(french.translation.Beta, 'Beta')
      assert.equal(firstReport.base, 'en.json')
      assert.equal(firstReport.locales.fr.missingCount, 1)
      await assert.rejects(fs.readFile(staleUntranslatedPath), {
        code: 'ENOENT',
      })
      await assert.rejects(fs.readFile(staleExtrasPath), { code: 'ENOENT' })

      const firstSnapshot = await snapshotTree(root)
      assert.deepEqual(temporaryPaths(firstSnapshot), [])

      const second = runSync(root)
      assert.ifError(second.error)
      assert.equal(second.status, 0, second.stderr)
      const secondReport = JSON.parse(
        await fs.readFile(
          path.join(reportsDirectory, '_sync-report.json'),
          'utf8'
        )
      )
      assert.equal(secondReport.base, 'en.json')
      assert.equal(secondReport.locales.fr.missingCount, 0)
      const convergedSnapshot = await snapshotTree(root)
      assert.deepEqual(temporaryPaths(convergedSnapshot), [])

      const third = runSync(root)
      assert.ifError(third.error)
      assert.equal(third.status, 0, third.stderr)
      const thirdSnapshot = await snapshotTree(root)
      assert.deepEqual(thirdSnapshot, convergedSnapshot)
      assert.deepEqual(temporaryPaths(thirdSnapshot), [])
    } finally {
      await fs.rm(root, { recursive: true, force: true })
    }
  })

  test('does not report newline-delimited English literals as untranslated', async () => {
    const root = await createFixture()
    const literal = 'Copy the first line\nand then the second line'

    try {
      for (const locale of LOCALES) {
        await writeLocale(root, locale, { Literal: literal })
      }

      const result = runSync(root)

      assert.ifError(result.error)
      assert.equal(result.status, 0, result.stderr)

      const report = JSON.parse(
        await fs.readFile(
          path.join(root, 'src/i18n/locales/_reports/_sync-report.json'),
          'utf8'
        )
      )
      for (const locale of LOCALES.filter((locale) => locale !== 'en')) {
        assert.equal(report.locales[locale].untranslatedCount, 0)
        await assert.rejects(
          fs.readFile(
            path.join(
              root,
              'src/i18n/locales/_reports',
              `${locale}.untranslated.json`
            )
          ),
          { code: 'ENOENT' }
        )
      }
    } finally {
      await fs.rm(root, { recursive: true, force: true })
    }
  })
})
