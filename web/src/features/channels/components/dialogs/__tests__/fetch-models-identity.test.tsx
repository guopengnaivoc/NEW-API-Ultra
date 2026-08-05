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

import type { FetchModelsResponse } from '../../../types'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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

const { act, useEffect, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { channelSchema } = await import('../../../types')
const { ChannelsProvider, useChannels } =
  await import('../../channels-provider')
const { FetchModelsDialog } = await import('../fetch-models-dialog')

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

const channelA = channelSchema.parse({
  id: 101,
  type: 1,
  key: 'key-a',
  status: 1,
  name: 'channel-a',
  created_time: 1,
  test_time: 1,
  response_time: 1,
  balance_updated_time: 1,
  models: 'model-a',
})

const channelB = channelSchema.parse({
  id: 202,
  type: 1,
  key: 'key-b',
  status: 1,
  name: 'channel-b',
  created_time: 1,
  test_time: 1,
  response_time: 1,
  balance_updated_time: 1,
  models: 'model-b',
})

type Channel = typeof channelA

type FetchModelsAxiosResponse = {
  data: FetchModelsResponse
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve
  })
  return { promise, resolve }
}

function ignoreOpenChange() {}

function CurrentRowHarness(props: {
  channel: Channel
  onOpenChange?: (open: boolean) => void
}) {
  const { setCurrentRow } = useChannels()

  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])

  return (
    <FetchModelsDialog
      open
      onOpenChange={props.onOpenChange ?? ignoreOpenChange}
    />
  )
}

function FormFillReopenHarness(props: {
  customFetcher: (signal: AbortSignal) => Promise<string[]>
  onModelsSelected: (models: string[]) => void
  onClose: () => void
}) {
  const [open, setOpen] = useState(true)
  const [existingModels, setExistingModels] = useState(['old-model'])

  return (
    <>
      <button
        type='button'
        data-target='reopen-form-fill'
        onClick={() => setOpen(true)}
      >
        Reopen form fill
      </button>
      <FetchModelsDialog
        open={open}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            props.onClose()
            setExistingModels(['new-model'])
          }
          setOpen(nextOpen)
        }}
        onModelsSelected={props.onModelsSelected}
        customFetcher={props.customFetcher}
        existingModelsOverride={existingModels}
      />
    </>
  )
}

function findButton(label: string): HTMLButtonElement | undefined {
  return [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent?.trim() === label
  )
}

