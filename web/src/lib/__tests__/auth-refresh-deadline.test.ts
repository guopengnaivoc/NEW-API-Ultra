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
import { test } from 'node:test'

import axios, { type AxiosAdapter } from 'axios'

import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import * as authSession from '../auth-session'
import type { AuthRefreshRuntime, RefreshOutcome } from '../auth-session'

let freshAuthSessionImport = 0

async function importFreshAuthSession(): Promise<typeof authSession> {
  freshAuthSessionImport += 1
  const modulePath = `../auth-session.ts?auth-refresh-production=${freshAuthSessionImport}`
  return (await import(modulePath)) as typeof authSession
}

test('a refresh waiting for the browser lock returns a transient result at its deadline', async () => {
  const navigatorDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'navigator'
  )
  const setTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'setTimeout'
  )
  const clearTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'clearTimeout'
  )

  let deadlineCallback: (() => void) | undefined
  let deadlineDelay: number | undefined
  let deadlineSignal: AbortSignal | undefined
  let deadlineWasCleared = false
  let rejectLock!: (reason?: unknown) => void
  let lockSettled = false
  const lockPromise = new Promise<never>((_, reject) => {
    rejectLock = (reason) => {
      if (lockSettled) return
      lockSettled = true
      reject(reason)
    }
  })

  Object.defineProperty(globalThis, 'setTimeout', {
    configurable: true,
    value: (
      handler: TimerHandler,
      timeout?: number
    ): ReturnType<typeof setTimeout> => {
      assert.equal(typeof handler, 'function')
      deadlineCallback = handler as () => void
      deadlineDelay = timeout
      return 41 as unknown as ReturnType<typeof setTimeout>
    },
  })
  Object.defineProperty(globalThis, 'clearTimeout', {
    configurable: true,
    value: (timer: ReturnType<typeof setTimeout>) => {
      if (timer === (41 as unknown as ReturnType<typeof setTimeout>)) {
        deadlineWasCleared = true
      }
    },
  })

  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {
      locks: {
        request: (_name: string, options: LockOptions): Promise<never> => {
          deadlineSignal = options.signal
          deadlineSignal?.addEventListener(
            'abort',
            () => rejectLock(deadlineSignal?.reason),
            { once: true }
          )
          return lockPromise
        },
      },
    },
  })

  useAuthStore.getState().auth.setBootstrapState('checking')
  const refresh = authSession.refreshAuthentication()

  try {
    await Promise.resolve()
    assert.equal(deadlineDelay, 10_000)
    assert.ok(deadlineCallback)
    assert.ok(deadlineSignal)
    assert.equal(deadlineSignal.aborted, false)

    deadlineCallback()
    const outcome = await refresh

    assert.equal(outcome.kind, 'transient_error')
    assert.equal(deadlineSignal.aborted, true)
    assert.equal(useAuthStore.getState().auth.bootstrapState, 'idle')
    assert.equal(deadlineWasCleared, true)
  } finally {
    rejectLock(new Error('test lock cleanup'))
    await refresh.catch(() => undefined)
    useAuthStore.getState().auth.reset('idle')
    if (setTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'setTimeout', setTimeoutDescriptor)
    }
    if (clearTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'clearTimeout', clearTimeoutDescriptor)
    }
    if (navigatorDescriptor) {
      Object.defineProperty(globalThis, 'navigator', navigatorDescriptor)
    } else {
      Reflect.deleteProperty(globalThis, 'navigator')
    }
  }
})

test('the production Axios request uses the browser-lock deadline signal', async () => {
  const navigatorDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'navigator'
  )
  const originalAdapter = axios.defaults.adapter
  let lockSignal: AbortSignal | undefined
  let requestSignal: unknown
  const adapter: AxiosAdapter = async (config) => {
    requestSignal = config.signal
    return {
      data: { success: false },
      status: 503,
      statusText: 'Service Unavailable',
      headers: {},
      config,
    }
  }
  axios.defaults.adapter = adapter
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {
      locks: {
        request: async (
          _name: string,
          options: LockOptions,
          callback: (lock: Lock | null) => Promise<RefreshOutcome>
        ) => {
          lockSignal = options.signal
          return callback(null)
        },
      },
    },
  })

  try {
    const productionAuthSession = await importFreshAuthSession()
    const outcome = await productionAuthSession.refreshAuthentication()

    assert.equal(outcome.kind, 'transient_error')
    assert.ok(lockSignal)
    assert.strictEqual(requestSignal, lockSignal)
  } finally {
    axios.defaults.adapter = originalAdapter
    useAuthStore.getState().auth.reset('idle')
    if (navigatorDescriptor) {
      Object.defineProperty(globalThis, 'navigator', navigatorDescriptor)
    } else {
      Reflect.deleteProperty(globalThis, 'navigator')
    }
  }
})

