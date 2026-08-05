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
import { after, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLScriptElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of domGlobalKeys) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, startTransition, Suspense, useLayoutEffect, useState } =
  await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { Turnstile } = await import('../../turnstile')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Failed to load': 'Failed to load',
        Retry: 'Retry',
      },
    },
  },
})

const reactGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactGlobals.IS_REACT_ACT_ENVIRONMENT = true
const originalScriptDispatchEvent = HTMLScriptElement.prototype.dispatchEvent
let suppressBrowserScriptError = false
HTMLScriptElement.prototype.dispatchEvent = function dispatchEvent(
  event: Event
) {
  if (suppressBrowserScriptError && event.type === 'error') return true
  return originalScriptDispatchEvent.call(this, event)
}

type RenderOptions = {
  sitekey: string
  callback: (token: string) => void
  'error-callback': () => void
  'expired-callback': () => void
}

type RenderCall = {
  element: HTMLElement
  options: RenderOptions
  widgetId: string
}

function createTurnstileApi(configuration?: {
  renderError?: Error
  widgetId?: string
}) {
  const renderCalls: RenderCall[] = []
  const removedWidgetIds: string[] = []
  return {
    renderCalls,
    removedWidgetIds,
    api: {
      render(element: HTMLElement, renderOptions: Record<string, unknown>) {
        const widgetId =
          configuration?.widgetId ?? `widget-${renderCalls.length + 1}`
        renderCalls.push({
          element,
          options: renderOptions as RenderOptions,
          widgetId,
        })
        if (configuration?.renderError) {
          throw configuration.renderError
        }
        return widgetId
      },
      remove(widgetId: string) {
        removedWidgetIds.push(widgetId)
      },
    },
  }
}

function withI18n(node: ReactNode) {
  return <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
}

async function flushEffects() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function LayoutCommitProbe(props: { onCommit?: () => void }) {
  const onCommit = props.onCommit
  useLayoutEffect(() => {
    onCommit?.()
  }, [onCommit])

  return null
}

beforeEach(() => {
  suppressBrowserScriptError = false
  delete window.turnstile
  document.querySelector('#cf-turnstile')?.remove()
  document.body.replaceChildren()
})

after(() => {
  HTMLScriptElement.prototype.dispatchEvent = originalScriptDispatchEvent
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('a surviving concurrent mount renders after the shared script loads', async () => {
  function ConcurrentHarness({ showFirst }: { showFirst: boolean }) {
    return (
      <>
        {showFirst && (
          <Turnstile
            siteKey='first-site-key'
            onVerify={() => undefined}
            className='first-widget'
          />
        )}
        <Turnstile
          siteKey='second-site-key'
          onVerify={() => undefined}
          className='second-widget'
        />
      </>
    )
  }

  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  assert.equal(window.turnstile, undefined)

  suppressBrowserScriptError = true
  await act(async () => {
    root.render(withI18n(<ConcurrentHarness showFirst />))
  })
  suppressBrowserScriptError = false

  const scripts = document.querySelectorAll('#cf-turnstile')
  assert.equal(scripts.length, 1, document.documentElement.outerHTML)
  const script = scripts[0]
  assert.ok(script instanceof HTMLScriptElement)

  await act(async () => {
    root.render(withI18n(<ConcurrentHarness showFirst={false} />))
  })

  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  window.turnstile = api
  script.dispatchEvent(new Event('load'))
  await flushEffects()

  assert.equal(renderCalls.length, 1)
  assert.ok(renderCalls[0]?.element.classList.contains('second-widget'))
  assert.equal(renderCalls[0]?.options.sitekey, 'second-site-key')

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1'])
})

test('removes a widget whose synchronous callback disposes its owner', async () => {
  const removedWidgetIds: string[] = []
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  window.turnstile = {
    render(_element, options) {
      const callback = options.callback
      assert.equal(typeof callback, 'function')
      const verify = callback as (token: string) => void
      verify('verified-token')
      return 'widget-reentrant'
    },
    remove(widgetId) {
      removedWidgetIds.push(widgetId)
    },
  }

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='reentrant-site-key'
          onVerify={() => root.unmount()}
        />
      )
    )
    await Promise.resolve()
  })

  assert.deepEqual(removedWidgetIds, ['widget-reentrant'])
})

