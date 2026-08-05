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
import { afterEach, describe, test } from 'node:test'

import type { AxiosAdapter } from 'axios'

import { api } from '@/lib/api'

import { updateSystemOption } from '../api'

const originalAdapter = api.defaults.adapter

afterEach(() => {
  api.defaults.adapter = originalAdapter
})

describe('system option updates', () => {
  test('rejects an HTTP 200 business failure with the server message', async () => {
    let requestSkippedGlobalBusinessError = false
    const adapter: AxiosAdapter = async (config) => {
      requestSkippedGlobalBusinessError = config.skipBusinessError === true
      return {
        data: {
          success: false,
          message: 'database rejected the option update',
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    api.defaults.adapter = adapter

    await assert.rejects(
      updateSystemOption({ key: 'QuotaPerUnit', value: 500000 }),
      {
        message: 'database rejected the option update',
      }
    )
    assert.equal(requestSkippedGlobalBusinessError, true)
  })
})
