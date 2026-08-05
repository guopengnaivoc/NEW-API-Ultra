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

import type { ColumnDef } from '@tanstack/react-table'
import { Window } from 'happy-dom'

import type { NavigateFn } from '@/hooks/use-table-url-state'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
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

const originalSetTimeout = globalThis.setTimeout
const originalClearTimeout = globalThis.clearTimeout
let nextTimerId = 1
const debounceTimers = new Map<number, () => void>()
globalThis.setTimeout = ((
  callback: TimerHandler,
  delay?: number,
  ...args: unknown[]
) => {
  if (delay !== 500) {
    return originalSetTimeout(callback, delay, ...args)
  }
  assert.ok(typeof callback === 'function')
  const timerCallback = callback
  const timerId = 1_000_000 + nextTimerId++
  debounceTimers.set(timerId, () => timerCallback(...args))
  return timerId
}) as typeof setTimeout
globalThis.clearTimeout = ((timerId: ReturnType<typeof setTimeout>) => {
  if (!debounceTimers.delete(Number(timerId))) {
    originalClearTimeout(timerId)
  }
}) as typeof clearTimeout

const { act, useCallback, useMemo, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { getCoreRowModel, useReactTable } = await import('@tanstack/react-table')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { DataTableToolbar } = await import('../toolbar')
const { useTableUrlState } = await import('@/hooks/use-table-url-state')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Reset: 'Reset',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type SearchState = {
  filter?: string
}

const columns: ColumnDef<{ name: string }, unknown>[] = [
  {
    accessorKey: 'name',
    header: 'Name',
  },
]

function SearchHistoryHarness() {
  const [location, setLocation] = useState<{
    history: SearchState[]
    historyIndex: number
  }>({
    history: [{ filter: 'old' }],
    historyIndex: 0,
  })
  const search = location.history[location.historyIndex] ?? {}
  const navigate = useCallback<NavigateFn>((options) => {
    setLocation((previousLocation) => {
      const currentSearch =
        previousLocation.history[previousLocation.historyIndex] ?? {}
      let patch: Record<string, unknown> = {}
      if (typeof options.search === 'function') {
        patch = options.search(currentSearch)
      } else if (options.search !== true) {
        patch = options.search
      }
      const nextSearch = {
        ...currentSearch,
        ...patch,
      } as SearchState
      const nextHistory = [
        ...previousLocation.history.slice(0, previousLocation.historyIndex + 1),
        nextSearch,
      ]
      return {
        history: nextHistory,
        historyIndex: nextHistory.length - 1,
      }
    })
  }, [])
  const tableState = useTableUrlState({
    search,
    navigate,
    globalFilter: { key: 'filter' },
  })
  const stableColumns = useMemo(() => columns, [])
  const table = useReactTable({
    data: [],
    columns: stableColumns,
    state: {
      columnFilters: [],
      globalFilter: tableState.globalFilter,
    },
    onGlobalFilterChange: tableState.onGlobalFilterChange,
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <>
      <DataTableToolbar
        table={table}
        normalizeSearchValue={tableState.normalizeGlobalFilter}
        searchDebounceMs={500}
        searchPlaceholder='Search'
        hideViewOptions
      />
      <button
        type='button'
        onClick={() =>
          setLocation((current) => ({
            ...current,
            historyIndex: Math.max(0, current.historyIndex - 1),
          }))
        }
      >
        Back
      </button>
      <button
        type='button'
        onClick={() =>
          setLocation((current) => ({
            ...current,
            historyIndex: Math.min(
              current.history.length - 1,
              current.historyIndex + 1
            ),
          }))
        }
      >
        Forward
      </button>
      <output data-testid='history-index'>{location.historyIndex}</output>
      <output data-testid='history-size'>{location.history.length}</output>
      <output data-testid='url-filter'>{search.filter ?? ''}</output>
    </>
  )
}

function DeferredSearchHistoryHarness(
  props: {
    searchDebounceMs?: number
    searchPlaceholder?: string
    trimGlobal?: boolean
  } = {}
) {
  const [location, setLocation] = useState<{
    history: SearchState[]
    historyIndex: number
    pending: SearchState[]
  }>({
    history: [{ filter: 'old' }],
    historyIndex: 0,
    pending: [],
  })
  const search = location.history[location.historyIndex] ?? {}
  const navigate = useCallback<NavigateFn>((options) => {
    setLocation((previousLocation) => {
      const currentSearch =
        previousLocation.history[previousLocation.historyIndex] ?? {}
      let patch: Record<string, unknown> = {}
      if (typeof options.search === 'function') {
        patch = options.search(currentSearch)
      } else if (options.search !== true) {
        patch = options.search
      }
      return {
        ...previousLocation,
        pending: [
          ...previousLocation.pending,
          { ...currentSearch, ...patch } as SearchState,
        ],
      }
    })
  }, [])
  const tableState = useTableUrlState({
    search,
    navigate,
    globalFilter: { key: 'filter', trim: props.trimGlobal },
  })
  const stableColumns = useMemo(() => columns, [])
  const table = useReactTable({
    data: [],
    columns: stableColumns,
    state: {
      columnFilters: [],
      globalFilter: tableState.globalFilter,
    },
    onGlobalFilterChange: tableState.onGlobalFilterChange,
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <>
      <DataTableToolbar
        table={table}
        normalizeSearchValue={tableState.normalizeGlobalFilter}
        searchDebounceMs={props.searchDebounceMs ?? 500}
        searchPlaceholder={props.searchPlaceholder ?? 'Deferred Search'}
        hideViewOptions
      />
      <button
        type='button'
        onClick={() =>
          setLocation((current) => {
            const acknowledged = current.pending[0]
            if (!acknowledged) return current
            const nextHistory = [
              ...current.history.slice(0, current.historyIndex + 1),
              acknowledged,
            ]
            return {
              history: nextHistory,
              historyIndex: nextHistory.length - 1,
              pending: current.pending.slice(1),
            }
          })
        }
      >
        Acknowledge
      </button>
      <button
        type='button'
        onClick={() =>
          setLocation((current) => ({
            ...current,
            historyIndex: Math.max(0, current.historyIndex - 1),
          }))
        }
      >
        Deferred Back
      </button>
      <button
        type='button'
        onClick={() =>
          setLocation((current) => ({
            ...current,
            historyIndex: Math.min(
              current.history.length - 1,
              current.historyIndex + 1
            ),
          }))
        }
      >
        Deferred Forward
      </button>
      <button
        type='button'
        onClick={() =>
          setLocation((current) => {
            const nextHistory = [
              ...current.history.slice(0, current.historyIndex + 1),
              { filter: 'external' },
            ]
            return {
              ...current,
              history: nextHistory,
              historyIndex: nextHistory.length - 1,
            }
          })
        }
      >
        External URL
      </button>
      <output data-testid='deferred-pending'>{location.pending.length}</output>
      <output data-testid='deferred-next-filter'>
        {location.pending[0]?.filter ?? ''}
      </output>
      <output data-testid='deferred-url-filter'>{search.filter ?? ''}</output>
    </>
  )
}

function LocalSearchHarness() {
  const [globalFilter, setGlobalFilter] = useState('old')
  const stableColumns = useMemo(() => columns, [])
  const table = useReactTable({
    data: [],
    columns: stableColumns,
    state: {
      columnFilters: [],
      globalFilter,
    },
    onGlobalFilterChange: (updater) => {
      setGlobalFilter((current) =>
        typeof updater === 'function' ? updater(current) : updater
      )
    },
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <>
      <DataTableToolbar
        table={table}
        searchDebounceMs={0}
        searchPlaceholder='Local Search'
        hideViewOptions
      />
      <output data-testid='local-filter'>{globalFilter}</output>
    </>
  )
}

function changeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

function dispatchCompositionEvent(
  input: HTMLInputElement,
  type: 'compositionstart' | 'compositionend'
) {
  input.dispatchEvent(
    new domWindow.Event(type, { bubbles: true }) as unknown as Event
  )
}

function flushDebounceTimers() {
  const callbacks = [...debounceTimers.values()]
  debounceTimers.clear()
  for (const callback of callbacks) {
    callback()
  }
}

after(() => {
  debounceTimers.clear()
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  domWindow.close()
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('committed global search follows same-route Back and Forward without recommitting a stale draft', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <SearchHistoryHarness />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Search"]'
    )
    const backButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Back'
    )
    const forwardButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Forward'
    )
    assert.ok(searchInput)
    assert.ok(backButton)
    assert.ok(forwardButton)
    assert.equal(searchInput.value, 'old')

    await act(async () => {
      changeInputValue(searchInput, 'typed')
    })
    await act(async () => {
      flushDebounceTimers()
    })
    assert.equal(
      container.querySelector('[data-testid="url-filter"]')?.textContent,
      'typed'
    )
    assert.equal(
      container.querySelector('[data-testid="history-size"]')?.textContent,
      '2'
    )

    await act(async () => backButton.click())
    assert.equal(searchInput.value, 'old')
    assert.equal(
      container.querySelector('[data-testid="url-filter"]')?.textContent,
      'old'
    )
    assert.equal(
      container.querySelector('[data-testid="history-index"]')?.textContent,
      '0'
    )
    assert.equal(
      container.querySelector('[data-testid="history-size"]')?.textContent,
      '2'
    )

    await act(async () => forwardButton.click())
    assert.equal(searchInput.value, 'typed')
    assert.equal(
      container.querySelector('[data-testid="url-filter"]')?.textContent,
      'typed'
    )
    assert.equal(
      container.querySelector('[data-testid="history-index"]')?.textContent,
      '1'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('deferred URL acknowledgements preserve newer input and never reactivate completed drafts', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Deferred Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    const backButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Deferred Back')
    const forwardButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Deferred Forward')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)
    assert.ok(backButton)
    assert.ok(forwardButton)

    await act(async () => {
      changeInputValue(searchInput, 'typed')
    })
    await act(async () => {
      flushDebounceTimers()
    })

    assert.equal(searchInput.value, 'typed')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'old'
    )
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '1'
    )

    await act(async () => {
      changeInputValue(searchInput, 'newer draft')
    })
    await act(async () => acknowledgeButton.click())

    assert.equal(searchInput.value, 'newer draft')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'typed'
    )

    await act(async () => {
      flushDebounceTimers()
    })
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '1'
    )
    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'newer draft')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'newer draft'
    )

    await act(async () => backButton.click())
    assert.equal(searchInput.value, 'typed')
    await act(async () => backButton.click())
    assert.equal(searchInput.value, 'old')
    await act(async () => forwardButton.click())
    assert.equal(searchInput.value, 'typed')
    await act(async () => forwardButton.click())
    assert.equal(searchInput.value, 'newer draft')
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('matches a debounced acknowledgement to the canonical value queued for the URL', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness searchPlaceholder='Canonical Search' />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Canonical Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)

    await act(async () => {
      changeInputValue(searchInput, ' x ')
    })
    await act(async () => {
      flushDebounceTimers()
    })

    assert.equal(searchInput.value, ' x ')
    assert.equal(
      container.querySelector('[data-testid="deferred-next-filter"]')
        ?.textContent,
      'x'
    )

    await act(async () => {
      changeInputValue(searchInput, 'newer')
    })
    await act(async () => acknowledgeButton.click())

    assert.equal(searchInput.value, 'newer')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'x'
    )

    await act(async () => {
      flushDebounceTimers()
    })
    assert.equal(
      container.querySelector('[data-testid="deferred-next-filter"]')
        ?.textContent,
      'newer'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'newer')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'newer'
    )
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '0'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('immediate search keeps the latest draft across older URL acknowledgements', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness
            searchDebounceMs={0}
            searchPlaceholder='Immediate Search'
          />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Immediate Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)

    await act(async () => {
      changeInputValue(searchInput, 'typed')
    })
    assert.equal(searchInput.value, 'typed')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'old'
    )

    await act(async () => {
      changeInputValue(searchInput, 'newer draft')
    })
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '2'
    )
    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'newer draft')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'typed'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'newer draft')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'newer draft'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('matches immediate search acknowledgements to their canonical URL values', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness
            searchDebounceMs={0}
            searchPlaceholder='Canonical Immediate Search'
          />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Canonical Immediate Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)

    await act(async () => {
      changeInputValue(searchInput, ' x ')
    })
    await act(async () => {
      changeInputValue(searchInput, 'newer')
    })

    assert.equal(
      container.querySelector('[data-testid="deferred-next-filter"]')
        ?.textContent,
      'x'
    )
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '2'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'newer')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'x'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'newer')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'newer'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('adjacent canonical-equivalent searches reconcile on the first matching URL state', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness
            searchDebounceMs={0}
            searchPlaceholder='Equivalent Immediate Search'
          />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Equivalent Immediate Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)

    await act(async () => {
      changeInputValue(searchInput, 'x')
    })
    await act(async () => {
      changeInputValue(searchInput, ' x ')
    })

    assert.equal(searchInput.value, ' x ')

    await act(async () => acknowledgeButton.click())

    assert.equal(searchInput.value, 'x')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'x'
    )

    await act(async () => acknowledgeButton.click())

    assert.equal(searchInput.value, 'x')
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '0'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('trim-disabled URL search keeps identity acknowledgement semantics', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness
            searchDebounceMs={0}
            searchPlaceholder='Untrimmed Search'
            trimGlobal={false}
          />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Untrimmed Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)

    await act(async () => {
      changeInputValue(searchInput, ' x ')
    })

    assert.equal(
      container.querySelector('[data-testid="deferred-next-filter"]')
        ?.textContent,
      ' x '
    )
    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, ' x ')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      ' x '
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('non-URL search keeps identity semantics by default', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <LocalSearchHarness />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Local Search"]'
    )
    assert.ok(searchInput)

    await act(async () => {
      changeInputValue(searchInput, ' x ')
    })

    assert.equal(searchInput.value, ' x ')
    assert.equal(
      container.querySelector('[data-testid="local-filter"]')?.textContent,
      ' x '
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('repeated search values stay tied to their own acknowledgement generation', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness
            searchDebounceMs={0}
            searchPlaceholder='Repeated Search'
          />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Repeated Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)

    await act(async () => {
      changeInputValue(searchInput, 'x')
    })
    await act(async () => {
      changeInputValue(searchInput, 'y')
    })
    await act(async () => {
      changeInputValue(searchInput, 'x')
    })

    assert.equal(searchInput.value, 'x')
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '3'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'x')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'x'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'x')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'y'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'x')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'x'
    )
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '0'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'x')
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('Reset clears immediately through delayed acknowledgements and preserves a newer draft', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness
            searchDebounceMs={0}
            searchPlaceholder='Reset Search'
          />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Reset Search"]'
    )
    const acknowledgeButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Acknowledge')
    const resetButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent?.trim() === 'Reset')
    assert.ok(searchInput)
    assert.ok(acknowledgeButton)
    assert.ok(resetButton)

    await act(async () => {
      changeInputValue(searchInput, 'before-reset')
    })
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '1'
    )

    await act(async () => resetButton.click())
    assert.equal(searchInput.value, '')
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '2'
    )

    await act(async () => {
      changeInputValue(searchInput, 'after-reset')
    })
    assert.equal(searchInput.value, 'after-reset')
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '3'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'after-reset')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'before-reset'
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'after-reset')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      ''
    )

    await act(async () => acknowledgeButton.click())
    assert.equal(searchInput.value, 'after-reset')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'after-reset'
    )
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '0'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('IME draft remains visible while the URL changes during composition', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DeferredSearchHistoryHarness />
        </I18nextProvider>
      )
    })

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[placeholder="Deferred Search"]'
    )
    const externalUrlButton = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'External URL')
    assert.ok(searchInput)
    assert.ok(externalUrlButton)

    await act(async () => {
      dispatchCompositionEvent(searchInput, 'compositionstart')
    })
    await act(async () => {
      changeInputValue(searchInput, '拼')
    })
    await act(async () => externalUrlButton.click())

    assert.equal(searchInput.value, '拼')
    assert.equal(
      container.querySelector('[data-testid="deferred-url-filter"]')
        ?.textContent,
      'external'
    )

    await act(async () => {
      dispatchCompositionEvent(searchInput, 'compositionend')
    })
    await act(async () => {
      flushDebounceTimers()
    })
    assert.equal(
      container.querySelector('[data-testid="deferred-pending"]')?.textContent,
      '1'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
