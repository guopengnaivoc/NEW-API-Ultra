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
import { afterEach, beforeEach, describe, test } from 'node:test'

import type { AxiosAdapter } from 'axios'

import {
  resetPassword,
  sendEmailVerification as sendAuthEmailVerification,
  sendPasswordResetEmail,
} from '@/features/auth/api'
import { sendEmailVerification as sendProfileEmailVerification } from '@/features/profile/api'
import { api } from '@/lib/api'

type ApiCall = {
  body?: unknown
  config?: unknown
  method: 'get' | 'post'
  url: string
}

const calls: ApiCall[] = []
const originalGet = api.get
const originalPost = api.post

beforeEach(() => {
  calls.length = 0
  api.get = (async (url: string) => {
    calls.push({ method: 'get', url })
    throw new Error(`Unexpected GET ${url}`)
  }) as typeof api.get
  api.post = (async (url: string, body?: unknown, config?: unknown) => {
    calls.push({ method: 'post', url, body, config })
    return { data: { success: true, message: '' } }
  }) as typeof api.post
})

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})

describe('verification API transport', () => {
  test('auth email verification sends email in a POST body and Turnstile in a header', async () => {
    await sendAuthEmailVerification('person@example.com', 'turnstile-token')

    assert.deepEqual(calls, [
      {
        method: 'post',
        url: '/api/verification',
        body: { email: 'person@example.com' },
        config: {
          headers: { 'X-Turnstile-Token': 'turnstile-token' },
        },
      },
    ])
  })

  test('profile email verification sends email in a POST body without query parameters', async () => {
    await sendProfileEmailVerification('profile@example.com')

    assert.deepEqual(calls, [
      {
        method: 'post',
        url: '/api/verification',
        body: { email: 'profile@example.com' },
        config: {
          headers: undefined,
        },
      },
    ])
  })

  test('password reset email sends email in a POST body and Turnstile in a header', async () => {
    await sendPasswordResetEmail('reset@example.com', 'turnstile-token')

    assert.deepEqual(calls, [
      {
        method: 'post',
        url: '/api/reset_password',
        body: { email: 'reset@example.com' },
        config: {
          headers: { 'X-Turnstile-Token': 'turnstile-token' },
        },
      },
    ])
  })

  test('password reset submits exactly the opaque token and chosen password', async () => {
    await resetPassword({
      token: 'a'.repeat(43),
      password: 'chosen-password',
      email: 'must-not-leak@example.com',
    } as { token: string; password: string })

    assert.deepEqual(calls, [
      {
        method: 'post',
        url: '/api/user/reset',
        body: {
          token: 'a'.repeat(43),
          password: 'chosen-password',
        },
        config: undefined,
      },
    ])
  })

  test('password reset accepts a successful secret-free response through the real client interceptors', async () => {
    const testPost = api.post
    const testAdapter = api.defaults.adapter
    const successfulResetAdapter: AxiosAdapter = async (config) => ({
      config,
      data: { success: true, message: '' },
      headers: {},
      status: 200,
      statusText: 'OK',
    })

    api.post = originalPost
    api.defaults.adapter = successfulResetAdapter

    try {
      assert.deepEqual(
        await resetPassword({
          token: 'a'.repeat(43),
          password: 'chosen-password',
        }),
        { success: true, message: '' }
      )
    } finally {
      api.defaults.adapter = testAdapter
      api.post = testPost
    }
  })
})
