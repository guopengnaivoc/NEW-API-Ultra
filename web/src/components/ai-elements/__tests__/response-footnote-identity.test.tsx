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
import type { FootnoteNode, ParsedNode } from 'stream-markdown-parser'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLAnchorElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
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
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Back: 'Back',
        'Back to footnote {{id}} reference':
          'Back to footnote {{id}} reference',
      },
    },
  },
})
const { Response } = await import('../response')
const { createResponseRenderer } = await import('../response-renderer')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const repeatedFootnote =
  'First reference[^1] and second reference[^1].\n\n[^1]: Shared note.'
const singleFootnote = 'Another reference[^1].\n\n[^1]: Another note.'

function ResponseSet() {
  return (
    <>
      <section data-response='first'>
        <Response>{repeatedFootnote}</Response>
      </section>
      <section data-response='second'>
        <Response>{singleFootnote}</Response>
      </section>
    </>
  )
}

function getIds(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll<HTMLElement>('[id]'), (node) => {
    assert.notEqual(node.id, '')
    return node.id
  })
}

function assertLocalFragmentsResolve(response: HTMLElement): void {
  const fragmentLinks = [
    ...response.querySelectorAll<HTMLAnchorElement>('a[href^="#"]'),
  ]

  for (const link of fragmentLinks) {
    const href = link.getAttribute('href')
    assert.ok(href)
    const targetId = href.slice(1)
    const localTargets = response.querySelectorAll<HTMLElement>(
      `[id="${targetId}"]`
    )
    const documentTargets = document.querySelectorAll<HTMLElement>(
      `[id="${targetId}"]`
    )

    assert.equal(localTargets.length, 1)
    assert.equal(documentTargets.length, 1)
    assert.equal(documentTargets[0], localTargets[0])
    assert.ok(response.contains(localTargets[0]))
  }
}

