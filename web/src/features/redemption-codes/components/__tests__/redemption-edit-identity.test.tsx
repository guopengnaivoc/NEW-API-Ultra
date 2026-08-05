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

import { api, type ApiRequestConfig } from '@/lib/api'

import type { ApiResponse, Redemption } from '../../types'

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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { RedemptionsMutateDrawer } = await import('../redemptions-mutate-drawer')
const { RedemptionsProvider } = await import('../redemptions-provider')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {},
    },
    fr: {
      translation: {},
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalSystemConfig = useSystemConfigStore.getState().config
useSystemConfigStore.setState({
  config: {
    ...originalSystemConfig,
    currency: {
      ...originalSystemConfig.currency,
      quotaDisplayType: 'TOKENS',
    },
  },
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve
  })
  return { promise, resolve }
}

function redemption(id: number, name: string): Redemption {
  return {
    id,
    user_id: 1,
    name,
    key: `key-${id}`,
    status: 1,
    quota: 500000,
    created_time: 1_700_000_000,
    redeemed_time: 0,
    expired_time: 0,
    used_user_id: 0,
  }
}

const rowA = redemption(101, 'redemption-a')
const rowB = redemption(202, 'redemption-b')

type RedemptionResponse = {
  data: ApiResponse<Redemption>
}

function RedemptionDrawerHarness() {
  const [open, setOpen] = useState(false)
  const [currentRow, setCurrentRow] = useState<Redemption>()

  return (
    <>
      <button
        type='button'
        data-target='A'
        onClick={() => {
          setCurrentRow(rowA)
          setOpen(true)
        }}
      >
        A
      </button>
      <button
        type='button'
        data-target='B'
        onClick={() => {
          setCurrentRow(rowB)
          setOpen(true)
        }}
      >
        B
      </button>
      <RedemptionsMutateDrawer
        open={open}
        onOpenChange={setOpen}
        currentRow={currentRow}
      />
    </>
  )
}

function findButton(label: string): HTMLButtonElement {
  const button = [...document.querySelectorAll('button')].find(
    (candidate) => candidate.textContent?.trim() === label
  )
  assert.ok(button)
  return button
}

function assertRequestConfig(
  config: ApiRequestConfig | undefined,
  aborted: boolean
) {
  assert.ok(config?.signal)
  assert.equal(config.signal.aborted, aborted)
  assert.equal(config.disableDuplicate, true)
  assert.equal(config.skipBusinessError, true)
  assert.equal(config.skipErrorHandler, true)
}

async function switchFromAToB() {
  const targetA = document.querySelector<HTMLButtonElement>('[data-target="A"]')
  const targetB = document.querySelector<HTMLButtonElement>('[data-target="B"]')
  assert.ok(targetA)
  assert.ok(targetB)

  await act(async () => {
    targetA.click()
    await Promise.resolve()
  })
  await act(async () => {
    targetB.click()
    await Promise.resolve()
  })
}