test('uses the latest callbacks without recreating the widget', async () => {
  const verifyCalls: string[] = []
  const expireCalls: string[] = []
  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  window.turnstile = api

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='stable-site-key'
          onVerify={(token) => verifyCalls.push(`A:${token}`)}
          onExpire={() => expireCalls.push('A')}
        />
      )
    )
    await Promise.resolve()
  })

  const firstRender = renderCalls[0]
  assert.ok(firstRender)

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='stable-site-key'
          onVerify={(token) => verifyCalls.push(`B:${token}`)}
          onExpire={() => expireCalls.push('B')}
        />
      )
    )
    await Promise.resolve()
  })

  assert.equal(renderCalls.length, 1)
  assert.deepEqual(removedWidgetIds, [])
  firstRender.options.callback('latest-token')
  firstRender.options['error-callback']()
  firstRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['B:latest-token'])
  assert.deepEqual(expireCalls, ['B', 'B'])

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1'])
  firstRender.options.callback('post-cleanup-token')
  firstRender.options['error-callback']()
  firstRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['B:latest-token'])
  assert.deepEqual(expireCalls, ['B', 'B'])
})

test('keeps committed callbacks while a newer render is suspended', async () => {
  const verifyCalls: string[] = []
  const expireCalls: string[] = []
  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  const neverResolves = new Promise<void>(() => undefined)
  let suspendedRenderCount = 0
  window.turnstile = api

  function SuspendedSibling(props: { suspend: boolean }) {
    if (props.suspend) {
      suspendedRenderCount += 1
      throw neverResolves
    }
    return null
  }

  function Harness(props: { callbackOwner: string; suspend: boolean }) {
    return (
      <Suspense fallback={null}>
        <Turnstile
          siteKey='stable-site-key'
          onVerify={(token) =>
            verifyCalls.push(`${props.callbackOwner}:${token}`)
          }
          onExpire={() => expireCalls.push(props.callbackOwner)}
        />
        <SuspendedSibling suspend={props.suspend} />
      </Suspense>
    )
  }

  await act(async () => {
    root.render(<Harness callbackOwner='A' suspend={false} />)
    await Promise.resolve()
  })

  const committedWidget = renderCalls[0]
  assert.ok(committedWidget)

  await act(async () => {
    startTransition(() => {
      root.render(<Harness callbackOwner='B' suspend />)
    })
    await Promise.resolve()
  })

  assert.equal(suspendedRenderCount, 1)
  assert.equal(renderCalls.length, 1)
  assert.deepEqual(removedWidgetIds, [])

  committedWidget.options.callback('during-suspended-render')
  committedWidget.options['error-callback']()
  committedWidget.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['A:during-suspended-render'])
  assert.deepEqual(expireCalls, ['A', 'A'])

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1'])
})

test('replaces a changed site key and ignores callbacks from the removed widget', async () => {
  const verifyCalls: string[] = []
  const expireCalls: string[] = []
  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  window.turnstile = api

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='site-a'
          onVerify={(token) => verifyCalls.push(`A:${token}`)}
          onExpire={() => expireCalls.push('A')}
        />
      )
    )
    await Promise.resolve()
  })

  const firstRender = renderCalls[0]
  assert.ok(firstRender)

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='site-b'
          onVerify={(token) => verifyCalls.push(`B:${token}`)}
          onExpire={() => expireCalls.push('B')}
        />
      )
    )
    await Promise.resolve()
  })

  const secondRender = renderCalls[1]
  assert.ok(secondRender)
  assert.equal(renderCalls.length, 2)
  assert.equal(firstRender.options.sitekey, 'site-a')
  assert.equal(secondRender.options.sitekey, 'site-b')
  assert.deepEqual(removedWidgetIds, ['widget-1'])

  firstRender.options.callback('obsolete-token')
  firstRender.options['error-callback']()
  firstRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, [])
  assert.deepEqual(expireCalls, [])

  secondRender.options.callback('latest-token')
  secondRender.options['error-callback']()
  secondRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['B:latest-token'])
  assert.deepEqual(expireCalls, ['B', 'B'])

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1', 'widget-2'])
  secondRender.options.callback('post-cleanup-token')
  secondRender.options['error-callback']()
  secondRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['B:latest-token'])
  assert.deepEqual(expireCalls, ['B', 'B'])
})

