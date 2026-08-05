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

import { createPaymentAmountController } from '../use-payment'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, resolve, reject }
}

describe('payment amount request coordination', () => {
  test('keeps a late older response from replacing the latest amount', async () => {
    const firstResponse = deferred<number>()
    const secondResponse = deferred<number>()
    const amounts: number[] = []
    const calculating: boolean[] = []
    const controller = createPaymentAmountController({
      requestAmount: (topupAmount) =>
        topupAmount === 100 ? firstResponse.promise : secondResponse.promise,
      setAmount: (amount) => amounts.push(amount),
      setCalculating: (value) => calculating.push(value),
    })

    const first = controller.calculate(100, 'stripe')
    const second = controller.calculate(200, 'stripe')
    secondResponse.resolve(20)
    assert.equal(await second, 20)
    firstResponse.resolve(10)

    assert.equal(await first, null)
    assert.deepEqual(amounts, [20])
    assert.deepEqual(calculating, [true, true, false])
  })

  test('keeps a late older failure from clearing the latest amount', async () => {
    const firstResponse = deferred<number>()
    const secondResponse = deferred<number>()
    const amounts: number[] = []
    const controller = createPaymentAmountController({
      requestAmount: (topupAmount) =>
        topupAmount === 100 ? firstResponse.promise : secondResponse.promise,
      setAmount: (amount) => amounts.push(amount),
      setCalculating: () => undefined,
    })

    const first = controller.calculate(100, 'stripe')
    const second = controller.calculate(200, 'stripe')
    secondResponse.resolve(20)
    assert.equal(await second, 20)
    firstResponse.reject(new Error('stale failure'))

    assert.equal(await first, null)
    assert.deepEqual(amounts, [20])
  })
})
