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
import { after, test } from 'node:test'

import { AxiosError, type AxiosAdapter } from 'axios'
import { Window } from 'happy-dom'

const sourceHref = 'https://dashboard.example.com/usage-logs?channel=7#row-9'
const domWindow = new Window({ url: sourceHref })
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
] as const
const originalGlobals = new Map<
  (typeof domGlobalKeys)[number],
  PropertyDescriptor | undefined
>()
for (const key of domGlobalKeys) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { api } = await import('../http-client')
const { useAuthStore } = await import('@/stores/auth-store')

const terminal401Adapter: AxiosAdapter = async (config) => {
  const response = {
    config,
    data: {},
    headers: {},
    status: 401,
    statusText: 'Unauthorized',
  }
  throw new AxiosError(
    'unauthorized',
    'ERR_BAD_REQUEST',
    config,
    undefined,
    response
  )
}

after(() => {
  useAuthStore.getState().auth.reset('idle')
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('a terminal interceptor 401 preserves the browser route for sign-in recovery', async () => {
  useAuthStore.getState().auth.setBundle({
    access_token: 'access-token',
    token_type: 'Bearer',
    access_expires_at: 1_900_000_000,
    user: { id: 1, username: 'test-user', role: 1 },
    session: {
      sid: 'session-a',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 1,
      last_active_at: 1,
      expires_at: 1_900_000_000,
    },
  })

  await assert.rejects(
    api.get('/api/protected', {
      adapter: terminal401Adapter,
      authRetry: true,
      skipErrorHandler: true,
    })
  )

  assert.equal(window.location.pathname, '/sign-in')
  assert.equal(
    new URLSearchParams(window.location.search).get('redirect'),
    '/usage-logs?channel=7#row-9'
  )
  assert.equal(useAuthStore.getState().auth.user, null)
})
