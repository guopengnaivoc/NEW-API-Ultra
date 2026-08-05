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
  'HTMLDivElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'KeyboardEvent',
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

const resizeListeners = new Set<EventListenerOrEventListenerObject>()
const nativeWindowAddEventListener = domWindow.addEventListener.bind(
  domWindow
) as unknown as (
  type: string,
  listener: EventListenerOrEventListenerObject,
  options?: boolean | AddEventListenerOptions
) => void
const nativeWindowRemoveEventListener = domWindow.removeEventListener.bind(
  domWindow
) as unknown as (
  type: string,
  listener: EventListenerOrEventListenerObject,
  options?: boolean | EventListenerOptions
) => void
Object.defineProperties(domWindow, {
  addEventListener: {
    configurable: true,
    value: (
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | AddEventListenerOptions
    ) => {
      if (type === 'resize') {
        resizeListeners.add(listener)
      }
      nativeWindowAddEventListener(type, listener, options)
    },
  },
  removeEventListener: {
    configurable: true,
    value: (
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | EventListenerOptions
    ) => {
      if (type === 'resize') {
        resizeListeners.delete(listener)
      }
      nativeWindowRemoveEventListener(type, listener, options)
    },
  },
})

class TestResizeObserver {
  static instances: TestResizeObserver[] = []

  readonly observed = new Set<Element>()
  disconnected = false

  constructor(private readonly callback: ResizeObserverCallback) {
    TestResizeObserver.instances.push(this)
  }

  observe(target: Element) {
    this.observed.add(target)
  }

  unobserve(target: Element) {
    this.observed.delete(target)
  }

  disconnect() {
    this.disconnected = true
    this.observed.clear()
  }

  trigger() {
    this.callback([], this as unknown as ResizeObserver)
  }
}

class TestMutationObserver {
  static instances: TestMutationObserver[] = []

  readonly observed = new Set<Node>()
  disconnected = false

  constructor(private readonly callback: MutationCallback) {
    TestMutationObserver.instances.push(this)
  }

  observe(target: Node) {
    this.observed.add(target)
  }

  disconnect() {
    this.disconnected = true
    this.observed.clear()
  }

  trigger() {
    this.callback([], this as unknown as MutationObserver)
  }
}

class TestFontFaceSet {
  readonly listeners = new Set<EventListener>()

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    if (type === 'loadingdone' && typeof listener === 'function') {
      this.listeners.add(listener)
    }
  }

  removeEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject
  ) {
    if (type === 'loadingdone' && typeof listener === 'function') {
      this.listeners.delete(listener)
    }
  }

  dispatchLoadingDone() {
    const event = new Event('loadingdone')
    for (const listener of this.listeners) {
      listener(event)
    }
  }
}

const resizeObserverDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  'ResizeObserver'
)
Object.defineProperty(globalThis, 'ResizeObserver', {
  configurable: true,
  value: TestResizeObserver,
})

const mutationObserverDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  'MutationObserver'
)
Object.defineProperty(globalThis, 'MutationObserver', {
  configurable: true,
  value: TestMutationObserver,
})

const fontFaceSet = new TestFontFaceSet()
const fontsDescriptor = Object.getOwnPropertyDescriptor(document, 'fonts')
Object.defineProperty(document, 'fonts', {
  configurable: true,
  value: fontFaceSet,
})

let availableWidth = 100
let characterWidth = 10
let responsiveTypographyIsWide = false
Object.defineProperties(domWindow.HTMLElement.prototype, {
  offsetHeight: {
    configurable: true,
    get() {
      return this.classList.contains('truncate') ? 20 : 0
    },
  },
  offsetWidth: {
    configurable: true,
    get() {
      return this.classList.contains('truncate') ? availableWidth : 0
    },
  },
  scrollHeight: {
    configurable: true,
    get() {
      return this.classList.contains('truncate') ? 20 : 0
    },
  },
  scrollWidth: {
    configurable: true,
    get() {
      const metricClassList = this.classList.contains('truncate')
        ? this.parentElement?.classList
        : this.classList
      const isResponsiveWide =
        metricClassList?.contains('sm:tracking-wide') &&
        responsiveTypographyIsWide
      const width =
        metricClassList?.contains('tracking-wide') || isResponsiveWide
          ? 12
          : characterWidth
      return this.classList.contains('truncate')
        ? (this.textContent?.length ?? 0) * width
        : 0
    },
  },
})

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { LongText } = await import('../long-text')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function latestObserver(): TestResizeObserver {
  const observer = TestResizeObserver.instances.at(-1)
  assert.ok(observer)
  return observer
}