test('a production refresh cannot publish after a newer authentication epoch', async () => {
  const originalAdapter = axios.defaults.adapter
  const staleBundle: AuthBundle = {
    access_token: 'stale-access-token',
    token_type: 'Bearer',
    access_expires_at: 4_102_444_800,
    user: {
      id: 42,
      username: 'stale-user',
      role: 1,
    },
    session: {
      sid: 'stale-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 100,
      last_active_at: 100,
      expires_at: 4_102_444_800,
    },
  }
  const newerBundle: AuthBundle = {
    ...staleBundle,
    access_token: 'newer-access-token',
    user: { ...staleBundle.user, id: 84, username: 'newer-user' },
    session: { ...staleBundle.session, sid: 'newer-session' },
  }
  let notifyRequestStarted!: () => void
  const requestStarted = new Promise<void>((resolve) => {
    notifyRequestStarted = resolve
  })
  let releaseResponse!: () => void
  const responseReady = new Promise<void>((resolve) => {
    releaseResponse = resolve
  })
  const adapter: AxiosAdapter = async (config) => {
    notifyRequestStarted()
    await responseReady
    return {
      data: { success: true, data: staleBundle },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  axios.defaults.adapter = adapter
  let refresh: Promise<RefreshOutcome> | undefined

  try {
    const productionAuthSession = await importFreshAuthSession()
    productionAuthSession.applyAuthBundle(staleBundle, false)
    refresh = productionAuthSession.refreshAuthentication()
    await requestStarted

    productionAuthSession.applyAuthBundle(newerBundle, false)
    releaseResponse()
    const outcome = await refresh

    assert.equal(outcome.kind, 'transient_error')
    assert.equal(
      useAuthStore.getState().auth.accessToken,
      newerBundle.access_token
    )
    assert.equal(
      useAuthStore.getState().auth.session?.sid,
      newerBundle.session.sid
    )
  } finally {
    releaseResponse()
    await refresh?.catch(() => undefined)
    axios.defaults.adapter = originalAdapter
    useAuthStore.getState().auth.reset('idle')
  }
})

test('production refreshes are single-flight only until the shared request settles', async () => {
  const originalAdapter = axios.defaults.adapter
  let notifyFirstRequest!: () => void
  const firstRequestStarted = new Promise<void>((resolve) => {
    notifyFirstRequest = resolve
  })
  let notifySecondRequest!: () => void
  const secondRequestStarted = new Promise<void>((resolve) => {
    notifySecondRequest = resolve
  })
  const releaseRequests: Array<() => void> = []
  let requestCount = 0
  const adapter: AxiosAdapter = async (config) => {
    requestCount += 1
    if (requestCount === 1) {
      notifyFirstRequest()
    } else if (requestCount === 2) {
      notifySecondRequest()
    }
    await new Promise<void>((resolve) => {
      releaseRequests.push(() => resolve())
    })
    return {
      data: { success: false },
      status: 503,
      statusText: 'Service Unavailable',
      headers: {},
      config,
    }
  }
  axios.defaults.adapter = adapter
  const pendingRefreshes: Array<Promise<RefreshOutcome>> = []

  try {
    const productionAuthSession = await importFreshAuthSession()
    const first = productionAuthSession.refreshAuthentication()
    const concurrent = productionAuthSession.refreshAuthentication()
    pendingRefreshes.push(first, concurrent)
    await firstRequestStarted

    assert.strictEqual(concurrent, first)
    assert.equal(requestCount, 1)

    releaseRequests[0]()
    await Promise.all([first, concurrent])

    const later = productionAuthSession.refreshAuthentication()
    pendingRefreshes.push(later)
    assert.notStrictEqual(later, first)
    await secondRequestStarted
    assert.equal(requestCount, 2)

    releaseRequests[1]()
    assert.equal((await later).kind, 'transient_error')
  } finally {
    for (const release of releaseRequests) {
      release()
    }
    await Promise.allSettled(pendingRefreshes)
    axios.defaults.adapter = originalAdapter
    useAuthStore.getState().auth.reset('idle')
  }
})

test('an already-aborted refresh-race wait rejects without scheduling a timer', async () => {
  const setTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'setTimeout'
  )
  const controller = new AbortController()
  const deadlineReason = new Error('refresh deadline reached')
  controller.abort(deadlineReason)
  let timerCallback: (() => void) | undefined
  let wait: Promise<void> | undefined

  Object.defineProperty(globalThis, 'setTimeout', {
    configurable: true,
    value: (handler: TimerHandler): ReturnType<typeof setTimeout> => {
      assert.equal(typeof handler, 'function')
      timerCallback = handler as () => void
      return 89 as unknown as ReturnType<typeof setTimeout>
    },
  })

  try {
    wait = authSession.waitForRefreshRace(80, controller.signal)

    assert.equal(timerCallback, undefined)
    await assert.rejects(wait, (error: unknown) => {
      assert.strictEqual(error, deadlineReason)
      return true
    })
  } finally {
    timerCallback?.()
    await wait?.catch(() => undefined)
    if (setTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'setTimeout', setTimeoutDescriptor)
    }
  }
})

