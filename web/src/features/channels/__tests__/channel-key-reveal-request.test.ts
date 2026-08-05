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

import { api } from '@/lib/api'

import { getChannelKey } from '../api'

test('sends one proof only to the selected channel key endpoint', async () => {
  const originalPost = api.post
  const calls: Array<{ url: string; body: unknown; config: unknown }> = []
  api.post = (async (url: string, body?: unknown, config?: unknown) => {
    calls.push({ url, body, config })
    return { data: { success: true, data: { key: 'redacted-fixture' } } }
  }) as typeof api.post

  try {
    await getChannelKey(17, 'opaque-proof')
  } finally {
    api.post = originalPost
  }

  assert.deepEqual(calls, [
    {
      url: '/api/channel/17/key',
      body: undefined,
      config: {
        headers: { 'X-Security-Proof': 'opaque-proof' },
        skipBusinessError: true,
        skipErrorHandler: true,
      },
    },
  ])
})
