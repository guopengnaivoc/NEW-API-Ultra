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
import { describe, test } from 'node:test'

import { PAYMENT_TYPES } from '../../constants'
import { createPaymentAmountController } from '../../hooks/use-payment'
import {
  createPaymentConfirmation,
  createPaymentConfirmationController,
  dispatchPaymentConfirmation,
} from '../payment'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, reject, resolve }
}

function stripeSelection(topupAmount: number, loadingKey: string) {
  return {
    paymentMethod: {
      name: 'Stripe',
      type: PAYMENT_TYPES.STRIPE,
    },
    topupAmount,
    discountRate: 1,
    waffoMethodIndex: null,
    loadingKey,
    minTopupAmount: 10,
    usdExchangeRate: 1,
  }
}

describe('payment confirmation snapshot', () => {
  test('dispatches the exact Waffo details reviewed by the user', async () => {
    const selectedMethod = {
      name: 'Waffo Card',
      type: PAYMENT_TYPES.WAFFO,
      icon: '/card.svg',
    }
    let liveTopupAmount = 120
    let liveWaffoMethodIndex = 3
    const confirmation = createPaymentConfirmation({
      paymentMethod: selectedMethod,
      topupAmount: liveTopupAmount,
      paymentAmount: 18.75,
      waffoMethodIndex: liveWaffoMethodIndex,
      discountRate: 0.9,
      usdExchangeRate: 7.2,
    })

    selectedMethod.name = 'Changed method'
    liveTopupAmount = 999
    liveWaffoMethodIndex = 8

    const calls: string[] = []
    const success = await dispatchPaymentConfirmation(confirmation, {
      regular: async () => false,
      waffo: async (amount, index) => {
        calls.push(`${amount}:${index}`)
        return true
      },
      waffoPancake: async () => false,
    })

    assert.equal(success, true)
    assert.deepEqual(calls, ['120:3'])
    assert.equal(Object.isFrozen(confirmation), true)
    assert.equal(Object.isFrozen(confirmation.paymentMethod), true)
    assert.deepEqual(confirmation, {
      paymentMethod: {
        name: 'Waffo Card',
        type: PAYMENT_TYPES.WAFFO,
        icon: '/card.svg',
      },
      topupAmount: 120,
      paymentAmount: 18.75,
      waffoMethodIndex: 3,
      discountRate: 0.9,
      usdExchangeRate: 7.2,
    })
    assert.equal(liveTopupAmount, 999)
    assert.equal(liveWaffoMethodIndex, 8)
  })

  test('invalidates a pending selection when the topup amount changes', async () => {
    const staleSelection = deferred<number | null>()
    const amountRefresh = deferred<number | null>()
    const currentSelection = deferred<number | null>()
    const calculations: string[] = []
    const confirmations: Array<ReturnType<
      typeof createPaymentConfirmation
    > | null> = []
    const loading: Array<string | null> = []
    const responses = [
      staleSelection.promise,
      amountRefresh.promise,
      currentSelection.promise,
    ]
    const controller = createPaymentConfirmationController({
      calculatePaymentAmount: (amount, paymentType) => {
        calculations.push(`${amount}:${paymentType}`)
        return responses[calculations.length - 1]
      },
      invalidatePaymentAmount: () => undefined,
      setConfirmation: (confirmation) => confirmations.push(confirmation),
      setLoading: (value) => loading.push(value),
    })
    const selectedMethod = {
      name: 'Waffo Card',
      type: PAYMENT_TYPES.WAFFO,
    }

    const staleResult = controller.select({
      paymentMethod: selectedMethod,
      topupAmount: 100,
      discountRate: 0.95,
      waffoMethodIndex: 3,
      loadingKey: 'waffo-3',
      minTopupAmount: 10,
      usdExchangeRate: 7.2,
    })
    const refreshResult = controller.amountChanged(200, PAYMENT_TYPES.STRIPE)
    staleSelection.resolve(10)
    amountRefresh.resolve(20)

    assert.equal(await staleResult, 'stale')
    assert.equal(await refreshResult, 20)
    assert.equal(
      confirmations.some((confirmation) => confirmation?.topupAmount === 100),
      false
    )

    const currentResult = controller.select({
      paymentMethod: selectedMethod,
      topupAmount: 200,
      discountRate: 0.9,
      waffoMethodIndex: 3,
      loadingKey: 'waffo-3',
      minTopupAmount: 10,
      usdExchangeRate: 7.2,
    })
    selectedMethod.name = 'Mutated after selection'
    currentSelection.resolve(30)

    assert.equal(await currentResult, 'ready')
    assert.deepEqual(calculations, ['100:waffo', '200:waffo', '200:waffo'])
    assert.deepEqual(loading, ['waffo-3', null, 'waffo-3', null])

    const confirmation = confirmations.at(-1)
    assert.ok(confirmation)
    assert.deepEqual(confirmation, {
      paymentMethod: {
        name: 'Waffo Card',
        type: PAYMENT_TYPES.WAFFO,
      },
      topupAmount: 200,
      paymentAmount: 30,
      waffoMethodIndex: 3,
      discountRate: 0.9,
      usdExchangeRate: 7.2,
    })

    const dispatches: string[] = []
    const success = await dispatchPaymentConfirmation(confirmation, {
      regular: async () => false,
      waffo: async (amount, index) => {
        dispatches.push(`${amount}:${index}`)
        return true
      },
      waffoPancake: async () => false,
    })
    assert.equal(success, true)
    assert.deepEqual(dispatches, ['200:3'])
  })

  test('cleans up a failed calculation and allows a retry', async () => {
    const loading: Array<string | null> = []
    const confirmations: Array<ReturnType<
      typeof createPaymentConfirmation
    > | null> = []
    const calculatedAmounts = [0, 25]
    const controller = createPaymentConfirmationController({
      calculatePaymentAmount: async () => calculatedAmounts.shift() ?? null,
      invalidatePaymentAmount: () => undefined,
      setConfirmation: (confirmation) => confirmations.push(confirmation),
      setLoading: (value) => loading.push(value),
    })
    const selection = {
      paymentMethod: {
        name: 'Stripe',
        type: PAYMENT_TYPES.STRIPE,
      },
      topupAmount: 200,
      discountRate: 1,
      waffoMethodIndex: null,
      loadingKey: PAYMENT_TYPES.STRIPE,
      minTopupAmount: 10,
      usdExchangeRate: 1,
    }

    assert.equal(await controller.select(selection), 'calculation-error')
    assert.equal(await controller.select(selection), 'ready')
    assert.deepEqual(loading, [
      PAYMENT_TYPES.STRIPE,
      null,
      PAYMENT_TYPES.STRIPE,
      null,
    ])
    assert.equal(confirmations.at(-1)?.paymentAmount, 25)
  })

  test('close prevents a pending selection from overwriting newer loading ownership', async () => {
    const pendingSelection = deferred<number | null>()
    const confirmations: Array<ReturnType<
      typeof createPaymentConfirmation
    > | null> = []
    const loading: Array<string | null> = []
    let currentLoading: string | null = null
    const publishLoading = (value: string | null) => {
      currentLoading = value
      loading.push(value)
    }
    const controller = createPaymentConfirmationController({
      calculatePaymentAmount: async () => pendingSelection.promise,
      invalidatePaymentAmount: () => undefined,
      setConfirmation: (confirmation) => confirmations.push(confirmation),
      setLoading: publishLoading,
    })

    const result = controller.select({
      paymentMethod: {
        name: 'Stripe',
        type: PAYMENT_TYPES.STRIPE,
      },
      topupAmount: 200,
      discountRate: 1,
      waffoMethodIndex: null,
      loadingKey: PAYMENT_TYPES.STRIPE,
      minTopupAmount: 10,
      usdExchangeRate: 1,
    })

    controller.close()
    publishLoading('newer-payment-interaction')
    pendingSelection.resolve(25)

    assert.equal(await result, 'stale')
    assert.equal(currentLoading, 'newer-payment-interaction')
    assert.deepEqual(loading, [
      PAYMENT_TYPES.STRIPE,
      null,
      'newer-payment-interaction',
    ])
    assert.equal(
      confirmations.some((confirmation) => confirmation !== null),
      false
    )
  })

  test('close invalidates amount and confirmation ownership across reopened selections', async () => {
    const initialResponse = deferred<number>()
    const closedResponse = deferred<number>()
    const staleSuccessResponse = deferred<number>()
    const currentAfterSuccessResponse = deferred<number>()
    const staleFailureResponse = deferred<number>()
    const currentAfterFailureResponse = deferred<number>()
    const responses = [
      initialResponse.promise,
      closedResponse.promise,
      staleSuccessResponse.promise,
      currentAfterSuccessResponse.promise,
      staleFailureResponse.promise,
      currentAfterFailureResponse.promise,
    ]
    const state: {
      amount: number
      calculating: boolean
      confirmation: ReturnType<typeof createPaymentConfirmation> | null
      loading: string | null
    } = {
      amount: 0,
      calculating: false,
      confirmation: null,
      loading: null,
    }
    const currentConfirmation = () => state.confirmation
    const amountController = createPaymentAmountController({
      requestAmount: () => {
        const response = responses.shift()
        assert.ok(response)
        return response
      },
      setAmount: (value) => {
        state.amount = value
      },
      setCalculating: (value) => {
        state.calculating = value
      },
    })
    const controller = createPaymentConfirmationController({
      calculatePaymentAmount: amountController.calculate,
      invalidatePaymentAmount: amountController.invalidate,
      setConfirmation: (value) => {
        state.confirmation = value
      },
      setLoading: (value) => {
        state.loading = value
      },
    })

    const initialSelection = controller.select(stripeSelection(100, 'initial'))
    initialResponse.resolve(25)

    assert.equal(await initialSelection, 'ready')
    assert.equal(state.amount, 25)
    assert.equal(currentConfirmation()?.paymentAmount, 25)

    controller.close()

    assert.equal(state.confirmation, null)
    assert.equal(state.loading, null)
    assert.equal(state.calculating, false)

    const closedSelection = controller.select(
      stripeSelection(200, 'closed-success')
    )
    controller.close()

    assert.equal(state.calculating, false)

    closedResponse.resolve(99)

    assert.equal(await closedSelection, 'stale')
    assert.equal(state.amount, 25)
    assert.equal(state.confirmation, null)

    const staleSuccessSelection = controller.select(
      stripeSelection(300, 'stale-success')
    )
    controller.close()
    const currentAfterSuccessSelection = controller.select(
      stripeSelection(400, 'current-after-success')
    )

    staleSuccessResponse.resolve(75)

    assert.equal(await staleSuccessSelection, 'stale')
    assert.equal(state.amount, 25)
    assert.equal(state.confirmation, null)
    assert.equal(state.loading, 'current-after-success')
    assert.equal(state.calculating, true)

    currentAfterSuccessResponse.resolve(40)

    assert.equal(await currentAfterSuccessSelection, 'ready')
    assert.equal(state.amount, 40)
    assert.equal(currentConfirmation()?.paymentAmount, 40)
    assert.equal(state.loading, null)
    assert.equal(state.calculating, false)

    const staleFailureSelection = controller.select(
      stripeSelection(500, 'stale-failure')
    )
    controller.close()
    const currentAfterFailureSelection = controller.select(
      stripeSelection(600, 'current-after-failure')
    )

    staleFailureResponse.reject(new Error('stale calculation failure'))

    assert.equal(await staleFailureSelection, 'stale')
    assert.equal(state.amount, 40)
    assert.equal(state.confirmation, null)
    assert.equal(state.loading, 'current-after-failure')
    assert.equal(state.calculating, true)

    currentAfterFailureResponse.resolve(50)

    assert.equal(await currentAfterFailureSelection, 'ready')
    assert.equal(state.amount, 50)
    assert.equal(currentConfirmation()?.paymentAmount, 50)
    assert.equal(state.loading, null)
    assert.equal(state.calculating, false)
  })
})