test('a completed refresh-race wait removes its abort listener and resolves exactly once', async () => {
  const setTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'setTimeout'
  )
  const clearTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'clearTimeout'
  )
  const controller = new AbortController()
  const addEventListener = controller.signal.addEventListener.bind(
    controller.signal
  )
  const removeEventListener = controller.signal.removeEventListener.bind(
    controller.signal
  )
  const addEventListenerDescriptor = Object.getOwnPropertyDescriptor(
    controller.signal,
    'addEventListener'
  )
  const removeEventListenerDescriptor = Object.getOwnPropertyDescriptor(
    controller.signal,
    'removeEventListener'
  )
  const scheduledTimer = 97 as unknown as ReturnType<typeof setTimeout>
  let timerCallback: (() => void) | undefined
  let addedAbortListener: EventListenerOrEventListenerObject | undefined
  let removedAbortListener: EventListenerOrEventListenerObject | undefined
  let addCount = 0
  let removeCount = 0
  let clearCount = 0
  let wait: Promise<void> | undefined

  Object.defineProperty(globalThis, 'setTimeout', {
    configurable: true,
    value: (
      handler: TimerHandler,
      timeout?: number
    ): ReturnType<typeof setTimeout> => {
      assert.equal(typeof handler, 'function')
      assert.equal(timeout, 200)
      timerCallback = handler as () => void
      return scheduledTimer
    },
  })
  Object.defineProperty(globalThis, 'clearTimeout', {
    configurable: true,
    value: () => {
      clearCount += 1
    },
  })
  Object.defineProperty(controller.signal, 'addEventListener', {
    configurable: true,
    value: (
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | AddEventListenerOptions
    ) => {
      if (type === 'abort') {
        addCount += 1
        addedAbortListener = listener
      }
      addEventListener(type, listener, options)
    },
  })
  Object.defineProperty(controller.signal, 'removeEventListener', {
    configurable: true,
    value: (
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | EventListenerOptions
    ) => {
      if (type === 'abort') {
        removeCount += 1
        removedAbortListener = listener
      }
      removeEventListener(type, listener, options)
    },
  })

  try {
    wait = authSession.waitForRefreshRace(200, controller.signal)
    assert.equal(addCount, 1)
    assert.ok(timerCallback)

    timerCallback()

    const result = await wait
    if (typeof addedAbortListener !== 'function') {
      assert.fail('expected a callable abort listener')
    }
    addedAbortListener.call(controller.signal, new Event('abort'))

    assert.strictEqual(result, undefined)
    assert.equal(removeCount, 1)
    assert.strictEqual(removedAbortListener, addedAbortListener)
    assert.equal(clearCount, 0)
  } finally {
    timerCallback?.()
    await wait?.catch(() => undefined)
    controller.abort()
    if (setTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'setTimeout', setTimeoutDescriptor)
    }
    if (clearTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'clearTimeout', clearTimeoutDescriptor)
    }
    if (addEventListenerDescriptor) {
      Object.defineProperty(
        controller.signal,
        'addEventListener',
        addEventListenerDescriptor
      )
    } else {
      Reflect.deleteProperty(controller.signal, 'addEventListener')
    }
    if (removeEventListenerDescriptor) {
      Object.defineProperty(
        controller.signal,
        'removeEventListener',
        removeEventListenerDescriptor
      )
    } else {
      Reflect.deleteProperty(controller.signal, 'removeEventListener')
    }
  }
})