test('invalidates each site-key generation before sibling layout effects run', async () => {
  const verifyCalls: string[] = []
  const expireCalls: string[] = []
  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  window.turnstile = api

  const renderHarness = (
    siteKey: string,
    callbackOwner: string,
    onCommit?: () => void
  ) =>
    withI18n(
      <>
        <Turnstile
          siteKey={siteKey}
          onVerify={(token) => verifyCalls.push(`${callbackOwner}:${token}`)}
          onExpire={() => expireCalls.push(callbackOwner)}
        />
        <LayoutCommitProbe onCommit={onCommit} />
      </>
    )

  await act(async () => {
    root.render(renderHarness('site-a', 'A1'))
    await Promise.resolve()
  })

  const firstRender = renderCalls[0]
  assert.ok(firstRender)

  await act(async () => {
    root.render(
      renderHarness('site-b', 'B', () => {
        firstRender.options.callback('a-during-b-commit')
        firstRender.options['error-callback']()
        firstRender.options['expired-callback']()
      })
    )
    await Promise.resolve()
  })

  assert.deepEqual(verifyCalls, [])
  assert.deepEqual(expireCalls, [])
  const secondRender = renderCalls[1]
  assert.ok(secondRender)
  assert.deepEqual(removedWidgetIds, ['widget-1'])

  await act(async () => {
    root.render(
      renderHarness('site-a', 'A2', () => {
        secondRender.options.callback('b-during-a-commit')
        secondRender.options['error-callback']()
        secondRender.options['expired-callback']()
      })
    )
    await Promise.resolve()
  })

  assert.deepEqual(verifyCalls, [])
  assert.deepEqual(expireCalls, [])
  const thirdRender = renderCalls[2]
  assert.ok(thirdRender)
  assert.deepEqual(
    renderCalls.map((renderCall) => [
      renderCall.options.sitekey,
      renderCall.widgetId,
    ]),
    [
      ['site-a', 'widget-1'],
      ['site-b', 'widget-2'],
      ['site-a', 'widget-3'],
    ]
  )
  assert.deepEqual(removedWidgetIds, ['widget-1', 'widget-2'])

  firstRender.options.callback('old-a-after-a-returned')
  firstRender.options['error-callback']()
  firstRender.options['expired-callback']()
  secondRender.options.callback('old-b-after-a-returned')
  secondRender.options['error-callback']()
  secondRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, [])
  assert.deepEqual(expireCalls, [])

  thirdRender.options.callback('current-token')
  thirdRender.options['error-callback']()
  thirdRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['A2:current-token'])
  assert.deepEqual(expireCalls, ['A2', 'A2'])

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1', 'widget-2', 'widget-3'])

  thirdRender.options.callback('post-cleanup-token')
  thirdRender.options['error-callback']()
  thirdRender.options['expired-callback']()
  assert.deepEqual(verifyCalls, ['A2:current-token'])
  assert.deepEqual(expireCalls, ['A2', 'A2'])
})

test('shows a loader failure and retries with a fresh script', async () => {
  const expireCalls: string[] = []
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)

  suppressBrowserScriptError = true
  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='retry-site-key'
          onVerify={() => undefined}
          onExpire={() => expireCalls.push('expired')}
        />
      )
    )
  })
  suppressBrowserScriptError = false

  const firstScript = document.querySelector('#cf-turnstile')
  assert.ok(firstScript instanceof HTMLScriptElement)

  await act(async () => {
    firstScript.dispatchEvent(new Event('error'))
    await Promise.resolve()
  })
  await flushEffects()

  assert.equal(firstScript.isConnected, false)
  assert.match(container.textContent ?? '', /Failed to load/)
  assert.deepEqual(expireCalls, [])
  const retryButton = [...container.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === 'Retry'
  )
  assert.ok(retryButton)

  suppressBrowserScriptError = true
  await act(async () => {
    retryButton.click()
    await Promise.resolve()
  })
  suppressBrowserScriptError = false

  const secondScript = document.querySelector('#cf-turnstile')
  assert.ok(secondScript instanceof HTMLScriptElement)
  assert.notEqual(secondScript, firstScript)
  assert.equal(secondScript.isConnected, true)

  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  window.turnstile = api
  await act(async () => {
    secondScript.dispatchEvent(new Event('load'))
    await Promise.resolve()
  })
  await flushEffects()

  assert.equal(renderCalls.length, 1)
  assert.doesNotMatch(container.textContent ?? '', /Failed to load/)

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1'])
})

