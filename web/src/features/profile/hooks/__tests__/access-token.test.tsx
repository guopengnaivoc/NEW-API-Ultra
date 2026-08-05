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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import { api } from '@/lib/api'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const copiedValues: string[] = []
Object.defineProperty(navigator, 'clipboard', {
  configurable: true,
  value: {
    writeText: async (value: string) => {
      copiedValues.push(value)
    },
  },
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAccessToken } = await import('../use-access-token')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Copied to clipboard': 'Copied to clipboard',
        'Failed to copy to clipboard': 'Failed to copy to clipboard',
        'Token regenerated and copied to clipboard':
          'Token regenerated and copied to clipboard',
        'Failed to generate token': 'Failed to generate token',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function AccessTokenHarness() {
  const accessToken = useAccessToken()

  return (
    <>
      <button
        type='button'
        data-action='rotate'
        data-token={accessToken.token}
        data-expires-at={accessToken.expiresAt ?? ''}
        onClick={() => void accessToken.generate('proof-token')}
      >
        rotate
      </button>
      <button type='button' data-action='clear' onClick={accessToken.clear}>
        clear
      </button>
    </>
  )
}

describe('Dashboard PAT rotation hook', () => {
  after(() => {
    domWindow.close()
  })

  test('sends the one-time proof and retains the token expiration', async () => {
    const originalGet = api.get
    const originalPost = api.post
    const calls: Array<{
      method: string
      url: string
      body: unknown
      config: unknown
    }> = []
    const response = {
      data: {
        success: true,
        data: {
          token: 'pat_rotated',
          expires_at: 1_900_000_000,
        },
      },
    }

    api.get = (async (url: string, config?: unknown) => {
      calls.push({ method: 'get', url, body: undefined, config })
      return response
    }) as typeof api.get
    api.post = (async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ method: 'post', url, body, config })
      return response
    }) as typeof api.post

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    copiedValues.length = 0

    try {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <AccessTokenHarness />
          </I18nextProvider>
        )
      })
      const rotateButton = container.querySelector<HTMLButtonElement>(
        '[data-action="rotate"]'
      )
      const clearButton = container.querySelector<HTMLButtonElement>(
        '[data-action="clear"]'
      )
      assert.ok(rotateButton)
      assert.ok(clearButton)

      await act(async () => {
        rotateButton.click()
        await Promise.resolve()
      })

      assert.deepEqual(calls, [
        {
          method: 'post',
          url: '/api/user/token',
          body: undefined,
          config: {
            headers: { 'X-Security-Proof': 'proof-token' },
          },
        },
      ])
      assert.equal(rotateButton.dataset.token, 'pat_rotated')
      assert.equal(rotateButton.dataset.expiresAt, '1900000000')
      assert.deepEqual(copiedValues, ['pat_rotated'])

      await act(async () => clearButton.click())
      assert.equal(rotateButton.dataset.token, '')
      assert.equal(rotateButton.dataset.expiresAt, '')
    } finally {
      api.get = originalGet
      api.post = originalPost
      await act(async () => root.unmount())
      container.remove()
    }
  })
})
