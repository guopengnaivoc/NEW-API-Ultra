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
import { test } from 'node:test'

import type { AxiosAdapter } from 'axios'
import { Window } from 'happy-dom'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'Node',
  'Event',
  'CustomEvent',
] as const

test('only an authoritative completed setup response suppresses later checks', async () => {
  const domWindow = new Window({
    url: 'https://new-api.example/dashboard',
  })
  const globalDescriptors = new Map<
    (typeof domGlobalKeys)[number],
    PropertyDescriptor | undefined
  >()
  for (const key of domGlobalKeys) {
    globalDescriptors.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: domWindow[key],
    })
  }

  const originalAdapter = api.defaults.adapter
  const responses: Array<
    Error | { success: boolean; data?: { status: boolean } }
  > = [
    new Error('setup endpoint unavailable'),
    { success: false },
    { success: true },
    { success: true, data: { status: false } },
    { success: true, data: { status: true } },
  ]
  let requestCount = 0
  const adapter: AxiosAdapter = async (config) => {
    assert.equal(config.url, '/api/setup')
    requestCount += 1
    const response = responses.shift()
    assert.ok(response)
    if (response instanceof Error) throw response
    return {
      data: response,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  api.defaults.adapter = adapter
  localStorage.setItem('setup_status_checked', 'true')
  useAuthStore.getState().auth.reset('complete')

  const { Route } = await import('../__root')
  const beforeLoad = Route.options.beforeLoad
  assert.ok(beforeLoad)
  const runBeforeLoad = () =>
    beforeLoad({
      location: {
        href: 'https://new-api.example/dashboard',
        pathname: '/dashboard',
      },
    } as never)
  const isSetupRedirect = (error: unknown) =>
    error instanceof Response &&
    (
      error as Response & {
        options?: { to?: string }
      }
    ).options?.to === '/setup'

  try {
    for (let index = 0; index < 4; index += 1) {
      await assert.rejects(runBeforeLoad, isSetupRedirect)
    }

    await runBeforeLoad()
    await runBeforeLoad()

    assert.equal(requestCount, 5)
  } finally {
    api.defaults.adapter = originalAdapter
    useAuthStore.getState().auth.reset('idle')
    domWindow.close()
    for (const [key, descriptor] of globalDescriptors) {
      if (descriptor) {
        Object.defineProperty(globalThis, key, descriptor)
      } else {
        Reflect.deleteProperty(globalThis, key)
      }
    }
  }
})
