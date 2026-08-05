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
import type { ReactNode } from 'react'

import { api, type ApiRequestConfig } from '@/lib/api'

import type {
  MultiKeyManageParams,
  MultiKeyStatusResponse,
} from '../../../types'

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

const { act, Component, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { Toaster, toast } = await import('sonner')
const { channelsQueryKeys } = await import('../../../lib')
const { channelSchema } = await import('../../../types')
const { ChannelsProvider, useChannels } =
  await import('../../channels-provider')
const { MultiKeyManageDialog } = await import('../multi-key-manage-dialog')

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

type MultiKeyAxiosResponse = {
  data:
    | MultiKeyStatusResponse
    | { success: boolean; message?: string; data?: number }
}

type CapturedPost = {
  body: MultiKeyManageParams
  config: ApiRequestConfig | undefined
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, reject, resolve }
}

function keyStatusResponse(
  index: number,
  options: {
    aggregateCount?: number
    page?: number
    pageSize?: number
    total?: number
    totalPages?: number
    status?: number
  } = {}
): MultiKeyAxiosResponse {
  const status = options.status ?? 1
  const aggregateCount = options.aggregateCount ?? index + 1
  return {
    data: {
      success: true,
      data: {
        keys: [{ index, status }],
        total: options.total ?? aggregateCount,
        page: options.page ?? 1,
        page_size: options.pageSize ?? 10,
        total_pages: options.totalPages ?? 1,
        enabled_count: status === 1 ? aggregateCount : 0,
        manual_disabled_count: status === 2 ? aggregateCount : 0,
        auto_disabled_count: status === 3 ? aggregateCount : 0,
      },
    },
  }
}

function nullKeysResponse(): MultiKeyAxiosResponse {
  return {
    data: {
      success: true,
      data: {
        keys: null,
        total: 0,
        page: 1,
        page_size: 25,
        total_pages: 1,
        enabled_count: 0,
        manual_disabled_count: 0,
        auto_disabled_count: 0,
      },
    },
  } as unknown as MultiKeyAxiosResponse
}

function malformedStatusResponse(
  message: string,
  override: Record<string, unknown>
): MultiKeyAxiosResponse {
  return {
    data: {
      success: true,
      message,
      data: {
        keys: [],
        total: 0,
        page: 1,
        page_size: 25,
        total_pages: 1,
        enabled_count: 0,
        manual_disabled_count: 0,
        auto_disabled_count: 0,
        ...override,
      },
    },
  } as unknown as MultiKeyAxiosResponse
}

function malformedRowsResponse(
  message: string,
  keys: unknown[],
  override: Record<string, unknown> = {}
): MultiKeyAxiosResponse {
  return malformedStatusResponse(message, {
    keys,
    total: keys.length,
    enabled_count: keys.length,
    ...override,
  })
}

class MultiKeyErrorBoundary extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    return this.state.failed ? (
      <div>Multi-key dialog render failed</div>
    ) : (
      this.props.children
    )
  }
}

function MultiKeyHarness(props: { onDialogClose?: () => void }) {
  const { setCurrentRow } = useChannels()
  const [open, setOpen] = useState(false)

  return (
    <>
      <button
        type='button'
        data-target='A'
        onClick={() => {
          setCurrentRow(channelA)
          setOpen(true)
        }}
      >
        Open A
      </button>
      <button
        type='button'
        data-target='B'
        onClick={() => {
          setCurrentRow(channelB)
          setOpen(true)
        }}
      >
        Open B
      </button>
      <MultiKeyErrorBoundary>
        <MultiKeyManageDialog
          open={open}
          onOpenChange={(nextOpen) => {
            if (!nextOpen) props.onDialogClose?.()
            setOpen(nextOpen)
          }}
        />
      </MultiKeyErrorBoundary>
    </>
  )
}

function findButton(label: string): HTMLButtonElement | undefined {
  return [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent?.trim() === label
  )
}

function findRefreshButton(): HTMLButtonElement | undefined {
  return [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.querySelector('.lucide-refresh-cw') !== null
  )
}

async function clickTarget(target: 'A' | 'B') {
  const button = document.querySelector<HTMLButtonElement>(
    `[data-target="${target}"]`
  )
  assert.ok(button)
  await act(async () => {
    button.click()
    await Promise.resolve()
  })
}

