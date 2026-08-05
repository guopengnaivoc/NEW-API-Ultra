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
import i18next from 'i18next'
import { useState, useCallback, useRef } from 'react'
import { toast } from 'sonner'

import { openExternalUrl } from '@/lib/external-url'

import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoAmount,
  calculateWaffoPancakeAmount,
  requestPayment,
  requestStripePayment,
  isApiSuccess,
} from '../api'
import {
  isStripePayment,
  isWaffoPayment,
  isWaffoPancakePayment,
  submitPaymentForm,
} from '../lib'
import type { AmountRequest, AmountResponse } from '../types'

// ============================================================================
// Payment Hook
// ============================================================================

type AmountCalculator = (request: AmountRequest) => Promise<AmountResponse>

export interface PaymentAmountCalculators {
  regular: AmountCalculator
  stripe: AmountCalculator
  waffo: AmountCalculator
  waffoPancake: AmountCalculator
}

const defaultPaymentAmountCalculators: PaymentAmountCalculators = {
  regular: calculateAmount,
  stripe: calculateStripeAmount,
  waffo: calculateWaffoAmount,
  waffoPancake: calculateWaffoPancakeAmount,
}

export async function requestPaymentAmount(
  topupAmount: number,
  paymentType: string,
  calculators: PaymentAmountCalculators = defaultPaymentAmountCalculators
): Promise<number> {
  let calculator = calculators.regular
  if (isStripePayment(paymentType)) {
    calculator = calculators.stripe
  } else if (isWaffoPayment(paymentType)) {
    calculator = calculators.waffo
  } else if (isWaffoPancakePayment(paymentType)) {
    calculator = calculators.waffoPancake
  }

  const response = await calculator({ amount: topupAmount })
  if (!isApiSuccess(response) || !response.data) {
    return 0
  }

  return Number.parseFloat(response.data)
}

interface PaymentAmountControllerRuntime {
  requestAmount: (topupAmount: number, paymentType: string) => Promise<number>
  setAmount: (amount: number) => void
  setCalculating: (calculating: boolean) => void
}

export function createPaymentAmountController(
  runtime: PaymentAmountControllerRuntime
) {
  let latestRequestId = 0

  const calculate = async (topupAmount: number, paymentType: string) => {
    const requestId = ++latestRequestId
    runtime.setCalculating(true)

    try {
      const calculatedAmount = await runtime.requestAmount(
        topupAmount,
        paymentType
      )
      if (requestId !== latestRequestId) {
        return null
      }

      runtime.setAmount(calculatedAmount)
      return calculatedAmount
    } catch {
      if (requestId !== latestRequestId) {
        return null
      }

      runtime.setAmount(0)
      return 0
    } finally {
      if (requestId === latestRequestId) {
        runtime.setCalculating(false)
      }
    }
  }

  const invalidate = () => {
    latestRequestId += 1
    runtime.setCalculating(false)
  }

  return { calculate, invalidate }
}

export function usePayment() {
  const [amount, setAmount] = useState<number>(0)
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)
  const amountControllerRef = useRef<ReturnType<
    typeof createPaymentAmountController
  > | null>(null)
  if (!amountControllerRef.current) {
    amountControllerRef.current = createPaymentAmountController({
      requestAmount: requestPaymentAmount,
      setAmount,
      setCalculating,
    })
  }

  // Calculate payment amount
  const calculatePaymentAmount = useCallback(
    (topupAmount: number, paymentType: string) => {
      if (!amountControllerRef.current) {
        return Promise.resolve(null)
      }
      return amountControllerRef.current.calculate(topupAmount, paymentType)
    },
    []
  )

  const invalidatePaymentAmount = useCallback(() => {
    amountControllerRef.current?.invalidate()
  }, [])

  // Process payment
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string) => {
      try {
        setProcessing(true)

        const isStripe = isStripePayment(paymentType)
        const amount = Math.floor(topupAmount)

        const response = isStripe
          ? await requestStripePayment({
              amount,
              payment_method: 'stripe',
            })
          : await requestPayment({
              amount,
              payment_method: paymentType,
            })

        if (!isApiSuccess(response)) {
          toast.error(response.message || i18next.t('Payment request failed'))
          return false
        }

        // Handle Stripe payment
        if (isStripe && response.data?.pay_link) {
          if (!openExternalUrl(response.data.pay_link as string)) {
            toast.error(i18next.t('Invalid payment redirect URL'))
            return false
          }
          toast.success(i18next.t('Redirecting to payment page...'))
          return true
        }

        // Handle non-Stripe payment
        if (!isStripe && response.data) {
          const url = (response as unknown as { url?: string }).url
          if (url) {
            if (!submitPaymentForm(url, response.data)) {
              toast.error(i18next.t('Invalid payment redirect URL'))
              return false
            }
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        return false
      } catch {
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return {
    amount,
    calculating,
    processing,
    calculatePaymentAmount,
    invalidatePaymentAmount,
    processPayment,
    setAmount,
  }
}
