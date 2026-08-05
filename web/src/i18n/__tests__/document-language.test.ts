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
import { afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
] as const
const globalDescriptors = new Map<
  (typeof domGlobalKeys)[number],
  PropertyDescriptor | undefined
>()
for (const key of domGlobalKeys) {
  globalDescriptors.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
}

let i18n: (typeof import('../config'))['default'] | undefined

beforeEach(async () => {
  const domWindow = new Window({ url: 'https://dashboard.example.com/' })
  for (const key of domGlobalKeys) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: domWindow[key],
    })
  }

  document.documentElement.lang = 'en'
  localStorage.setItem('i18nextLng', 'zhCN')

  ;({ default: i18n } = await import('../config'))
  if (i18n.resolvedLanguage !== 'zhCN') {
    await i18n.changeLanguage('zhCN')
  }
})

afterEach(async () => {
  await i18n?.changeLanguage('en')
  for (const key of domGlobalKeys) {
    const descriptor = globalDescriptors.get(key)
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      delete (globalThis as Record<string, unknown>)[key]
    }
  }
})

test('keeps the document language aligned with detected and changed interface languages', async () => {
  assert.ok(i18n)
  assert.equal(i18n.resolvedLanguage, 'zhCN')
  assert.equal(document.documentElement.lang, 'zh-CN')

  await i18n.changeLanguage('fr')
  assert.equal(document.documentElement.lang, 'fr')

  await i18n.changeLanguage('zhTW')
  assert.equal(document.documentElement.lang, 'zh-TW')
})