describe('redemption edit identity', () => {
  after(() => {
    useSystemConfigStore.setState({ config: originalSystemConfig })
    domWindow.close()
  })

  test('keeps update disabled until the visible redemption owns loaded data', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<RedemptionResponse>()
    const requestB = deferred<RedemptionResponse>()
    const requests = [requestA, requestB]
    const getConfigs: Array<ApiRequestConfig | undefined> = []

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<RedemptionResponse> => {
      assert.match(url, /^\/api\/redemption\/(101|202)$/)
      getConfigs.push(config)
      const request = requests.shift()
      assert.ok(request)
      return request.promise
    }) as typeof api.get
    api.put = (async () => {
      assert.fail('update must not run in the load-readiness test')
    }) as typeof api.put

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    try {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <RedemptionsProvider>
              <RedemptionDrawerHarness />
            </RedemptionsProvider>
          </I18nextProvider>
        )
      })
      await switchFromAToB()

      const save = document.querySelector<HTMLButtonElement>(
        'button[form="redemption-form"]'
      )
      assert.ok(save)
      assert.equal(save.disabled, true)
      assert.equal(getConfigs.length, 2)
      assertRequestConfig(getConfigs[0], true)
      assertRequestConfig(getConfigs[1], false)

      await act(async () => {
        requestB.resolve({
          data: { success: true, data: rowB },
        })
        await requestB.promise
      })
      assert.equal(save.disabled, false)
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
    }
  })

  test('ignores late A and submits B fields to B', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<RedemptionResponse>()
    const requestB = deferred<RedemptionResponse>()
    const requests = [requestA, requestB]
    const getConfigs: Array<ApiRequestConfig | undefined> = []
    const updateBodies: unknown[] = []

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<RedemptionResponse> => {
      assert.match(url, /^\/api\/redemption\/(101|202)$/)
      getConfigs.push(config)
      const request = requests.shift()
      assert.ok(request)
      return request.promise
    }) as typeof api.get
    api.put = (async (
      url: string,
      body?: unknown
    ): Promise<RedemptionResponse> => {
      assert.equal(url, '/api/redemption/')
      updateBodies.push(body)
      return {
        data: { success: true, data: rowB },
      }
    }) as typeof api.put

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    try {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <RedemptionsProvider>
              <RedemptionDrawerHarness />
            </RedemptionsProvider>
          </I18nextProvider>
        )
      })
      await switchFromAToB()

      await act(async () => {
        requestB.resolve({
          data: { success: true, data: rowB },
        })
        await requestB.promise
      })
      await act(async () => {
        requestA.resolve({
          data: { success: true, data: rowA },
        })
        await requestA.promise
      })

      const name =
        document.querySelector<HTMLInputElement>('input[name="name"]')
      assert.ok(name)
      assert.equal(name.value, 'redemption-b')
      assert.equal(getConfigs.length, 2)
      assertRequestConfig(getConfigs[0], true)
      assertRequestConfig(getConfigs[1], false)

      await act(async () => {
        findButton('Save changes').click()
        await Promise.resolve()
      })
      assert.deepEqual(updateBodies, [
        {
          id: 202,
          name: 'redemption-b',
          quota: 500000,
          expired_time: 0,
          count: 1,
        },
      ])
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
    }
  })

  test('keeps an in-progress edit when the language changes', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const initialRequest = deferred<RedemptionResponse>()
    const unexpectedReload = deferred<RedemptionResponse>()
    const requests = [initialRequest, unexpectedReload]
    const getConfigs: Array<ApiRequestConfig | undefined> = []

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<RedemptionResponse> => {
      assert.equal(url, '/api/redemption/101')
      getConfigs.push(config)
      const request = requests.shift()
      assert.ok(request)
      return request.promise
    }) as typeof api.get
    api.put = (async () => {
      assert.fail('language changes must not submit an update')
    }) as typeof api.put

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    try {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <RedemptionsProvider>
              <RedemptionDrawerHarness />
            </RedemptionsProvider>
          </I18nextProvider>
        )
      })

      const targetA =
        document.querySelector<HTMLButtonElement>('[data-target="A"]')
      assert.ok(targetA)
      await act(async () => {
        targetA.click()
        await Promise.resolve()
      })
      await act(async () => {
        initialRequest.resolve({
          data: { success: true, data: rowA },
        })
        await initialRequest.promise
      })

      const name =
        document.querySelector<HTMLInputElement>('input[name="name"]')
      assert.ok(name)
      const valueSetter = Object.getOwnPropertyDescriptor(
        domWindow.HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(valueSetter)
      await act(async () => {
        valueSetter.call(name, 'operator-edit')
        name.dispatchEvent(new Event('input', { bubbles: true }))
      })
      assert.equal(name.value, 'operator-edit')

      await act(async () => {
        await i18n.changeLanguage('fr')
      })

      assert.deepEqual(
        {
          name: name.value,
          getCount: getConfigs.length,
        },
        {
          name: 'operator-edit',
          getCount: 1,
        }
      )
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => {
        root.unmount()
        await i18n.changeLanguage('en')
      })
      container.remove()
    }
  })
})