function latestMutationObserver(): TestMutationObserver {
  const observer = TestMutationObserver.instances.at(-1)
  assert.ok(observer)
  return observer
}

function triggerMutationObservers() {
  const observers = [...TestMutationObserver.instances]
  observers.forEach((observer) => observer.trigger())
}

function overflowTriggerCount(container: Element) {
  return container.querySelectorAll('[data-long-text-overflow-trigger]').length
}

after(() => {
  domWindow.close()
  if (resizeObserverDescriptor) {
    Object.defineProperty(
      globalThis,
      'ResizeObserver',
      resizeObserverDescriptor
    )
  } else {
    Reflect.deleteProperty(globalThis, 'ResizeObserver')
  }
  if (mutationObserverDescriptor) {
    Object.defineProperty(
      globalThis,
      'MutationObserver',
      mutationObserverDescriptor
    )
  } else {
    Reflect.deleteProperty(globalThis, 'MutationObserver')
  }
  if (fontsDescriptor) {
    Object.defineProperty(document, 'fonts', fontsDescriptor)
  } else {
    Reflect.deleteProperty(document, 'fonts')
  }
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('LongText follows container resizes and child changes', async () => {
  availableWidth = 100
  characterWidth = 10
  responsiveTypographyIsWide = false
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(<LongText>short</LongText>)
      await Promise.resolve()
    })

    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)
    const initialObserver = latestObserver()
    assert.equal(initialObserver.observed.size, 1)

    availableWidth = 30
    await act(async () => {
      initialObserver.trigger()
      await Promise.resolve()
    })

    assert.equal(initialObserver.disconnected, false)
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 2)

    availableWidth = 100
    await act(async () => {
      initialObserver.trigger()
      await Promise.resolve()
    })

    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)

    await act(async () => {
      root.render(<LongText>this value is much longer</LongText>)
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 2)

    await act(async () => {
      root.render(<LongText>tiny</LongText>)
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)

    await act(async () => {
      root.render(<LongText>font size</LongText>)
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)
    assert.equal(fontFaceSet.listeners.size, 1)

    characterWidth = 12
    await act(async () => {
      fontFaceSet.dispatchLoadingDone()
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 2)

    characterWidth = 10
    await act(async () => {
      fontFaceSet.dispatchLoadingDone()
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)

    const finalObserver = latestObserver()
    const finalMutationObserver = latestMutationObserver()
    await act(async () => root.unmount())
    assert.equal(finalObserver.disconnected, true)
    assert.equal(finalMutationObserver.disconnected, true)
    assert.equal(fontFaceSet.listeners.size, 0)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
  }
})

test('LongText preserves a self-stateful descendant when overflow controls appear', async () => {
  availableWidth = 100
  characterWidth = 10
  responsiveTypographyIsWide = false
  domWindow.innerWidth = 375
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  const descendantText = {
    set: null as ((value: string) => void) | null,
  }
  let mountCount = 0

  function StatefulDescendant() {
    const [text, setText] = useState('short')
    descendantText.set = setText
    const [mountId] = useState(() => {
      mountCount += 1
      return mountCount
    })
    return <span data-mount-id={mountId}>{text}</span>
  }

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <LongText>
          <StatefulDescendant />
        </LongText>
      )
      await Promise.resolve()
    })

    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(mountCount, 1)
    const updateDescendantText = descendantText.set
    assert.ok(updateDescendantText)

    await act(async () => {
      updateDescendantText('this self-stateful descendant is much longer')
      await Promise.resolve()
    })

    assert.match(container.textContent ?? '', /longer/)

    await act(async () => {
      triggerMutationObservers()
      await Promise.resolve()
    })

    assert.equal(mountCount, 1)
    assert.match(
      container.querySelector('.truncate')?.textContent ?? '',
      /longer/
    )
    assert.equal(overflowTriggerCount(container), 2)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
  }
})

test('LongText shares one viewport listener until the final instance unmounts', async () => {
  availableWidth = 100
  characterWidth = 10
  responsiveTypographyIsWide = false
  domWindow.innerWidth = 375
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  resizeListeners.clear()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <>
          <LongText key='first'>first</LongText>
          <LongText key='second'>second</LongText>
        </>
      )
      await Promise.resolve()
    })
    assert.equal(resizeListeners.size, 1)

    await act(async () => {
      root.render(<LongText key='first'>first</LongText>)
      await Promise.resolve()
    })
    assert.equal(resizeListeners.size, 1)

    await act(async () => root.unmount())
    assert.equal(resizeListeners.size, 0)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
  }
})

