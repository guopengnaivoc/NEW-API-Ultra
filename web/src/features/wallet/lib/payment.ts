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
import { isSafeExternalHttpUrl } from '@/lib/external-url'

import {
  PAYMENT_TYPES,
  DEFAULT_PRESET_MULTIPLIERS,
  DEFAULT_PAYMENT_TYPE,
  DEFAULT_MIN_TOPUP,
} from '../constants'
import type { PaymentMethod, PresetAmount, TopupInfo } from '../types'

// ============================================================================
// Payment Processing Functions
// ============================================================================

/**
 * Check if browser is Safari
 */
function isSafariBrowser(): boolean {
  return (
    navigator.userAgent.includes('Safari') &&
    !navigator.userAgent.includes('Chrome')
  )
}

/**
 * Submit payment form (for non-Stripe payments)
 */
export function submitPaymentForm(
  url: string,
  params: Record<string, unknown>
): boolean {
  if (!isSafeExternalHttpUrl(url)) {
    return false
  }
  const form = document.createElement('form')
  form.action = url
  form.method = 'POST'

  // Don't open in new tab for Safari
  if (!isSafariBrowser()) {
    form.target = '_blank'
  }

  // Add form parameters
  Object.entries(params).forEach(([key, value]) => {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = key
    input.value = String(value)
    form.appendChild(input)
  })

  document.body.appendChild(form)
  form.submit()
  document.body.removeChild(form)
  return true
}

/**
 * Check if payment method is Stripe
 */
export function isStripePayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.STRIPE
}

/**
 * Check if payment method is Waffo
 */
export function isWaffoPayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.WAFFO
}

/**
 * Check if payment method is Waffo Pancake
 *
 * Pancake is a metered-style payment that goes through a dedicated checkout
 * URL flow rather than the generic epay form submission, so it must be
 * special-cased in payment dispatch logic.
 */
export function isWaffoPancakePayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.WAFFO_PANCAKE
}

export interface PaymentProcessors {
  regular: (topupAmount: number, paymentType: string) => Promise<boolean>
  waffo: (topupAmount: number, payMethodIndex: number) => Promise<boolean>
  waffoPancake: (topupAmount: number) => Promise<boolean>
}

export interface PaymentConfirmation {
  readonly paymentMethod: PaymentMethod
  readonly topupAmount: number
  readonly paymentAmount: number
  readonly waffoMethodIndex: number | null
  readonly discountRate: number
  readonly usdExchangeRate: number
}

export interface PaymentConfirmationSelection {
  readonly paymentMethod: PaymentMethod
  readonly topupAmount: number
  readonly waffoMethodIndex: number | null
  readonly loadingKey: string
  readonly minTopupAmount: number
  readonly discountRate: number
  readonly usdExchangeRate: number
}

export type PaymentConfirmationSelectionResult =
  | 'ready'
  | 'stale'
  | 'below-minimum'
  | 'calculation-error'

interface PaymentConfirmationControllerRuntime {
  calculatePaymentAmount: (
    topupAmount: number,
    paymentType: string
  ) => Promise<number | null>
  invalidatePaymentAmount: () => void
  setConfirmation: (confirmation: PaymentConfirmation | null) => void
  setLoading: (loadingKey: string | null) => void
}

export function createPaymentConfirmation(
  confirmation: PaymentConfirmation
): PaymentConfirmation {
  return Object.freeze({
    ...confirmation,
    paymentMethod: Object.freeze({ ...confirmation.paymentMethod }),
  })
}