describe('fetch models dialog ownership', () => {
  after(() => {
    domWindow.close()
  })

  test('keeps standalone save unavailable until the visible channel owns fetched models', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<FetchModelsAxiosResponse>()
    const requestB = deferred<FetchModelsAxiosResponse>()
    const requestConfigs = new Map<number, ApiRequestConfig>()
    const updateBodies: unknown[] = []

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<FetchModelsAxiosResponse> => {
      const id = Number(url.split('/').at(-1))
      assert.ok(id === 101 || id === 202)
      assert.ok(config)
      requestConfigs.set(id, config)
      return id === 101 ? requestA.promise : requestB.promise
    }) as typeof api.get
    api.put = (async (
      url: string,
      body?: unknown
    ): Promise<FetchModelsAxiosResponse> => {
      assert.equal(url, '/api/channel/')
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
                <CurrentRowHarness channel={channelA} />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await act(async () => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <ChannelsProvider>
                <CurrentRowHarness channel={channelB} />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })

      assert.equal(requestConfigs.size, 2)

      await act(async () => {
        requestA.resolve({
          data: { success: true, data: ['model-a'] },
        })
        await requestA.promise
      })

      const staleSave = findButton('Save Models')
      assert.equal(staleSave?.disabled === false, false)

      await act(async () => {
        requestB.resolve({
          data: { success: true, data: ['model-b'] },
        })
        await requestB.promise
      })

      const save = findButton('Save Models')
      assert.ok(save)
      assert.equal(save.disabled, false)
      await act(async () => {
        save.click()
        await Promise.resolve()
      })

      const channelARequest = requestConfigs.get(101)
      const channelBRequest = requestConfigs.get(202)
      assert.deepEqual(updateBodies, [{ id: 202, models: 'model-b' }])
      assert.equal(channelARequest?.signal?.aborted, true)
      assert.equal(channelARequest?.disableDuplicate, true)
      assert.equal(channelBRequest?.disableDuplicate, true)
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    }
  })

  test('fills the form only with models owned by the current custom fetcher', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<string[]>()
    const requestB = deferred<string[]>()
    const selectedCalls: string[][] = []
    let signalA: AbortSignal | undefined
    let signalB: AbortSignal | undefined
    const customFetcherA = (signal: AbortSignal): Promise<string[]> => {
      signalA = signal
      return requestA.promise
    }
    const customFetcherB = (signal: AbortSignal): Promise<string[]> => {
      signalB = signal
      return requestB.promise
    }
    const onModelsSelected = (models: string[]) => {
      selectedCalls.push(models)
    }

    api.get = (async (): Promise<FetchModelsAxiosResponse> => {
      assert.fail('custom model fetching must not issue a standalone GET')
    }) as typeof api.get
    api.put = (async (): Promise<FetchModelsAxiosResponse> => {
      assert.fail('form filling must not update a channel directly')
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
                <FetchModelsDialog
                  open
                  onOpenChange={ignoreOpenChange}
                  onModelsSelected={onModelsSelected}
                  customFetcher={customFetcherA}
                  existingModelsOverride={['model-a']}
                />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await act(async () => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <ChannelsProvider>
                <FetchModelsDialog
                  open
                  onOpenChange={ignoreOpenChange}
                  onModelsSelected={onModelsSelected}
                  customFetcher={customFetcherB}
                  existingModelsOverride={['model-b']}
                />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })

      await act(async () => {
        requestB.resolve(['model-b'])
        await requestB.promise
      })
      await act(async () => {
        requestA.resolve(['model-a'])
        await requestA.promise
      })

      const save = findButton('Save Models')
      assert.ok(save)
      assert.equal(save.disabled, false)
      await act(async () => {
        save.click()
        await Promise.resolve()
      })

      assert.deepEqual(selectedCalls, [['model-b']])
      assert.equal(signalA?.aborted, true)
      assert.equal(signalB?.aborted, true)
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    }
  })

  test('invalidates form-fill ownership before closing and reloads a same-fetcher reopen', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const firstRequest = deferred<string[]>()
    const reopenedRequest = deferred<string[]>()
    const requests = [firstRequest, reopenedRequest]
    const signals: AbortSignal[] = []
    const signalWasAbortedAtClose: boolean[] = []
    const selectedCalls: string[][] = []

    const customFetcher = (signal: AbortSignal): Promise<string[]> => {
      signals.push(signal)
      const request = requests.shift()
      assert.ok(request)
      return request.promise
    }
    const onModelsSelected = (models: string[]) => {
      selectedCalls.push(models)
    }

    api.get = (async (): Promise<FetchModelsAxiosResponse> => {
      assert.fail('custom model fetching must not issue a standalone GET')
    }) as typeof api.get
    api.put = (async (): Promise<FetchModelsAxiosResponse> => {
      assert.fail('form filling must not update a channel directly')
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
                <FormFillReopenHarness
                  customFetcher={customFetcher}
                  onModelsSelected={onModelsSelected}
                  onClose={() => {
                    signalWasAbortedAtClose.push(signals[0]?.aborted === true)
                  }}
                />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await act(async () => {
        firstRequest.resolve(['old-model'])
        await firstRequest.promise
      })

      const firstSave = findButton('Save Models')
      assert.ok(firstSave)
      await act(async () => {
        firstSave.click()
        await Promise.resolve()
      })

      assert.deepEqual(selectedCalls, [['old-model']])
      assert.deepEqual(signalWasAbortedAtClose, [true])

      const reopen = document.querySelector<HTMLButtonElement>(
        '[data-target="reopen-form-fill"]'
      )
      assert.ok(reopen)
      await act(async () => {
        reopen.click()
        await Promise.resolve()
      })

      assert.equal(signals.length, 2)
      assert.equal(signals[0]?.aborted, true)
      assert.equal(signals[1]?.aborted, false)
      assert.equal(findButton('Save Models'), undefined)
      assert.doesNotMatch(document.body.textContent || '', /old-model/)

      await act(async () => {
        reopenedRequest.resolve(['new-model'])
        await reopenedRequest.promise
      })

      const reopenedSave = findButton('Save Models')
      assert.ok(reopenedSave)
      await act(async () => {
        reopenedSave.click()
        await Promise.resolve()
      })
      assert.deepEqual(selectedCalls, [['old-model'], ['new-model']])
    } finally {
      firstRequest.resolve(['old-model'])
      reopenedRequest.resolve(['new-model'])
      await act(async () => {
        await Promise.all([firstRequest.promise, reopenedRequest.promise])
        root.unmount()
      })
      api.get = originalGet
      api.put = originalPut
      container.remove()
      queryClient.clear()
    }
  })

  test('rejects a mixed custom model list without exposing form-fill save', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const request = deferred<string[]>()
    const selectedCalls: string[][] = []
    const invalidModels = ['valid-model', 42] as unknown as string[]
    const customFetcher = (): Promise<string[]> => request.promise
    const onModelsSelected = (models: string[]) => {
      selectedCalls.push(models)
    }

    api.get = (async (): Promise<FetchModelsAxiosResponse> => {
      assert.fail('custom model fetching must not issue a standalone GET')
    }) as typeof api.get
    api.put = (async (): Promise<FetchModelsAxiosResponse> => {
      assert.fail('invalid form-fill models must not update a channel')
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
                <FetchModelsDialog
                  open
                  onOpenChange={ignoreOpenChange}
                  onModelsSelected={onModelsSelected}
                  customFetcher={customFetcher}
                  existingModelsOverride={['valid-model']}
                />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })

      let renderError: unknown
      try {
        await act(async () => {
          request.resolve(invalidModels)
          await request.promise
        })
      } catch (error: unknown) {
        renderError = error
      }

      assert.equal(renderError, undefined)
      const save = findButton('Save Models')
      assert.equal(save?.disabled === false, false)
      assert.deepEqual(selectedCalls, [])
      assert.ok(findButton('Fetch Models'))
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    }
  })

  test('keeps B ready when A standalone save completes after retargeting', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requestA = deferred<FetchModelsAxiosResponse>()
    const requestB = deferred<FetchModelsAxiosResponse>()
    const updateA = deferred<FetchModelsAxiosResponse>()
    const requestConfigs = new Map<number, ApiRequestConfig>()
    const updateBodies: unknown[] = []
    const openChanges: boolean[] = []
    const onOpenChange = (open: boolean) => {
      openChanges.push(open)
    }

    api.get = (async (
      url: string,
      config?: ApiRequestConfig
    ): Promise<FetchModelsAxiosResponse> => {
      const id = Number(url.split('/').at(-1))
      assert.ok(id === 101 || id === 202)
      assert.ok(config)
      requestConfigs.set(id, config)
      return id === 101 ? requestA.promise : requestB.promise
    }) as typeof api.get
    api.put = (async (
      url: string,
      body?: unknown
    ): Promise<FetchModelsAxiosResponse> => {
      assert.equal(url, '/api/channel/')
      updateBodies.push(body)
      return updateA.promise
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
                <CurrentRowHarness
                  channel={channelA}
                  onOpenChange={onOpenChange}
                />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await act(async () => {
        requestA.resolve({
          data: { success: true, data: ['model-a'] },
        })
        await requestA.promise
      })

      const saveA = findButton('Save Models')
      assert.ok(saveA)
      await act(async () => {
        saveA.click()
        await Promise.resolve()
      })
      assert.deepEqual(updateBodies, [{ id: 101, models: 'model-a' }])

      await act(async () => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <ChannelsProvider>
                <CurrentRowHarness
                  channel={channelB}
                  onOpenChange={onOpenChange}
                />
              </ChannelsProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
        await Promise.resolve()
      })
      await act(async () => {
        requestB.resolve({
          data: { success: true, data: ['model-b'] },
        })
        await requestB.promise
      })

      await act(async () => {
        updateA.resolve({ data: { success: true } })
        await updateA.promise
        await Promise.resolve()
      })

      const saveB = findButton('Save Models')
      assert.ok(saveB)
      assert.equal(saveB.disabled, false)
      assert.match(document.body.textContent || '', /channel-b/)
      assert.match(document.body.textContent || '', /model-b/)
      assert.deepEqual(openChanges, [])
      assert.equal(requestConfigs.get(101)?.signal?.aborted, true)
      assert.equal(requestConfigs.get(202)?.signal?.aborted, false)
    } finally {
      api.get = originalGet
      api.put = originalPut
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    }
  })
})