test('keeps initialization retry manual for a caller that remounts on widget expiry', async () => {
  let expireCalls = 0

  function KeyedExpiryHarness() {
    const [widgetKey, setWidgetKey] = useState(0)

    return (
      <Turnstile
        key={widgetKey}
        siteKey='keyed-retry-site-key'
        onVerify={() => undefined}
        onExpire={() => {
          expireCalls += 1
          setWidgetKey((current) => current + 1)
        }}
      />
    )
  }

  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)

  suppressBrowserScriptError = true
  await act(async () => {
    root.render(withI18n(<KeyedExpiryHarness />))
  })

  const firstScript = document.querySelector('#cf-turnstile')
  assert.ok(firstScript instanceof HTMLScriptElement)

  await act(async () => {
    suppressBrowserScriptError = false
    firstScript.dispatchEvent(new Event('error'))
    suppressBrowserScriptError = true
    await Promise.resolve()
  })
  await flushEffects()

  assert.equal(expireCalls, 0)
  assert.equal(firstScript.isConnected, false)
  assert.equal(document.querySelector('#cf-turnstile'), null)
  assert.match(container.textContent ?? '', /Failed to load/)
  const retryButtons = [...container.querySelectorAll('button')].filter(
    (button) => button.textContent?.trim() === 'Retry'
  )
  assert.equal(retryButtons.length, 1)

  await act(async () => {
    retryButtons[0]?.click()
    await Promise.resolve()
  })

  const secondScript = document.querySelector('#cf-turnstile')
  assert.ok(secondScript instanceof HTMLScriptElement)
  assert.notEqual(secondScript, firstScript)
  assert.equal(secondScript.isConnected, true)
  assert.equal(expireCalls, 0)

  const { api, renderCalls, removedWidgetIds } = createTurnstileApi()
  window.turnstile = api
  await act(async () => {
    suppressBrowserScriptError = false
    secondScript.dispatchEvent(new Event('load'))
    await Promise.resolve()
  })
  await flushEffects()

  assert.equal(renderCalls.length, 1)
  assert.doesNotMatch(container.textContent ?? '', /Failed to load/)

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, ['widget-1'])
})

test('shows retry UI when the loaded API throws during render', async () => {
  const expireCalls: string[] = []
  const loadedScript = document.createElement('script')
  loadedScript.id = 'cf-turnstile'
  document.head.appendChild(loadedScript)
  const { api, renderCalls, removedWidgetIds } = createTurnstileApi({
    renderError: new Error('render failed'),
  })
  window.turnstile = api
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='render-failure-site-key'
          onVerify={() => undefined}
          onExpire={() => expireCalls.push('expired')}
        />
      )
    )
    await Promise.resolve()
  })

  assert.equal(renderCalls.length, 1)
  assert.equal(loadedScript.isConnected, true)
  assert.match(container.textContent ?? '', /Failed to load/)
  assert.match(container.textContent ?? '', /Retry/)
  assert.deepEqual(expireCalls, [])

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, [])
})

for (const scenario of [
  {
    name: 'throws',
    configuration: { renderError: new Error('render failed') },
  },
  {
    name: 'returns an empty widget ID',
    configuration: { widgetId: '' },
  },
] as const) {
  test(`ignores retained callbacks after render ${scenario.name}`, async () => {
    const verifyCalls: string[] = []
    const expireCalls: string[] = []
    const { api, renderCalls, removedWidgetIds } = createTurnstileApi(
      scenario.configuration
    )
    window.turnstile = api
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        withI18n(
          <Turnstile
            siteKey='failed-attempt-site-key'
            onVerify={(token) => verifyCalls.push(token)}
            onExpire={() => expireCalls.push('expired')}
          />
        )
      )
      await Promise.resolve()
    })

    const failedRender = renderCalls[0]
    assert.ok(failedRender)
    for (const token of ['late-token-1', 'late-token-2']) {
      failedRender.options.callback(token)
      failedRender.options['error-callback']()
      failedRender.options['expired-callback']()
    }

    assert.deepEqual(
      {
        verifyCalls,
        expireCalls,
        alertCount: container.querySelectorAll('[role="alert"]').length,
        retryButtonCount: [...container.querySelectorAll('button')].filter(
          (button) => button.textContent?.trim() === 'Retry'
        ).length,
      },
      {
        verifyCalls: [],
        expireCalls: [],
        alertCount: 1,
        retryButtonCount: 1,
      }
    )

    await act(async () => {
      root.unmount()
    })
    assert.deepEqual(removedWidgetIds, [])
  })
}

test('shows retry UI when the loaded API returns an empty widget ID', async () => {
  const expireCalls: string[] = []
  const loadedScript = document.createElement('script')
  loadedScript.id = 'cf-turnstile'
  document.head.appendChild(loadedScript)
  const { api, renderCalls, removedWidgetIds } = createTurnstileApi({
    widgetId: '',
  })
  window.turnstile = api
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      withI18n(
        <Turnstile
          siteKey='empty-widget-site-key'
          onVerify={() => undefined}
          onExpire={() => expireCalls.push('expired')}
        />
      )
    )
    await Promise.resolve()
  })

  assert.equal(renderCalls.length, 1)
  assert.equal(loadedScript.isConnected, true)
  assert.match(container.textContent ?? '', /Failed to load/)
  assert.match(container.textContent ?? '', /Retry/)
  assert.deepEqual(expireCalls, [])

  await act(async () => {
    root.unmount()
  })
  assert.deepEqual(removedWidgetIds, [])
})
