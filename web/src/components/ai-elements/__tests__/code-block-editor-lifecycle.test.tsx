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

import { EditorView } from '@codemirror/view'
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
  'KeyboardEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'DOMRect',
  'Range',
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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { CodeBlockEditor } = await import('../code-block')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function EditorHarness(props: { onEscape: (value: string) => void }) {
  const [value, setValue] = useState('initial')
  const [revision, setRevision] = useState(0)

  return (
    <>
      <button
        type='button'
        onClick={() => setRevision((current) => current + 1)}
      >
        Rerender {revision}
      </button>
      <CodeBlockEditor
        ariaLabel='Editor'
        language='markdown'
        onChange={setValue}
        onKeyDown={(event) => {
          if (event.key !== 'Escape') return
          props.onEscape(value)
          event.preventDefault()
        }}
        value={value}
      />
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

test('handler-only rerenders preserve the current draft and selection while using the latest handler', async () => {
  const escapedValues: string[] = []
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <EditorHarness
          onEscape={(value) => {
            escapedValues.push(value)
          }}
        />
      )
      await Promise.resolve()
    })

    const editorElement = document.querySelector<HTMLElement>('.cm-editor')
    assert.ok(editorElement)
    const initialView = EditorView.findFromDOM(editorElement)
    assert.ok(initialView)

    await act(async () => {
      initialView.dispatch({
        changes: {
          from: 0,
          to: initialView.state.doc.length,
          insert: 'edited draft',
        },
        selection: { anchor: 6 },
      })
      await Promise.resolve()
    })

    const rerender = document.querySelector<HTMLButtonElement>('button')
    assert.ok(rerender)
    await act(async () => {
      rerender.click()
      await Promise.resolve()
    })

    const currentEditorElement =
      document.querySelector<HTMLElement>('.cm-editor')
    assert.ok(currentEditorElement)
    const currentView = EditorView.findFromDOM(currentEditorElement)
    assert.ok(currentView)
    assert.equal(currentView.state.doc.toString(), 'edited draft')
    assert.equal(currentView.state.selection.main.head, 6)

    const keyboardEvent = new KeyboardEvent('keydown', {
      bubbles: true,
      cancelable: true,
      key: 'Escape',
    })
    currentView.contentDOM.dispatchEvent(keyboardEvent)

    assert.equal(keyboardEvent.defaultPrevented, true)
    assert.deepEqual(escapedValues, ['edited draft'])
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
