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
import { describe, test } from 'node:test'

import { api } from '@/lib/api'

import { rotateApiKey } from '../api'

describe('Relay Token key rotation request', () => {
  test('binds the one-time proof to the rotation endpoint', async () => {
    const originalPost = api.post
    const calls: Array<{
      url: string
      body: unknown
      config: unknown
    }> = []

    api.post = (async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ url, body, config })
      return { data: { success: true, data: {} } }
    }) as typeof api.post

    try {
      await rotateApiKey(7, 'rotation-proof')
    } finally {
      api.post = originalPost
    }

    assert.deepEqual(calls, [
      {
        url: '/api/token/7/rotate',
        body: undefined,
        config: {
          headers: { 'X-Security-Proof': 'rotation-proof' },
        },
      },
    ])
  })
})
