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

import type { NavigateFn } from '../use-table-url-state'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { useDebouncedColumnFilter } =
  await import('@/components/data-table/hooks/use-debounced-column-filter')
const { useTableUrlState } = await import('../use-table-url-state')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type SearchState = {
  filter?: string
  model?: string
}

function FilterHarness(props: {
  onNavigation: (search: SearchState) => void
  delay?: number
}) {
  const [search, setSearch] = useState<SearchState>({
    filter: 'old-global',
    model: 'old-model',
  })
  const navigate: NavigateFn = (options) => {
    setSearch((previous) => {
      if (options.search === true) return previous
      const patch =
        typeof options.search === 'function'
          ? options.search(previous)
          : options.search
      const next = { ...previous, ...patch } as SearchState
      props.onNavigation(next)
      return next
    })
  }
  const tableState = useTableUrlState({
    search,
    navigate,
    globalFilter: { key: 'filter' },
    columnFilters: [
      {
        columnId: 'model',
        searchKey: 'model',
        type: 'string',
      },
    ],
  })
  const modelFilter = useDebouncedColumnFilter({
    columnFilters: tableState.columnFilters,
    columnId: 'model',
    onColumnFiltersChange: tableState.onColumnFiltersChange,
    delay: props.delay ?? 60_000,
  })

  return (
    <>
      <button
        type='button'
        onClick={() =>
          setSearch({
            filter: 'new-global',
            model: 'new-model',
          })
        }
      >
        Apply external URL
      </button>
      <button
        type='button'
        onClick={() => modelFilter.setInputValue('typed-model')}
      >
        Edit model
      </button>
      <output data-testid='global'>{tableState.globalFilter}</output>
      <output data-testid='model'>{modelFilter.inputValue}</output>
      <output data-testid='search'>
        {search.filter}/{search.model}
      </output>
    </>
  )
}

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

test('external URL synchronization never writes stale filters and preserves the next user edit', async () => {
  const navigations: SearchState[] = []
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <FilterHarness
          delay={0}
          onNavigation={(search) => {
            navigations.push(search)
          }}
        />
      )
      await Promise.resolve()
    })

    const buttons = document.querySelectorAll('button')
    assert.equal(buttons.length, 2)
    await act(async () => {
      buttons[0]?.click()
      await Promise.resolve()
    })

    assert.equal(
      document.querySelector('[data-testid="global"]')?.textContent,
      'new-global'
    )
    assert.equal(
      document.querySelector('[data-testid="model"]')?.textContent,
      'new-model'
    )
    assert.equal(
      document.querySelector('[data-testid="search"]')?.textContent,
      'new-global/new-model'
    )
    assert.deepEqual(navigations, [])

    await act(async () => buttons[1]?.click())
    await act(
      () =>
        new Promise<void>((resolve) => {
          setTimeout(resolve, 0)
        })
    )

    assert.deepEqual(navigations, [
      {
        filter: 'new-global',
        model: 'typed-model',
        page: undefined,
      },
    ])
    assert.equal(
      document.querySelector('[data-testid="search"]')?.textContent,
      'new-global/typed-model'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