async function selectStatus(label: string) {
  const trigger = document.body.querySelector<HTMLButtonElement>(
    '[data-slot="select-trigger"]'
  )
  assert.ok(trigger)
  await act(async () => {
    trigger.click()
    await Promise.resolve()
  })

  const item = [
    ...document.body.querySelectorAll<HTMLElement>('[data-slot="select-item"]'),
  ].find((option) => option.textContent?.trim() === label)
  assert.ok(item)
  await act(async () => {
    item.click()
    await Promise.resolve()
  })
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

async function renderHarness(
  options: {
    queryClient?: InstanceType<typeof QueryClient>
    showToasts?: boolean
    onDialogClose?: () => void
  } = {}
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = options.queryClient ?? createTestQueryClient()

  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <ChannelsProvider>
            <MultiKeyHarness onDialogClose={options.onDialogClose} />
          </ChannelsProvider>
          {options.showToasts ? <Toaster duration={60_000} /> : null}
        </I18nextProvider>
      </QueryClientProvider>
    )
    await Promise.resolve()
  })

  return async () => {
    toast.dismiss()
    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
  }
}

async function renderMalformedStatusResponse(
  response: MultiKeyAxiosResponse
): Promise<{
  bodyText: string
  postedActions: MultiKeyManageParams[]
  rowActionWasVisible: boolean
}> {
  const originalPost = api.post
  const postedActions: MultiKeyManageParams[] = []

  api.post = (async (
    url: string,
    body?: unknown
  ): Promise<MultiKeyAxiosResponse> => {
    assert.equal(url, '/api/channel/multi_key/manage')
    const action = body as MultiKeyManageParams
    postedActions.push(action)
    assert.equal(action.action, 'get_key_status')
    return response
  }) as typeof api.post

  const cleanup = await renderHarness({ showToasts: true })
  try {
    await clickTarget('B')
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    return {
      bodyText: document.body.textContent ?? '',
      postedActions,
      rowActionWasVisible:
        findButton('Disable') !== undefined ||
        findButton('Enable') !== undefined ||
        findButton('Delete') !== undefined,
    }
  } finally {
    api.post = originalPost
    await cleanup()
  }
}

async function assertMalformedStatusRejected(
  message: string,
  response: MultiKeyAxiosResponse
) {
  const rendered = await renderMalformedStatusResponse(response)

  assert.doesNotMatch(rendered.bodyText, /Multi-key dialog render failed/)
  assert.match(rendered.bodyText, new RegExp(message))
  assert.equal(rendered.rowActionWasVisible, false)
  assert.deepEqual(
    rendered.postedActions.map((action) => action.action),
    ['get_key_status']
  )
}

async function capturePageRetryRequests(failureMode: 'business' | 'transport') {
  const originalPost = api.post
  const pageOne = deferred<MultiKeyAxiosResponse>()
  const failedPageTwo = deferred<MultiKeyAxiosResponse>()
  const retryPageTwo = deferred<MultiKeyAxiosResponse>()
  const capturedPosts: CapturedPost[] = []
  let pageTwoAttempt = 0

  api.post = (async (
    url: string,
    body?: unknown,
    config?: ApiRequestConfig
  ): Promise<MultiKeyAxiosResponse> => {
    assert.equal(url, '/api/channel/multi_key/manage')
    const action = body as MultiKeyManageParams
    assert.equal(action.action, 'get_key_status')
    capturedPosts.push({ body: action, config })
    if (action.page === 2) {
      pageTwoAttempt += 1
      return pageTwoAttempt === 1 ? failedPageTwo.promise : retryPageTwo.promise
    }
    return pageOne.promise
  }) as typeof api.post

  const cleanup = await renderHarness()
  try {
    await clickTarget('B')
    await act(async () => {
      pageOne.resolve(
        keyStatusResponse(7, {
          page: 1,
          pageSize: 25,
          totalPages: 2,
        })
      )
      await pageOne.promise
      await Promise.resolve()
    })

    const next = findButton('Next')
    assert.ok(next)
    await act(async () => {
      next.click()
      await Promise.resolve()
    })
    await act(async () => {
      if (failureMode === 'business') {
        failedPageTwo.resolve({
          data: {
            success: false,
            message: 'page two failed',
          },
        })
      } else {
        failedPageTwo.reject(new Error('page two transport failed'))
      }
      await failedPageTwo.promise.catch(() => undefined)
      await Promise.resolve()
    })

    const refresh = findRefreshButton()
    assert.ok(refresh)
    assert.equal(refresh.disabled, false)
    await act(async () => {
      refresh.click()
      await Promise.resolve()
    })
  } finally {
    pageOne.resolve(
      keyStatusResponse(7, {
        page: 1,
        pageSize: 25,
        totalPages: 2,
      })
    )
    if (failureMode === 'business') {
      failedPageTwo.resolve({
        data: {
          success: false,
          message: 'page two failed',
        },
      })
    } else {
      failedPageTwo.reject(new Error('page two transport failed'))
    }
    retryPageTwo.resolve(
      keyStatusResponse(8, {
        page: 2,
        pageSize: 25,
        totalPages: 2,
      })
    )
    await act(async () => {
      await Promise.allSettled([
        pageOne.promise,
        failedPageTwo.promise,
        retryPageTwo.promise,
      ])
      await Promise.resolve()
    })
    api.post = originalPost
    await cleanup()
  }

  return capturedPosts.map((post) => ({
    page: post.body.page,
    pageSize: post.body.page_size,
  }))
}

