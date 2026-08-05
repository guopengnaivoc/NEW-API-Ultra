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
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
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
const globalDescriptors = new Map<
  (typeof domGlobalKeys)[number],
  PropertyDescriptor | undefined
>()
for (const key of domGlobalKeys) {
  globalDescriptors.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ThemeProvider } = await import('@/context/theme-provider')
const { createChartThemeManagerLoader, useChartThemeWithLoader } =
  await import('../use-chart-theme')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

after(() => {
  domWindow.close()
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('a rejected shared chart theme load is evicted and the next attempt succeeds', async () => {
  const manager = {
    setCurrentTheme() {},
  }
  let attempts = 0
  const loader = createChartThemeManagerLoader(async () => {
    attempts += 1
    if (attempts === 1) {
      throw new Error('transient chunk failure')
    }
    return manager
  })

  const first = loader.load()
  const concurrent = loader.load()
  assert.strictEqual(concurrent, first)

  const firstResults = await Promise.allSettled([first, concurrent])
  assert.deepEqual(
    firstResults.map((result) => result.status),
    ['rejected', 'rejected']
  )
  assert.equal(attempts, 1)

  assert.strictEqual(await loader.load(), manager)
  assert.equal(attempts, 2)
  assert.strictEqual(await loader.load(), manager)
  assert.equal(attempts, 2)
})

test('the mounted hook exposes a retry and applies the resolved theme after recovery', async () => {
  const appliedThemes: string[] = []
  let attempts = 0
  const loader = createChartThemeManagerLoader(async () => {
    attempts += 1
    if (attempts === 1) {
      throw new Error('transient chunk failure')
    }
    return {
      setCurrentTheme(theme: string) {
        appliedThemes.push(theme)
      },
    }
  })

  function Harness() {
    const { resolvedTheme, retryTheme, themeError, themeReady } =
      useChartThemeWithLoader(loader)

    return (
      <div
        data-error={themeError ? 'true' : 'false'}
        data-ready={themeReady ? 'true' : 'false'}
        data-theme={resolvedTheme}
      >
        {themeError ? (
          <button type='button' onClick={retryTheme}>
            Retry
          </button>
        ) : null}
      </div>
    )
  }

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <ThemeProvider defaultTheme='dark' storageKey='chart-theme-loader-test'>
          <Harness />
        </ThemeProvider>
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    const state = container.querySelector<HTMLElement>('[data-ready]')
    const retry = container.querySelector<HTMLButtonElement>('button')
    assert.ok(state)
    assert.equal(state.dataset.ready, 'false')
    assert.equal(state.dataset.error, 'true')
    assert.equal(state.dataset.theme, 'dark')
    assert.ok(retry)

    await act(async () => {
      retry.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(attempts, 2)
    assert.deepEqual(appliedThemes, ['dark'])
    assert.equal(state.dataset.ready, 'true')
    assert.equal(state.dataset.error, 'false')
    assert.equal(container.querySelector('button'), null)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('a late rejection after unmount is ignored without becoming unhandled', async () => {
  let rejectLoad: ((reason?: unknown) => void) | undefined
  const loader = createChartThemeManagerLoader(
    () =>
      new Promise((_, reject) => {
        rejectLoad = reject
      })
  )
  let inspectedLateFailure = false
  const lateFailure = new Proxy(
    {},
    {
      getPrototypeOf() {
        inspectedLateFailure = true
        return Object.prototype
      },
    }
  )
  const unhandledRejections: unknown[] = []
  const onUnhandledRejection = (reason: unknown) => {
    unhandledRejections.push(reason)
  }

  function Harness() {
    useChartThemeWithLoader(loader)
    return null
  }

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <ThemeProvider
          defaultTheme='dark'
          storageKey='chart-theme-unmount-test'
        >
          <Harness />
        </ThemeProvider>
      )
      await Promise.resolve()
    })
    assert.ok(rejectLoad)

    await act(async () => root.unmount())
    process.on('unhandledRejection', onUnhandledRejection)
    rejectLoad(lateFailure)
    await new Promise<void>((resolve) => setImmediate(resolve))

    // `instanceof Error` inspects this proxy. Remaining false proves the
    // cancelled catch returned before normalizing and publishing error state.
    assert.equal(inspectedLateFailure, false)
    assert.deepEqual(unhandledRejections, [])
  } finally {
    process.off('unhandledRejection', onUnhandledRejection)
    container.remove()
  }
})

test('a theme application failure retries with the cached manager', async () => {
  const appliedThemes: string[] = []
  let loadAttempts = 0
  let applicationAttempts = 0
  const loader = createChartThemeManagerLoader(async () => {
    loadAttempts += 1
    return {
      setCurrentTheme(theme: string) {
        applicationAttempts += 1
        if (applicationAttempts === 1) {
          throw new Error('transient theme application failure')
        }
        appliedThemes.push(theme)
      },
    }
  })

  function Harness() {
    const { retryTheme, themeError, themeReady } =
      useChartThemeWithLoader(loader)

    return (
      <div
        data-error={themeError ? 'true' : 'false'}
        data-ready={themeReady ? 'true' : 'false'}
      >
        {themeError ? (
          <button type='button' onClick={retryTheme}>
            Retry
          </button>
        ) : null}
      </div>
    )
  }

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <ThemeProvider
          defaultTheme='dark'
          storageKey='chart-theme-application-test'
        >
          <Harness />
        </ThemeProvider>
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    const state = container.querySelector<HTMLElement>('[data-ready]')
    const retry = container.querySelector<HTMLButtonElement>('button')
    assert.ok(state)
    assert.equal(state.dataset.ready, 'false')
    assert.equal(state.dataset.error, 'true')
    assert.ok(retry)

    await act(async () => {
      retry.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(loadAttempts, 1)
    assert.equal(applicationAttempts, 2)
    assert.deepEqual(appliedThemes, ['dark'])
    assert.equal(state.dataset.ready, 'true')
    assert.equal(state.dataset.error, 'false')
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