test('a deadline abort cancels a refresh-race delay before its timer fires', async () => {
  const setTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'setTimeout'
  )
  const clearTimeoutDescriptor = Object.getOwnPropertyDescriptor(
    globalThis,
    'clearTimeout'
  )
  const controller = new AbortController()
  const removeEventListener = controller.signal.removeEventListener.bind(
    controller.signal
  )
  const removeEventListenerDescriptor = Object.getOwnPropertyDescriptor(
    controller.signal,
    'removeEventListener'
  )
  const deadlineReason = new Error('refresh deadline reached')
  const scheduledTimer = 73 as unknown as ReturnType<typeof setTimeout>
  let timerCallback: (() => void) | undefined
  let notifyTimerScheduled!: () => void
  const timerScheduled = new Promise<void>((resolve) => {
    notifyTimerScheduled = resolve
  })
  let clearedTimer: ReturnType<typeof setTimeout> | undefined
  let abortListenerRemoveCount = 0
  let requestCount = 0
  let markTransientCount = 0
  let refresh: Promise<RefreshOutcome> | undefined

  Object.defineProperty(globalThis, 'setTimeout', {
    configurable: true,
    value: (
      handler: TimerHandler,
      timeout?: number
    ): ReturnType<typeof setTimeout> => {
      assert.equal(typeof handler, 'function')
      assert.equal(timeout, 80)
      timerCallback = handler as () => void
      notifyTimerScheduled()
      return scheduledTimer
    },
  })
  Object.defineProperty(globalThis, 'clearTimeout', {
    configurable: true,
    value: (timer: ReturnType<typeof setTimeout>) => {
      clearedTimer = timer
    },
  })
  Object.defineProperty(controller.signal, 'removeEventListener', {
    configurable: true,
    value: (
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | EventListenerOptions
    ) => {
      if (type === 'abort') {
        abortListenerRemoveCount += 1
      }
      removeEventListener(type, listener, options)
    },
  })

  try {
    const runtime: AuthRefreshRuntime = {
      request: async () => {
        requestCount += 1
        if (requestCount > 1) {
          return {
            status: 503,
            data: null,
          }
        }
        return {
          status: 409,
          data: { code: 'AUTH_REFRESH_RACE' },
        }
      },
      getExpectedSID: () => 'session-a',
      parseBundle: () => null,
      acceptBundle: () => undefined,
      clear: () => undefined,
      markTransient: () => {
        markTransientCount += 1
      },
      wait: authSession.waitForRefreshRace,
    }

    refresh = authSession.createRefreshRunner(runtime, controller.signal)()
    await timerScheduled

    controller.abort(deadlineReason)

    assert.equal(clearedTimer, scheduledTimer)
    assert.equal(abortListenerRemoveCount, 1)
    const outcome = await refresh
    assert.equal(outcome.kind, 'transient_error')
    if (outcome.kind === 'transient_error') {
      assert.strictEqual(outcome.error, deadlineReason)
    }
    assert.equal(requestCount, 1)
    assert.equal(markTransientCount, 1)

    timerCallback?.()
    assert.equal(abortListenerRemoveCount, 1)
  } finally {
    if (!clearedTimer) {
      timerCallback?.()
    }
    await refresh?.catch(() => undefined)
    if (setTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'setTimeout', setTimeoutDescriptor)
    }
    if (clearTimeoutDescriptor) {
      Object.defineProperty(globalThis, 'clearTimeout', clearTimeoutDescriptor)
    }
    if (removeEventListenerDescriptor) {
      Object.defineProperty(
        controller.signal,
        'removeEventListener',
        removeEventListenerDescriptor
      )
    } else {
      Reflect.deleteProperty(controller.signal, 'removeEventListener')
    }
  }
})
