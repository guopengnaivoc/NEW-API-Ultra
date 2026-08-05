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

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'SVGElement',
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
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ChannelsProvider, useChannels } =
  await import('../../channels-provider')
const { TagBatchEditDialog } = await import('../tag-batch-edit-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {},
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type GetAxiosResponse = {
  data: {
    success: boolean
    message?: string
    data?: string | string[] | Array<{ id: string }>
  }
}

type PutAxiosResponse = {
  data: {
    success: boolean
    message?: string
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve
  })
  return { promise, resolve }
}

function TagBatchEditHarness() {
  const { setCurrentTag } = useChannels()
  const [open, setOpen] = useState(false)

  return (
    <>
      <button
        type='button'
        data-target='A'
        onClick={() => {
          setCurrentTag('A')
          setOpen(true)
        }}
      >
        A
      </button>
      <button
        type='button'
        data-target='B'
        onClick={() => {
          setCurrentTag('B')
          setOpen(true)
        }}
      >
        B
      </button>
      <TagBatchEditDialog open={open} onOpenChange={setOpen} />
    </>
  )
}

function findButton(label: string): HTMLButtonElement | undefined {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent?.trim() === label
  )
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

function assertTagRequests(
  requests: Map<string, ApiRequestConfig>,
  expectedAbortedTag: string
) {
  const requestA = requests.get('A')
  const requestB = requests.get('B')
  assert.ok(requestA?.signal)
  assert.ok(requestB?.signal)
  assert.equal(requests.size, 2)
  assert.equal(requestA.signal.aborted, expectedAbortedTag === 'A')
  assert.equal(requestA.disableDuplicate, true)
  assert.equal(requestB.disableDuplicate, true)
}

describe('tag batch edit dialog ownership', () => {
  after(() => {
    domWindow.close()
  })

  test('ignores late A and submits B fields to B', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<GetAxiosResponse>()
    const requestB = deferred<GetAxiosResponse>()
    const getConfigs = new Map<string, ApiRequestConfig>()
    const updateBodies: unknown[] = []
    let allModelsGetCount = 0

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<GetAxiosResponse> => {
      if (url === '/api/group/') {
        return { data: { success: true, data: [] } }
      }
      if (url === '/api/channel/models') {
        allModelsGetCount += 1
        return { data: { success: true, data: [] } }
      }

      assert.equal(url, '/api/channel/tag/models')
      const tag = config?.params?.tag
      assert.ok(tag === 'A' || tag === 'B')
      assert.ok(config)
      getConfigs.set(tag, config)
      return tag === 'A' ? requestA.promise : requestB.promise
    }) as typeof api.get
    api.put = (async (
      url: string,
      body?: unknown
    ): Promise<PutAxiosResponse> => {
      assert.equal(url, '/api/channel/tag')
      updateBodies.push(body)
      return { data: { success: true } }
    }) as typeof api.put

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    try {
      await act(async () => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <ChannelsProvider>
                <TagBatchEditHarness />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await switchFromAToB()

      await act(async () => {
        requestB.resolve({
          data: { success: true, data: 'b-models' },
        })
        await requestB.promise
        await Promise.resolve()
      })

      const newTag = document.querySelector<HTMLInputElement>('#new-tag')
      assert.ok(newTag)
      const valueSetter = Object.getOwnPropertyDescriptor(
        domWindow.HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(valueSetter)
      await act(async () => {
        valueSetter.call(newTag, 'B-renamed')
        newTag.dispatchEvent(new Event('input', { bubbles: true }))
      })

      await act(async () => {
        requestA.resolve({
          data: { success: true, data: 'a-models' },
        })
        await requestA.promise
        await Promise.resolve()
      })

      const save = findButton('Save Changes')
      assert.ok(save)
      assert.equal(save.disabled, false)
      await act(async () => {
        save.click()
        await Promise.resolve()
        await Promise.resolve()
      })

      assert.deepEqual(updateBodies, [
        {
          tag: 'B',
          new_tag: 'B-renamed',
          models: 'b-models',
        },
      ])
      assertTagRequests(getConfigs, 'A')
      assert.equal(allModelsGetCount, 0)
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    }
  })

  test('keeps Save unavailable for stale A and accepts empty B models', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<GetAxiosResponse>()
    const requestB = deferred<GetAxiosResponse>()
    const getConfigs = new Map<string, ApiRequestConfig>()
    let allModelsGetCount = 0

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<GetAxiosResponse> => {
      if (url === '/api/group/') {
        return { data: { success: true, data: [] } }
      }
      if (url === '/api/channel/models') {
        allModelsGetCount += 1
        return { data: { success: true, data: [] } }
      }

      assert.equal(url, '/api/channel/tag/models')
      const tag = config?.params?.tag
      assert.ok(tag === 'A' || tag === 'B')
      assert.ok(config)
      getConfigs.set(tag, config)
      return tag === 'A' ? requestA.promise : requestB.promise
    }) as typeof api.get
    api.put = (async (): Promise<PutAxiosResponse> => {
      assert.fail('loading ownership checks must not update tags')
    }) as typeof api.put

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    try {
      await act(async () => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <ChannelsProvider>
                <TagBatchEditHarness />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await switchFromAToB()

      await act(async () => {
        requestA.resolve({
          data: { success: true, data: 'a-models' },
        })
        await requestA.promise
        await Promise.resolve()
      })

      const staleSave = findButton('Save Changes')
      assert.equal(staleSave?.disabled === false, false)

      await act(async () => {
        requestB.resolve({
          data: { success: true, data: '' },
        })
        await requestB.promise
        await Promise.resolve()
      })

      const models = document.querySelector<HTMLTextAreaElement>('#models')
      assert.ok(models)
      assert.equal(models.value, '')
      assertTagRequests(getConfigs, 'A')
      assert.equal(allModelsGetCount, 0)
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    }
  })
})