function assertLocalFootnoteNavigation(
  section: HTMLElement,
  expectedReferenceCount: number
): void {
  const references = [
    ...section.querySelectorAll<HTMLAnchorElement>('sup > a[id]'),
  ]
  const definitions = [
    ...section.querySelectorAll<HTMLElement>('section li[id]'),
  ]
  const backlinks = [
    ...section.querySelectorAll<HTMLAnchorElement>('section li > a[href]'),
  ]

  assert.equal(references.length, expectedReferenceCount)
  assert.equal(definitions.length, 1)
  assert.equal(backlinks.length, 1)
  assert.equal(
    new Set(references.map((reference) => reference.id)).size,
    expectedReferenceCount
  )

  const definition = definitions[0]
  for (const reference of references) {
    assert.equal(reference.getAttribute('href'), `#${definition.id}`)
    assert.equal(
      document.querySelector<HTMLElement>(`[id="${definition.id}"]`),
      definition
    )
    assert.ok(section.contains(definition))
  }

  assert.equal(backlinks[0].getAttribute('href'), `#${references[0].id}`)
  assert.equal(
    document.querySelector<HTMLAnchorElement>(`[id="${references[0].id}"]`),
    references[0]
  )
  assert.ok(section.contains(references[0]))
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

test('footnote identities stay unique and local to each mounted response', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(<ResponseSet />)
      await Promise.resolve()
    })

    const first = container.querySelector<HTMLElement>(
      '[data-response="first"]'
    )
    const second = container.querySelector<HTMLElement>(
      '[data-response="second"]'
    )
    assert.ok(first)
    assert.ok(second)

    const initialIds = getIds(container)
    assert.equal(new Set(initialIds).size, initialIds.length)
    assertLocalFootnoteNavigation(first, 2)
    assertLocalFootnoteNavigation(second, 1)
    assertLocalFragmentsResolve(first)
    assertLocalFragmentsResolve(second)

    await act(async () => {
      root.render(<ResponseSet />)
      await Promise.resolve()
    })

    assert.deepEqual(getIds(container), initialIds)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('footnote labels that resemble occurrence suffixes keep distinct navigation targets', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <Response>
          {
            'A[^1] B[^1] C[^1-2].\n\n[^1]: One note.\n[^1-2]: One dash two note.'
          }
        </Response>
      )
      await Promise.resolve()
    })

    const references = [
      ...container.querySelectorAll<HTMLAnchorElement>('sup > a[id]'),
    ]
    const definitions = [
      ...container.querySelectorAll<HTMLElement>('section li[id]'),
    ]
    const backlinks = [
      ...container.querySelectorAll<HTMLAnchorElement>('section li > a[href]'),
    ]

    const ids = getIds(container)
    assert.equal(new Set(ids).size, ids.length)
    assert.equal(references.length, 3)
    assert.equal(definitions.length, 2)
    assert.equal(backlinks.length, 2)

    const firstDefinition = definitions.find((definition) =>
      definition.textContent?.includes('One note.')
    )
    const secondDefinition = definitions.find((definition) =>
      definition.textContent?.includes('One dash two note.')
    )
    assert.ok(firstDefinition)
    assert.ok(secondDefinition)

    assert.equal(references[0].getAttribute('href'), `#${firstDefinition.id}`)
    assert.equal(references[1].getAttribute('href'), `#${firstDefinition.id}`)
    assert.equal(references[2].getAttribute('href'), `#${secondDefinition.id}`)
    assert.equal(backlinks[0].getAttribute('href'), `#${references[0].id}`)
    assert.equal(backlinks[1].getAttribute('href'), `#${references[2].id}`)
    assert.equal(
      document.querySelector<HTMLElement>(`[id="${firstDefinition.id}"]`),
      firstDefinition
    )
    assert.equal(
      document.querySelector<HTMLElement>(`[id="${secondDefinition.id}"]`),
      secondDefinition
    )
    assert.equal(
      document.querySelector<HTMLAnchorElement>(`[id="${references[0].id}"]`),
      references[0]
    )
    assert.equal(
      document.querySelector<HTMLAnchorElement>(`[id="${references[2].id}"]`),
      references[2]
    )
    assertLocalFragmentsResolve(container)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('footnote labels that resemble reference prefixes keep definitions distinct', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <Response>
          {'A[^1] B[^ref-1].\n\n[^1]: One note.\n[^ref-1]: Ref one note.'}
        </Response>
      )
      await Promise.resolve()
    })

    const references = [
      ...container.querySelectorAll<HTMLAnchorElement>('sup > a[id]'),
    ]
    const definitions = [
      ...container.querySelectorAll<HTMLElement>('section li[id]'),
    ]
    const backlinks = [
      ...container.querySelectorAll<HTMLAnchorElement>('section li > a[href]'),
    ]

    const ids = getIds(container)
    assert.equal(new Set(ids).size, ids.length)
    assert.equal(references.length, 2)
    assert.equal(definitions.length, 2)
    assert.equal(backlinks.length, 2)

    const firstDefinition = definitions.find((definition) =>
      definition.textContent?.includes('One note.')
    )
    const secondDefinition = definitions.find((definition) =>
      definition.textContent?.includes('Ref one note.')
    )
    assert.ok(firstDefinition)
    assert.ok(secondDefinition)

    assert.equal(references[0].getAttribute('href'), `#${firstDefinition.id}`)
    assert.equal(references[1].getAttribute('href'), `#${secondDefinition.id}`)
    assert.equal(backlinks[0].getAttribute('href'), `#${references[0].id}`)
    assert.equal(backlinks[1].getAttribute('href'), `#${references[1].id}`)
    assert.equal(
      document.querySelector<HTMLElement>(`[id="${firstDefinition.id}"]`),
      firstDefinition
    )
    assert.equal(
      document.querySelector<HTMLElement>(`[id="${secondDefinition.id}"]`),
      secondDefinition
    )
    assert.equal(
      document.querySelector<HTMLAnchorElement>(`[id="${references[0].id}"]`),
      references[0]
    )
    assert.equal(
      document.querySelector<HTMLAnchorElement>(`[id="${references[1].id}"]`),
      references[1]
    )
    assert.equal(
      backlinks[0].getAttribute('aria-label'),
      'Back to footnote 1 reference'
    )
    assert.equal(
      backlinks[1].getAttribute('aria-label'),
      'Back to footnote ref-1 reference'
    )
    assert.equal(backlinks[0].textContent, 'Back')
    assert.equal(backlinks[1].textContent, 'Back')
    assert.doesNotMatch(
      backlinks[1].getAttribute('aria-label') ?? '',
      /footnote-(definition|reference)-\d/
    )
    assertLocalFragmentsResolve(container)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('nested footnote backlinks target references allocated inside later definitions', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const content =
    '[^a]: A note references nested[^b].\n[^b]: B note.\n\nBody[^a].'

  try {
    await act(async () => {
      root.render(<Response>{content}</Response>)
      await Promise.resolve()
    })

    const definitions = [
      ...container.querySelectorAll<HTMLElement>('section li[id]'),
    ]
    const aDefinition = definitions.find((definition) =>
      definition.textContent?.includes('A note references nested')
    )
    const bDefinition = definitions.find((definition) =>
      definition.textContent?.includes('B note.')
    )
    assert.ok(aDefinition)
    assert.ok(bDefinition)

    const bodyReference = container.querySelector<HTMLAnchorElement>(
      'div > p sup > a[id]'
    )
    const nestedReference =
      aDefinition.querySelector<HTMLAnchorElement>('sup > a[id]')
    const aBacklink =
      aDefinition.querySelector<HTMLAnchorElement>(':scope > a[href]')
    const bBacklink =
      bDefinition.querySelector<HTMLAnchorElement>(':scope > a[href]')
    assert.ok(bodyReference)
    assert.ok(nestedReference)
    assert.ok(aBacklink)
    assert.ok(bBacklink)

    const ids = getIds(container)
    assert.equal(new Set(ids).size, ids.length)
    assert.equal(
      container.querySelector(
        '[id$="footnote-reference-0"], [href$="footnote-reference-0"]'
      ),
      null
    )

    assert.equal(bodyReference.getAttribute('href'), `#${aDefinition.id}`)
    assert.equal(nestedReference.getAttribute('href'), `#${bDefinition.id}`)
    assert.equal(aBacklink.getAttribute('href'), `#${bodyReference.id}`)
    assert.equal(bBacklink.getAttribute('href'), `#${nestedReference.id}`)
    assert.equal(
      document.querySelector<HTMLAnchorElement>(`[id="${nestedReference.id}"]`),
      nestedReference
    )
    assertLocalFragmentsResolve(container)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('malformed footnote identities render labels without dangling anchors', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const missingDefinitionReference = {
    id: 'missing',
    raw: '[^missing]',
    type: 'footnote_reference',
  } satisfies ParsedNode
  const lateReference = {
    id: 'late',
    raw: '[^late]',
    type: 'footnote_reference',
  } satisfies ParsedNode
  const lateDefinition = {
    children: [],
    id: 'late',
    raw: '[^late]: Late note.',
    type: 'footnote',
  } satisfies FootnoteNode
  const missingDefinitionRenderer = createResponseRenderer(
    'malformed-missing-definition',
    [missingDefinitionReference],
    []
  )
  const lateReferenceRenderer = createResponseRenderer(
    'malformed-late-reference',
    [],
    [lateDefinition]
  )

  try {
    await act(async () => {
      root.render(
        <>
          <section data-malformed='missing-definition'>
            {missingDefinitionRenderer.renderChildren([
              missingDefinitionReference,
            ])}
          </section>
          <section data-malformed='late-reference'>
            {lateReferenceRenderer.renderChildren([lateReference])}
            {lateReferenceRenderer.renderFootnotes([lateDefinition])}
          </section>
        </>
      )
      await Promise.resolve()
    })

    const missingDefinition = container.querySelector<HTMLElement>(
      '[data-malformed="missing-definition"]'
    )
    const late = container.querySelector<HTMLElement>(
      '[data-malformed="late-reference"]'
    )
    assert.ok(missingDefinition)
    assert.ok(late)

    assert.equal(missingDefinition.textContent, '[missing]')
    assert.equal(late.querySelector('sup')?.textContent, '[late]')
    assert.equal(missingDefinition.querySelector('sup > a'), null)
    assert.equal(late.querySelector('sup > a'), null)
    assertLocalFragmentsResolve(missingDefinition)
    assertLocalFragmentsResolve(late)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