export function createPaymentConfirmationController(
  runtime: PaymentConfirmationControllerRuntime
) {
  let generation = 0
  let activePaymentType: string | null = null

  const initializeAmount = (
    topupAmount: number,
    paymentType: string
  ): Promise<number | null> => {
    generation += 1
    activePaymentType = paymentType
    return runtime.calculatePaymentAmount(topupAmount, paymentType)
  }

  const amountChanged = (
    topupAmount: number,
    defaultPaymentType: string
  ): Promise<number | null> => {
    generation += 1
    runtime.setConfirmation(null)
    runtime.setLoading(null)
    return runtime.calculatePaymentAmount(
      topupAmount,
      activePaymentType || defaultPaymentType
    )
  }

  const select = async (
    input: PaymentConfirmationSelection
  ): Promise<PaymentConfirmationSelectionResult> => {
    const selectionGeneration = ++generation
    const selection = {
      ...input,
      paymentMethod: { ...input.paymentMethod },
    }
    activePaymentType = selection.paymentMethod.type
    runtime.setConfirmation(null)
    runtime.setLoading(selection.loadingKey)

    try {
      if (selection.topupAmount < selection.minTopupAmount) {
        return 'below-minimum'
      }

      const paymentAmount = await runtime.calculatePaymentAmount(
        selection.topupAmount,
        selection.paymentMethod.type
      )
      if (selectionGeneration !== generation || paymentAmount === null) {
        return 'stale'
      }
      if (!Number.isFinite(paymentAmount) || paymentAmount <= 0) {
        return 'calculation-error'
      }

      runtime.setConfirmation(
        createPaymentConfirmation({
          paymentMethod: selection.paymentMethod,
          topupAmount: selection.topupAmount,
          paymentAmount,
          waffoMethodIndex: selection.waffoMethodIndex,
          discountRate: selection.discountRate,
          usdExchangeRate: selection.usdExchangeRate,
        })
      )
      return 'ready'
    } finally {
      if (selectionGeneration === generation) {
        runtime.setLoading(null)
      }
    }
  }

  const close = () => {
    generation += 1
    runtime.invalidatePaymentAmount()
    runtime.setConfirmation(null)
    runtime.setLoading(null)
  }

  return {
    initializeAmount,
    amountChanged,
    select,
    close,
  }
}

export async function dispatchSelectedPayment(
  paymentMethod: PaymentMethod,
  topupAmount: number,
  waffoMethodIndex: number | null,
  processors: PaymentProcessors
): Promise<boolean> {
  if (isWaffoPayment(paymentMethod.type)) {
    if (waffoMethodIndex === null) {
      return false
    }
    return processors.waffo(topupAmount, waffoMethodIndex)
  }

  if (isWaffoPancakePayment(paymentMethod.type)) {
    return processors.waffoPancake(topupAmount)
  }

  return processors.regular(topupAmount, paymentMethod.type)
}

export function dispatchPaymentConfirmation(
  confirmation: PaymentConfirmation,
  processors: PaymentProcessors
): Promise<boolean> {
  return dispatchSelectedPayment(
    confirmation.paymentMethod,
    confirmation.topupAmount,
    confirmation.waffoMethodIndex,
    processors
  )
}

/**
 * Get default payment type from topup info
 */
export function getDefaultPaymentType(topupInfo: TopupInfo | null): string {
  if (!topupInfo) {
    return DEFAULT_PAYMENT_TYPE
  }

  // Return first available payment method or default
  if (topupInfo.pay_methods?.length > 0) {
    return topupInfo.pay_methods[0].type
  }

  if (topupInfo.enable_stripe_topup) {
    return PAYMENT_TYPES.STRIPE
  }

  if (topupInfo.enable_waffo_topup) {
    return PAYMENT_TYPES.WAFFO
  }

  if (topupInfo.enable_waffo_pancake_topup) {
    return PAYMENT_TYPES.WAFFO_PANCAKE
  }

  return DEFAULT_PAYMENT_TYPE
}

/**
 * Get minimum topup amount from topup info
 */
export function getMinTopupAmount(topupInfo: TopupInfo | null): number {
  if (!topupInfo) {
    return DEFAULT_MIN_TOPUP
  }

  if (topupInfo.enable_online_topup) {
    return topupInfo.min_topup
  }

  if (topupInfo.enable_stripe_topup) {
    return topupInfo.stripe_min_topup
  }

  if (topupInfo.enable_waffo_topup) {
    return topupInfo.waffo_min_topup || DEFAULT_MIN_TOPUP
  }

  if (topupInfo.enable_waffo_pancake_topup) {
    return topupInfo.waffo_pancake_min_topup || DEFAULT_MIN_TOPUP
  }

  return DEFAULT_MIN_TOPUP
}

/**
 * Generate preset amounts based on minimum topup
 */
export function generatePresetAmounts(minAmount: number): PresetAmount[] {
  return DEFAULT_PRESET_MULTIPLIERS.map((multiplier) => ({
    value: minAmount * multiplier,
  }))
}

/**
 * Merge custom preset amounts with discounts
 */
export function mergePresetAmounts(
  amountOptions: number[],
  discounts: Record<number, number>
): PresetAmount[] {
  if (!amountOptions || amountOptions.length === 0) {
    return []
  }

  return amountOptions.map((amount) => ({
    value: amount,
    discount: discounts[amount] || 1.0,
  }))
}
