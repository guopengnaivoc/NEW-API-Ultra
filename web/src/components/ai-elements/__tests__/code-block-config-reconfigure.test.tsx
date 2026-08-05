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

import { EditorView } from '@codemirror/view'
import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

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
const { CodeMirrorCodeView } = await import('../code-block')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type ConfigControls = {
  setLanguage: (value: string) => void
  setReadOnly: (value: boolean) => void
  setShowLineNumbers: (value: boolean) => void
}

const controls: ConfigControls = {
  setLanguage: () => {},
  setReadOnly: () => {},
  setShowLineNumbers: () => {},
}

function ConfigHarness() {
  const [value, setValue] = useState('initial')
  const [language, setLanguage] = useState('markdown')
  const [readOnly, setReadOnly] = useState(false)
  const [showLineNumbers, setShowLineNumbers] = useState(true)

  controls.setLanguage = setLanguage
  controls.setReadOnly = setReadOnly
  controls.setShowLineNumbers = setShowLineNumbers

  return (
    <CodeMirrorCodeView
      ariaLabel='Editor'
      language={language}
      onChange={setValue}
      readOnly={readOnly}
      showLineNumbers={showLineNumbers}
      value={value}
    />
  )
}

type Mounted = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function mount(node: ReactNode): Promise<Mounted> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(node)
    await Promise.resolve()
  })
  return { container, root }
}

async function unmount(mounted: Mounted) {
  await act(async () => mounted.root.unmount())
  mounted.container.remove()
}

function currentView(): EditorView {
  const element = document.querySelector<HTMLElement>('.cm-editor')
  assert.ok(element)
  const view = EditorView.findFromDOM(element)
  assert.ok(view)
  return view
}

// Type a draft and place the caret inside it, so we can later prove both the
// document text and the selection survive a configuration change.
async function seedDraft(text: string, caret: number) {
  const view = currentView()
  await act(async () => {
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: text },
      selection: { anchor: caret },
    })
    await Promise.resolve()
  })
  return view
}

describe('CodeMirror config changes preserve draft and selection', () => {
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

  test('language change keeps the same EditorView, draft, and caret', async () => {
    const mounted = await mount(<ConfigHarness />)
    try {
      const viewBefore = await seedDraft('edited draft', 6)

      await act(async () => {
        controls.setLanguage('javascript')
        await Promise.resolve()
      })

      const viewAfter = currentView()
      // A destroy/recreate would produce a different EditorView instance and
      // reset the doc to the initial "initial" value with a collapsed caret.
      assert.equal(viewAfter === viewBefore, true)
      assert.equal(viewAfter.state.doc.toString(), 'edited draft')
      assert.equal(viewAfter.state.selection.main.head, 6)
    } finally {
      await unmount(mounted)
    }
  })

  test('readOnly toggle keeps the draft and caret while flipping editability', async () => {
    const mounted = await mount(<ConfigHarness />)
    try {
      const viewBefore = await seedDraft('draft body', 4)

      await act(async () => {
        controls.setReadOnly(true)
        await Promise.resolve()
      })

      const viewAfter = currentView()
      assert.equal(viewAfter === viewBefore, true)
      assert.equal(viewAfter.state.doc.toString(), 'draft body')
      assert.equal(viewAfter.state.selection.main.head, 4)
      assert.equal(viewAfter.state.readOnly, true)

      await act(async () => {
        controls.setReadOnly(false)
        await Promise.resolve()
      })
      assert.equal(currentView().state.readOnly, false)
      assert.equal(currentView().state.doc.toString(), 'draft body')
    } finally {
      await unmount(mounted)
    }
  })

  test('showLineNumbers toggle keeps the draft and caret', async () => {
    const mounted = await mount(<ConfigHarness />)
    try {
      const viewBefore = await seedDraft('numbered draft', 3)

      await act(async () => {
        controls.setShowLineNumbers(false)
        await Promise.resolve()
      })

      const viewAfter = currentView()
      assert.equal(viewAfter === viewBefore, true)
      assert.equal(viewAfter.state.doc.toString(), 'numbered draft')
      assert.equal(viewAfter.state.selection.main.head, 3)
      // The gutter is gone but the document is untouched.
      assert.equal(
        viewAfter.dom.querySelector('.cm-lineNumbers') === null,
        true
      )
    } finally {
      await unmount(mounted)
    }
  })

  test('simultaneous config changes still preserve the draft and caret', async () => {
    const mounted = await mount(<ConfigHarness />)
    try {
      const viewBefore = await seedDraft('combo draft', 5)

      await act(async () => {
        controls.setLanguage('json')
        controls.setReadOnly(true)
        controls.setShowLineNumbers(false)
        await Promise.resolve()
      })

      const viewAfter = currentView()
      assert.equal(viewAfter === viewBefore, true)
      assert.equal(viewAfter.state.doc.toString(), 'combo draft')
      assert.equal(viewAfter.state.selection.main.head, 5)
      assert.equal(viewAfter.state.readOnly, true)
    } finally {
      await unmount(mounted)
    }
  })
})
