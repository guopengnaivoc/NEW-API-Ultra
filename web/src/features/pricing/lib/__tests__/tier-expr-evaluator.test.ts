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

import { type ExtraTokenValues, evalExprLocally } from '../tier-expr'

const emptyExtras: ExtraTokenValues = {
  cacheReadTokens: 0,
  cacheCreateTokens: 0,
  cacheCreate1hTokens: 0,
  imageTokens: 0,
  imageOutputTokens: 0,
  audioInputTokens: 0,
  audioOutputTokens: 0,
}

describe('local billing expression evaluator', () => {
  test('rejects JavaScript execution without creating a global side effect', () => {
    const globals = globalThis as typeof globalThis & {
      __newApiBillingExprProbe?: number
    }
    delete globals.__newApiBillingExprProbe

    const result = evalExprLocally(
      '(globalThis.__newApiBillingExprProbe = 1337, 0)',
      1,
      1,
      emptyExtras
    )

    assert.equal(globals.__newApiBillingExprProbe, undefined)
    assert.equal(result.cost, 0)
    assert.equal(result.matchedTier, '')
    assert.notEqual(result.error, null)
  })

  test('evaluates supported arithmetic, tiers, and version prefixes', () => {
    const result = evalExprLocally(
      'v1:len > 100 ? tier("large", max(p * 2, c * 3) + abs(-4)) : tier("small", floor(p / 2))',
      125,
      30,
      emptyExtras
    )

    assert.deepEqual(result, {
      cost: 254,
      matchedTier: 'large',
      error: null,
    })
  })

  test('uses explicit extra token variables in the selected branch', () => {
    const result = evalExprLocally(
      'cc1h > 0 && img_o >= 2 ? tier("media", cr + cc + cc1h + img + img_o + ai + ao) : 0',
      0,
      0,
      {
        cacheReadTokens: 1,
        cacheCreateTokens: 2,
        cacheCreate1hTokens: 3,
        imageTokens: 4,
        imageOutputTokens: 5,
        audioInputTokens: 6,
        audioOutputTokens: 7,
      }
    )

    assert.deepEqual(result, {
      cost: 28,
      matchedTier: 'media',
      error: null,
    })
  })

  test('rejects unsupported identifiers, functions, and property access', () => {
    for (const expression of [
      'globalThis',
      'prompt(1)',
      'p.toString()',
      'p["constructor"]',
      'tier.constructor("return 1")()',
    ]) {
      const result = evalExprLocally(expression, 1, 1, emptyExtras)
      assert.notEqual(result.error, null, expression)
      assert.equal(result.cost, 0, expression)
      assert.equal(result.matchedTier, '', expression)
    }
  })

  test('validates identifiers, calls, and types in every lazy branch', () => {
    for (const expression of [
      'true ? 1 : globalThis',
      'false ? globalThis : 1',
      'false && globalThis == 0 ? 1 : 0',
      'true || prompt(1) == 1 ? 1 : 0',
      'true ? 1 : prompt(1)',
      'true ? 1 : tier("unused")',
      'true ? 1 : max(1)',
      'true ? 1 : max("unused", 1)',
      'true ? 1 : max(1, prompt(1))',
      'true ? 1 : tier(1, 2)',
      'true ? 1 : "unused" + 1',
      'true ? 1 : !1',
    ]) {
      const result = evalExprLocally(expression, 1, 1, emptyExtras)
      assert.notEqual(result.error, null, expression)
      assert.equal(result.cost, 0, expression)
      assert.equal(result.matchedTier, '', expression)
    }
  })

  test('keeps conditional evaluation lazy when branch result types differ', () => {
    const selectedNumber = evalExprLocally(
      'p > 0 ? 1 : "unused"',
      1,
      0,
      emptyExtras
    )
    assert.deepEqual(selectedNumber, {
      cost: 1,
      matchedTier: '',
      error: null,
    })

    const selectedString = evalExprLocally(
      'p > 0 ? 1 : "unused"',
      0,
      0,
      emptyExtras
    )
    assert.notEqual(selectedString.error, null)
    assert.equal(selectedString.cost, 0)
    assert.equal(selectedString.matchedTier, '')
  })

  test('evaluates exponentiation with backend precedence and associativity', () => {
    for (const [expression, expectedCost] of [
      ['p ** 2', 9],
      ['2 ** 3 ** 2', 512],
      ['-2 ** 2 + 4', 0],
      ['2 ** -2', 0.25],
    ] as const) {
      const result = evalExprLocally(expression, 3, 0, emptyExtras)
      assert.deepEqual(
        result,
        {
          cost: expectedCost,
          matchedTier: '',
          error: null,
        },
        expression
      )
    }
  })

  test('concatenates strings with plus without coercing mixed types', () => {
    const result = evalExprLocally(
      'tier("long_" + "context", p + 2)',
      3,
      0,
      emptyExtras
    )

    assert.deepEqual(result, {
      cost: 5,
      matchedTier: 'long_context',
      error: null,
    })

    const mixed = evalExprLocally('"cost: " + p', 3, 0, emptyExtras)
    assert.notEqual(mixed.error, null)
    assert.equal(mixed.cost, 0)
    assert.equal(mixed.matchedTier, '')
  })

  test('rejects non-finite and negative billing results', () => {
    for (const expression of [
      '1 / 0',
      '0 / 0',
      '1e308 * 1e308',
      '1e308 ** 2',
      '1 - 2',
      '-1',
    ]) {
      const result = evalExprLocally(expression, 1, 1, emptyExtras)
      assert.notEqual(result.error, null, expression)
      assert.equal(result.cost, 0, expression)
      assert.equal(result.matchedTier, '', expression)
    }
  })

  test('rejects invalid estimator inputs before calculating a cost', () => {
    for (const { name, promptTokens, completionTokens, extraTokenValues } of [
      {
        name: 'negative prompt tokens',
        promptTokens: -1,
        completionTokens: 0,
        extraTokenValues: emptyExtras,
      },
      {
        name: 'non-finite completion tokens',
        promptTokens: 0,
        completionTokens: Number.NaN,
        extraTokenValues: emptyExtras,
      },
      {
        name: 'infinite cache tokens',
        promptTokens: 0,
        completionTokens: 0,
        extraTokenValues: {
          ...emptyExtras,
          cacheReadTokens: Number.POSITIVE_INFINITY,
        },
      },
      {
        name: 'non-finite audio tokens',
        promptTokens: 0,
        completionTokens: 0,
        extraTokenValues: {
          ...emptyExtras,
          audioOutputTokens: Number.NaN,
        },
      },
      {
        name: 'negative image tokens',
        promptTokens: 0,
        completionTokens: 0,
        extraTokenValues: {
          ...emptyExtras,
          imageTokens: -1,
        },
      },
    ]) {
      const result = evalExprLocally(
        'p + c + cr + img + ao',
        promptTokens,
        completionTokens,
        extraTokenValues
      )
      assert.notEqual(result.error, null, name)
      assert.equal(result.cost, 0, name)
      assert.equal(result.matchedTier, '', name)
    }
  })

  test('enforces expression length, token, parse-depth, and evaluation caps', () => {
    for (const { name, expression, expectedError } of [
      {
        name: 'expression length',
        expression: '1'.repeat(10_001),
        expectedError: 'Billing expression is too long',
      },
      {
        name: 'token count',
        expression: `${'1+'.repeat(1_024)}1`,
        expectedError: 'Billing expression is too complex',
      },
      {
        name: 'parse depth',
        expression: `${'('.repeat(129)}1${')'.repeat(129)}`,
        expectedError: 'Billing expression nesting is too deep',
      },
      {
        name: 'evaluation depth',
        expression: Array.from({ length: 300 }, () => '1').join('+'),
        expectedError: 'Billing expression is too complex',
      },
    ]) {
      const result = evalExprLocally(expression, 0, 0, emptyExtras)
      assert.equal(result.error, expectedError, name)
      assert.equal(result.cost, 0, name)
      assert.equal(result.matchedTier, '', name)
    }
  })
})