describe('multi-key management dialog ownership', () => {
  after(() => {
    domWindow.close()
  })

  test('hides A rows and actions as soon as B becomes the visible channel', async () => {
    const originalPost = api.post
    const requestA = deferred<MultiKeyAxiosResponse>()
    const requestB = deferred<MultiKeyAxiosResponse>()
    let pendingBodyText = ''
    let pendingHasDisable = false
    let pendingHasEnableAll = false
    let pendingHasDisableAll = false
    let readyBodyText = ''

    api.post = (async (
      url: string,
      body?: unknown
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      assert.equal(action.action, 'get_key_status')
      return action.channel_id === channelA.id
        ? requestA.promise
        : requestB.promise
    }) as typeof api.post

    const cleanup = await renderHarness()
    try {
      await clickTarget('A')
      await act(async () => {
        requestA.resolve(keyStatusResponse(3))
        await requestA.promise
        await Promise.resolve()
      })
      assert.match(document.body.textContent ?? '', /#4/)
      assert.ok(findButton('Disable'))
      assert.ok(findButton('Disable All'))

      await clickTarget('B')

      pendingBodyText = document.body.textContent ?? ''
      pendingHasDisable = findButton('Disable') !== undefined
      pendingHasEnableAll = findButton('Enable All') !== undefined
      pendingHasDisableAll = findButton('Disable All') !== undefined

      await act(async () => {
        requestB.resolve(keyStatusResponse(7))
        await requestB.promise
        await Promise.resolve()
      })
      readyBodyText = document.body.textContent ?? ''
    } finally {
      requestA.resolve(keyStatusResponse(3))
      requestB.resolve(keyStatusResponse(7))
      await act(async () => {
        await Promise.all([requestA.promise, requestB.promise])
        await Promise.resolve()
      })
      api.post = originalPost
      await cleanup()
    }

    assert.doesNotMatch(pendingBodyText, /#4/)
    assert.equal(pendingHasDisable, false)
    assert.equal(pendingHasEnableAll, false)
    assert.equal(pendingHasDisableAll, false)
    assert.match(readyBodyText, /#8/)
    assert.doesNotMatch(readyBodyText, /#4/)
  })

  test('ignores an A response that settles after B is ready', async () => {
    const originalPost = api.post
    const requestA = deferred<MultiKeyAxiosResponse>()
    const requestB = deferred<MultiKeyAxiosResponse>()
    const capturedPosts: CapturedPost[] = []
    let readyBodyText = ''
    let requestAWasAborted = false
    let requestBHadSignal = false

    api.post = (async (
      url: string,
      body?: unknown,
      config?: ApiRequestConfig
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      assert.equal(action.action, 'get_key_status')
      capturedPosts.push({ body: action, config })
      return action.channel_id === channelA.id
        ? requestA.promise
        : requestB.promise
    }) as typeof api.post

    const cleanup = await renderHarness()
    try {
      await clickTarget('A')
      await clickTarget('B')

      assert.deepEqual(
        capturedPosts.map((post) => post.body.channel_id),
        [channelA.id, channelB.id]
      )

      await act(async () => {
        requestB.resolve(keyStatusResponse(7))
        await requestB.promise
        await Promise.resolve()
      })
      assert.match(document.body.textContent ?? '', /#8/)

      await act(async () => {
        requestA.resolve(keyStatusResponse(3))
        await requestA.promise
        await Promise.resolve()
      })

      readyBodyText = document.body.textContent ?? ''
      requestAWasAborted = capturedPosts[0]?.config?.signal?.aborted === true
      requestBHadSignal = capturedPosts[1]?.config?.signal !== undefined
    } finally {
      requestA.resolve(keyStatusResponse(3))
      requestB.resolve(keyStatusResponse(7))
      await act(async () => {
        await Promise.all([requestA.promise, requestB.promise])
        await Promise.resolve()
      })
      api.post = originalPost
      await cleanup()
    }

    assert.match(readyBodyText, /#8/)
    assert.doesNotMatch(readyBodyText, /#4/)
    assert.equal(requestAWasAborted, true)
    assert.equal(requestBHadSignal, true)
  })

  test('removes an A confirmation after retargeting to B without dispatching it', async () => {
    const originalPost = api.post
    const requestA = deferred<MultiKeyAxiosResponse>()
    const requestB = deferred<MultiKeyAxiosResponse>()
    const postedActions: MultiKeyManageParams[] = []
    let continueWasVisible = false
    let disableAllDispatches: MultiKeyManageParams[] = []

    api.post = (async (
      url: string,
      body?: unknown
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      postedActions.push(action)
      if (action.action === 'get_key_status') {
        return action.channel_id === channelA.id
          ? requestA.promise
          : requestB.promise
      }
      return { data: { success: true } }
    }) as typeof api.post

    const cleanup = await renderHarness()
    try {
      await clickTarget('A')
      await act(async () => {
        requestA.resolve(keyStatusResponse(3))
        await requestA.promise
        await Promise.resolve()
      })

      const disableAll = findButton('Disable All')
      assert.ok(disableAll)
      await act(async () => {
        disableAll.click()
        await Promise.resolve()
      })
      assert.ok(findButton('Continue'))

      await clickTarget('B')

      continueWasVisible = findButton('Continue') !== undefined
      disableAllDispatches = postedActions.filter(
        (action) => action.action === 'disable_all_keys'
      )
    } finally {
      requestA.resolve(keyStatusResponse(3))
      requestB.resolve(keyStatusResponse(7))
      await act(async () => {
        await Promise.all([requestA.promise, requestB.promise])
        await Promise.resolve()
      })
      api.post = originalPost
      await cleanup()
    }

    assert.equal(continueWasVisible, false)
    assert.deepEqual(disableAllDispatches, [])
  })

  test('keeps the latest B filter when an older page request resolves last', async () => {
    const originalPost = api.post
    const pageOne = deferred<MultiKeyAxiosResponse>()
    const pageTwo = deferred<MultiKeyAxiosResponse>()
    const enabledPageOne = deferred<MultiKeyAxiosResponse>()
    const capturedPosts: CapturedPost[] = []
    let filteredBodyText = ''
    let finalBodyText = ''
    let olderPageWasAborted = false
    let filteredPageHadLiveSignal = false

    api.post = (async (
      url: string,
      body?: unknown,
      config?: ApiRequestConfig
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      assert.equal(action.action, 'get_key_status')
      capturedPosts.push({ body: action, config })
      if (action.page === 2) return pageTwo.promise
      if (action.status === 1) return enabledPageOne.promise
      return pageOne.promise
    }) as typeof api.post

    const cleanup = await renderHarness()
    try {
      await clickTarget('B')
      await act(async () => {
        pageOne.resolve(
          keyStatusResponse(7, {
            page: 1,
            pageSize: 25,
            totalPages: 2,
          })
        )
        await pageOne.promise
        await Promise.resolve()
      })

      const next = findButton('Next')
      assert.ok(next)
      await act(async () => {
        next.click()
        await Promise.resolve()
      })
      await selectStatus('Enabled')

      await act(async () => {
        enabledPageOne.resolve(
          keyStatusResponse(9, {
            page: 1,
            pageSize: 25,
            totalPages: 1,
          })
        )
        await enabledPageOne.promise
        await Promise.resolve()
      })
      filteredBodyText = document.body.textContent ?? ''

      await act(async () => {
        pageTwo.resolve(
          keyStatusResponse(8, {
            page: 2,
            pageSize: 25,
            totalPages: 2,
          })
        )
        await pageTwo.promise
        await Promise.resolve()
      })

      finalBodyText = document.body.textContent ?? ''
      olderPageWasAborted = capturedPosts[1]?.config?.signal?.aborted === true
      filteredPageHadLiveSignal =
        capturedPosts[2]?.config?.signal !== undefined &&
        capturedPosts[2]?.config?.signal?.aborted === false
    } finally {
      pageOne.resolve(
        keyStatusResponse(7, {
          page: 1,
          pageSize: 25,
          totalPages: 2,
        })
      )
      pageTwo.resolve(
        keyStatusResponse(8, {
          page: 2,
          pageSize: 25,
          totalPages: 2,
        })
      )
      enabledPageOne.resolve(
        keyStatusResponse(9, {
          page: 1,
          pageSize: 25,
          totalPages: 1,
        })
      )
      await act(async () => {
        await Promise.all([
          pageOne.promise,
          pageTwo.promise,
          enabledPageOne.promise,
        ])
        await Promise.resolve()
      })
      api.post = originalPost
      await cleanup()
    }

    assert.deepEqual(
      capturedPosts.map((post) => ({
        channelId: post.body.channel_id,
        page: post.body.page,
        pageSize: post.body.page_size,
        status: post.body.status,
      })),
      [
        {
          channelId: channelB.id,
          page: 1,
          pageSize: 10,
          status: undefined,
        },
        {
          channelId: channelB.id,
          page: 2,
          pageSize: 25,
          status: undefined,
        },
        {
          channelId: channelB.id,
          page: 1,
          pageSize: 25,
          status: 1,
        },
      ]
    )
    assert.match(filteredBodyText, /#10/)
    assert.match(finalBodyText, /#10/)
    assert.doesNotMatch(finalBodyText, /#9/)
    assert.equal(olderPageWasAborted, true)
    assert.equal(filteredPageHadLiveSignal, true)
  })

  test('renders a backend null key page as an empty ready view', async () => {
    const originalPost = api.post
    let bodyText = ''
    let disableWasVisible = false

    api.post = (async (
      url: string,
      body?: unknown
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      assert.equal(action.action, 'get_key_status')
      return nullKeysResponse()
    }) as typeof api.post

    const cleanup = await renderHarness()
    try {
      await clickTarget('B')
      await act(async () => {
        await Promise.resolve()
        await Promise.resolve()
      })
      bodyText = document.body.textContent ?? ''
      disableWasVisible = findButton('Disable') !== undefined
    } finally {
      api.post = originalPost
      await cleanup()
    }

    assert.doesNotMatch(bodyText, /Multi-key dialog render failed/)
    assert.match(bodyText, /No keys found/)
    assert.equal(disableWasVisible, false)
  })

  test('rejects a non-array key payload through the load-failure path', async () => {
    const message = 'malformed key list'
    await assertMalformedStatusRejected(
      message,
      malformedStatusResponse(message, {
        keys: { unexpected: 'truthy object' },
      })
    )
  })

  test('rejects malformed numeric status fields through the load-failure path', async () => {
    const fields = [
      'total',
      'page',
      'page_size',
      'total_pages',
      'enabled_count',
      'manual_disabled_count',
      'auto_disabled_count',
    ] as const

    for (const field of fields) {
      const message = `malformed ${field}`
      await assertMalformedStatusRejected(
        message,
        malformedStatusResponse(message, {
          [field]: { unexpected: 'truthy object' },
        })
      )
    }
  })

  const malformedRowCases: Array<{ name: string; row: unknown }> = [
    { name: 'null', row: null },
    { name: 'primitive', row: 'not-a-key-row' },
    { name: 'array', row: [0, 1] },
  ]
  for (const { name, row } of malformedRowCases) {
    test(`rejects the ${name} key row through the load-failure path`, async () => {
      const message = `malformed ${name} row`
      await assertMalformedStatusRejected(
        message,
        malformedRowsResponse(message, [row])
      )
    })
  }

  const invalidIndexCases = [
    { name: 'negative', value: -1 },
    { name: 'fractional', value: 1.5 },
    { name: 'unsafe', value: Number.MAX_SAFE_INTEGER + 1 },
  ]
  for (const { name, value } of invalidIndexCases) {
    test(`rejects the ${name} key index`, async () => {
      const message = `malformed ${name} index`
      await assertMalformedStatusRejected(
        message,
        malformedRowsResponse(message, [{ index: value, status: 1 }])
      )
    })
  }

  test('rejects duplicate key indexes on one page', async () => {
    const message = 'duplicate key index'
    await assertMalformedStatusRejected(
      message,
      malformedRowsResponse(message, [
        { index: 0, status: 1 },
        { index: 0, status: 1 },
      ])
    )
  })

  const invalidStatusCases: Array<{ name: string; value: unknown }> = [
    { name: 'zero', value: 0 },
    { name: 'out-of-range', value: 4 },
    { name: 'fractional', value: 1.5 },
    { name: 'string', value: '1' },
  ]
  for (const { name, value } of invalidStatusCases) {
    test(`rejects the ${name} key status`, async () => {
      const message = `malformed ${name} status`
      await assertMalformedStatusRejected(
        message,
        malformedRowsResponse(message, [{ index: 0, status: value }])
      )
    })
  }

  const invalidTextCases = [{ name: 'reason', field: { reason: 42 } }]
  for (const { name, field } of invalidTextCases) {
    test(`rejects a non-string optional ${name}`, async () => {
      const message = `malformed ${name}`
      await assertMalformedStatusRejected(
        message,
        malformedRowsResponse(message, [{ index: 0, status: 1, ...field }])
      )
    })
  }

  const invalidDisabledTimeCases: Array<{ name: string; value: unknown }> = [
    { name: 'negative', value: -1 },
    { name: 'fractional', value: 1.5 },
    { name: 'unsafe', value: Number.MAX_SAFE_INTEGER + 1 },
    { name: 'string', value: '1' },
    { name: 'null', value: null },
  ]
  for (const { name, value } of invalidDisabledTimeCases) {
    test(`rejects the ${name} disabled timestamp`, async () => {
      const message = `malformed ${name} disabled time`
      await assertMalformedStatusRejected(
        message,
        malformedRowsResponse(message, [
          {
            index: 0,
            status: 2,
            disabled_time: value,
          },
        ])
      )
    })
  }

  test('rejects a page that exceeds its declared capacity', async () => {
    const oversizedMessage = 'too many keys for page'
    await assertMalformedStatusRejected(
      oversizedMessage,
      malformedRowsResponse(
        oversizedMessage,
        [
          { index: 0, status: 1 },
          { index: 1, status: 1 },
        ],
        { page_size: 1 }
      )
    )
  })

  test('rejects a key index outside the aggregate key count', async () => {
    const outOfRangeMessage = 'key index outside aggregate count'
    await assertMalformedStatusRejected(
      outOfRangeMessage,
      malformedRowsResponse(outOfRangeMessage, [{ index: 2, status: 1 }], {
        total: 2,
        enabled_count: 2,
      })
    )
  })

  test('accepts valid empty and paginated key pages', async () => {
    const empty = await renderMalformedStatusResponse(
      malformedStatusResponse('valid empty page', {
        keys: [],
      })
    )
    assert.doesNotMatch(empty.bodyText, /valid empty page/)
    assert.match(empty.bodyText, /No keys found/)
    assert.equal(empty.rowActionWasVisible, false)

    const paginated = await renderMalformedStatusResponse(
      malformedStatusResponse('valid paginated page', {
        keys: [
          {
            index: 2,
            status: 2,
            disabled_time: 0,
            reason: '',
          },
        ],
        total: 3,
        page: 2,
        page_size: 2,
        total_pages: 2,
        enabled_count: 2,
        manual_disabled_count: 1,
      })
    )
    assert.doesNotMatch(paginated.bodyText, /valid paginated page/)
    assert.match(paginated.bodyText, /#3/)
    assert.match(paginated.bodyText, /Page 2 of 2/)
    assert.equal(paginated.rowActionWasVisible, true)
  })

  test('retries a failed page with its captured page and ready page size', async () => {
    assert.deepEqual(await capturePageRetryRequests('business'), [
      { page: 1, pageSize: 10 },
      { page: 2, pageSize: 25 },
      { page: 2, pageSize: 25 },
    ])
  })

  test('retries a rejected page with its captured page and ready page size', async () => {
    assert.deepEqual(await capturePageRetryRequests('transport'), [
      { page: 1, pageSize: 10 },
      { page: 2, pageSize: 25 },
      { page: 2, pageSize: 25 },
    ])
  })

  test('keeps a dispatched A row mutation owned by A after B becomes ready', async () => {
    const originalPost = api.post
    const requestA = deferred<MultiKeyAxiosResponse>()
    const requestB = deferred<MultiKeyAxiosResponse>()
    const mutationA = deferred<MultiKeyAxiosResponse>()
    const capturedPosts: CapturedPost[] = []
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(channelsQueryKeys.lists(), {
      source: 'mutation-ownership-sentinel',
    })

    let aProgressWasVisible = false
    let bProgressWasClearBeforeASettled = false
    let bConfirmationSurvivedASettlement = false
    let finalBodyText = ''
    let channelListWasInvalidated = false
    let staleToastWasRecorded = false

    api.post = (async (
      url: string,
      body?: unknown,
      config?: ApiRequestConfig
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      capturedPosts.push({ body: action, config })
      if (action.action === 'get_key_status') {
        return action.channel_id === channelA.id
          ? requestA.promise
          : requestB.promise
      }
      assert.equal(action.action, 'disable_key')
      return mutationA.promise
    }) as typeof api.post

    const cleanup = await renderHarness({
      queryClient,
      showToasts: true,
    })
    try {
      await clickTarget('A')
      await act(async () => {
        requestA.resolve(keyStatusResponse(3))
        await requestA.promise
        await Promise.resolve()
      })

      const disableA = findButton('Disable')
      assert.ok(disableA)
      await act(async () => {
        disableA.click()
        await Promise.resolve()
      })
      const continueA = findButton('Continue')
      assert.ok(continueA)
      await act(async () => {
        continueA.click()
        await Promise.resolve()
      })
      aProgressWasVisible = findButton('Continue')?.disabled === true

      await clickTarget('B')
      await act(async () => {
        requestB.resolve(keyStatusResponse(7))
        await requestB.promise
        await Promise.resolve()
      })

      const disableB = findButton('Disable')
      assert.ok(disableB)
      await act(async () => {
        disableB.click()
        await Promise.resolve()
      })
      bProgressWasClearBeforeASettled =
        findButton('Continue')?.disabled === false

      await act(async () => {
        mutationA.resolve({
          data: {
            success: true,
            message: 'stale A mutation completed',
          },
        })
        await mutationA.promise
        await Promise.resolve()
        await Promise.resolve()
      })

      bConfirmationSurvivedASettlement =
        findButton('Continue')?.disabled === false
      finalBodyText = document.body.textContent ?? ''
      channelListWasInvalidated =
        queryClient.getQueryState(channelsQueryKeys.lists())?.isInvalidated ===
        true
      staleToastWasRecorded = toast
        .getHistory()
        .some(
          (entry) =>
            'title' in entry && entry.title === 'stale A mutation completed'
        )
    } finally {
      requestA.resolve(keyStatusResponse(3))
      requestB.resolve(keyStatusResponse(7))
      mutationA.resolve({
        data: {
          success: true,
          message: 'stale A mutation completed',
        },
      })
      await act(async () => {
        await Promise.all([
          requestA.promise,
          requestB.promise,
          mutationA.promise,
        ])
        await Promise.resolve()
      })
      api.post = originalPost
      await cleanup()
    }

    const mutationPost = capturedPosts.find(
      (post) => post.body.action === 'disable_key'
    )
    assert.deepEqual(mutationPost?.body, {
      channel_id: channelA.id,
      action: 'disable_key',
      key_index: 3,
    })
    assert.equal(mutationPost?.config?.signal, undefined)
    assert.equal(aProgressWasVisible, true)
    assert.equal(bProgressWasClearBeforeASettled, true)
    assert.equal(bConfirmationSurvivedASettlement, true)
    assert.match(finalBodyText, /#8/)
    assert.doesNotMatch(finalBodyText, /#4/)
    assert.doesNotMatch(finalBodyText, /stale A mutation completed/)
    assert.equal(channelListWasInvalidated, false)
    assert.equal(staleToastWasRecorded, false)
    assert.equal(
      capturedPosts.filter((post) => post.body.action === 'get_key_status')
        .length,
      2
    )
  })

  test('invalidates and clears a performing session before a same-channel close callback', async () => {
    const originalPost = api.post
    const initialRequest = deferred<MultiKeyAxiosResponse>()
    const reopenedRequest = deferred<MultiKeyAxiosResponse>()
    const staleMutation = deferred<MultiKeyAxiosResponse>()
    const capturedPosts: CapturedPost[] = []
    let requestWasAbortedAtClose = false

    api.post = (async (
      url: string,
      body?: unknown,
      config?: ApiRequestConfig
    ): Promise<MultiKeyAxiosResponse> => {
      assert.equal(url, '/api/channel/multi_key/manage')
      const action = body as MultiKeyManageParams
      capturedPosts.push({ body: action, config })
      if (action.action === 'get_key_status') {
        const statusRequestCount = capturedPosts.filter(
          (post) => post.body.action === 'get_key_status'
        ).length
        return statusRequestCount === 1
          ? initialRequest.promise
          : reopenedRequest.promise
      }
      assert.equal(action.action, 'disable_key')
      return staleMutation.promise
    }) as typeof api.post

    const cleanup = await renderHarness({
      showToasts: true,
      onDialogClose: () => {
        const initialStatusRequest = capturedPosts.find(
          (post) => post.body.action === 'get_key_status'
        )
        requestWasAbortedAtClose =
          initialStatusRequest?.config?.signal?.aborted === true
      },
    })

    try {
      await clickTarget('A')
      await act(async () => {
        initialRequest.resolve(keyStatusResponse(3))
        await initialRequest.promise
        await Promise.resolve()
      })

      const disable = findButton('Disable')
      assert.ok(disable)
      await act(async () => {
        disable.click()
        await Promise.resolve()
      })
      const continueButton = findButton('Continue')
      assert.ok(continueButton)
      await act(async () => {
        continueButton.click()
        await Promise.resolve()
      })

      const close = findButton('Close')
      assert.ok(close)
      await act(async () => {
        close.click()
        await Promise.resolve()
      })

      assert.equal(requestWasAbortedAtClose, true)

      await clickTarget('A')
      assert.equal(
        capturedPosts.filter((post) => post.body.action === 'get_key_status')
          .length,
        2
      )
      assert.equal(findButton('Continue'), undefined)
      assert.equal(findButton('Disable'), undefined)
      assert.doesNotMatch(document.body.textContent || '', /#4/)

      await act(async () => {
        staleMutation.resolve({
          data: {
            success: true,
            message: 'closed mutation completed',
          },
        })
        await staleMutation.promise
        await Promise.resolve()
      })

      assert.equal(findButton('Continue'), undefined)
      assert.equal(findButton('Disable'), undefined)
      assert.doesNotMatch(
        document.body.textContent || '',
        /closed mutation completed/
      )

      await act(async () => {
        reopenedRequest.resolve(keyStatusResponse(7))
        await reopenedRequest.promise
        await Promise.resolve()
      })

      assert.match(document.body.textContent || '', /#8/)
      assert.doesNotMatch(document.body.textContent || '', /#4/)
      assert.ok(findButton('Disable'))
      assert.equal(findButton('Continue'), undefined)

      const mutationPost = capturedPosts.find(
        (post) => post.body.action === 'disable_key'
      )
      assert.equal(mutationPost?.config?.signal, undefined)
      assert.equal(
        toast
          .getHistory()
          .some(
            (entry) =>
              'title' in entry && entry.title === 'closed mutation completed'
          ),
        false
      )
    } finally {
      initialRequest.resolve(keyStatusResponse(3))
      reopenedRequest.resolve(keyStatusResponse(7))
      staleMutation.resolve({
        data: {
          success: true,
          message: 'closed mutation completed',
        },
      })
      await act(async () => {
        await Promise.all([
          initialRequest.promise,
          reopenedRequest.promise,
          staleMutation.promise,
        ])
        await Promise.resolve()
      })
      api.post = originalPost
      await cleanup()
    }
  })
})