test('LongText coalesces raw viewport events before remeasuring all instances', async () => {
  availableWidth = 100
  characterWidth = 10
  responsiveTypographyIsWide = false
  domWindow.innerWidth = 375
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  resizeListeners.clear()
  const animationFrames = new Map<number, FrameRequestCallback>()
  let nextAnimationFrameId = 1
  const nativeRequestAnimationFrame = window.requestAnimationFrame
  const nativeCancelAnimationFrame = window.cancelAnimationFrame
  Object.defineProperties(window, {
    requestAnimationFrame: {
      configurable: true,
      value: (callback: FrameRequestCallback) => {
        const id = nextAnimationFrameId
        nextAnimationFrameId += 1
        animationFrames.set(id, callback)
        return id
      },
    },
    cancelAnimationFrame: {
      configurable: true,
      value: (id: number) => {
        animationFrames.delete(id)
      },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <>
          <LongText className='sm:tracking-wide'>narrowtext</LongText>
          <LongText className='sm:tracking-wide'>narrowtext</LongText>
        </>
      )
      await Promise.resolve()
    })
    assert.equal(overflowTriggerCount(container), 0)

    responsiveTypographyIsWide = true
    domWindow.innerWidth = 800
    await act(async () => {
      window.dispatchEvent(new Event('resize'))
      window.dispatchEvent(new Event('resize'))
      await Promise.resolve()
    })

    assert.equal(animationFrames.size, 1)
    assert.equal(overflowTriggerCount(container), 0)

    await act(async () => {
      const callbacks = [...animationFrames.values()]
      animationFrames.clear()
      for (const callback of callbacks) {
        callback(16)
      }
      await Promise.resolve()
    })

    assert.equal(overflowTriggerCount(container), 4)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    Object.defineProperties(window, {
      requestAnimationFrame: {
        configurable: true,
        value: nativeRequestAnimationFrame,
      },
      cancelAnimationFrame: {
        configurable: true,
        value: nativeCancelAnimationFrame,
      },
    })
    container.remove()
  }
})

test('LongText remeasures when its className changes text metrics', async () => {
  availableWidth = 100
  characterWidth = 10
  responsiveTypographyIsWide = false
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(<LongText>narrowtext</LongText>)
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)

    await act(async () => {
      root.render(<LongText className='tracking-wide'>narrowtext</LongText>)
      await Promise.resolve()
    })

    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 2)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
  }
})

test('LongText remeasures responsive text metrics after a viewport resize', async () => {
  availableWidth = 100
  characterWidth = 10
  responsiveTypographyIsWide = false
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(<LongText className='sm:tracking-wide'>narrowtext</LongText>)
      await Promise.resolve()
    })
    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 0)

    responsiveTypographyIsWide = true
    await act(async () => {
      window.dispatchEvent(new Event('resize'))
      await new Promise<void>((resolve) => {
        window.requestAnimationFrame(() => resolve())
      })
    })

    assert.equal(container.querySelectorAll('.truncate').length, 1)
    assert.equal(overflowTriggerCount(container), 2)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
  }
})

test('LongText mobile popover trigger is keyboard accessible when text overflows', async () => {
  availableWidth = 30
  characterWidth = 10
  responsiveTypographyIsWide = false
  TestResizeObserver.instances.length = 0
  TestMutationObserver.instances.length = 0
  fontFaceSet.listeners.clear()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(<LongText>this value is much longer</LongText>)
      await Promise.resolve()
    })

    const trigger = container.querySelector(
      '[data-slot="popover-trigger"]'
    ) as HTMLElement | null
    assert.ok(trigger)
    assert.equal(trigger.getAttribute('role'), 'button')
    assert.equal(trigger.getAttribute('tabindex'), '0')
    assert.equal(trigger.getAttribute('aria-expanded'), 'false')
    assert.equal(
      trigger.getAttribute('aria-label'),
      'this value is much longer'
    )
    trigger.focus()
    assert.equal(document.activeElement, trigger)

    await act(async () => {
      trigger.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })
      )
      trigger.dispatchEvent(
        new KeyboardEvent('keyup', { key: 'Enter', bubbles: true })
      )
      await Promise.resolve()
    })

    assert.equal(trigger.getAttribute('aria-expanded'), 'true')
    const popoverContent = document.querySelector(
      '[data-slot="popover-content"]'
    )
    assert.ok(popoverContent)
    assert.equal(popoverContent.textContent, 'this value is much longer')
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
  }
})
