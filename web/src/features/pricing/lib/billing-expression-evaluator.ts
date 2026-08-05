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
const MAX_EXPRESSION_LENGTH = 10_000
const MAX_TOKENS = 2_048
const MAX_PARSE_DEPTH = 128
const MAX_EVALUATION_DEPTH = 256

type TokenType = 'number' | 'string' | 'identifier' | 'symbol' | 'eof'

type Token = {
  type: TokenType
  value: string
  position: number
}

type LiteralNode = {
  type: 'literal'
  value: number | string | boolean
}

type IdentifierNode = {
  type: 'identifier'
  name: string
}

type UnaryNode = {
  type: 'unary'
  operator: '+' | '-' | '!'
  operand: ExpressionNode
}

type BinaryNode = {
  type: 'binary'
  operator:
    | '+'
    | '-'
    | '*'
    | '**'
    | '/'
    | '%'
    | '<'
    | '<='
    | '>'
    | '>='
    | '=='
    | '!='
    | '&&'
    | '||'
  left: ExpressionNode
  right: ExpressionNode
}

type ConditionalNode = {
  type: 'conditional'
  condition: ExpressionNode
  consequent: ExpressionNode
  alternate: ExpressionNode
}

type CallNode = {
  type: 'call'
  name: string
  arguments: ExpressionNode[]
}

type ExpressionNode =
  | LiteralNode
  | IdentifierNode
  | UnaryNode
  | BinaryNode
  | ConditionalNode
  | CallNode

type ExpressionValue = number | string | boolean
type ExpressionStaticType = 'number' | 'string' | 'boolean' | 'unknown'

export type BillingExpressionEnvironment = Record<string, number>

export type BillingExpressionResult = {
  cost: number
  matchedTier: string
}

class BillingExpressionTokenizer {
  private index = 0

  constructor(private readonly source: string) {}

  tokenize(): Token[] {
    const tokens: Token[] = []
    while (this.index < this.source.length) {
      this.skipWhitespace()
      if (this.index >= this.source.length) break

      const position = this.index
      const char = this.source[this.index]
      if (
        this.isDigit(char) ||
        (char === '.' && this.isDigit(this.source[this.index + 1]))
      ) {
        tokens.push(this.readNumber())
      } else if (char === '"' || char === "'") {
        tokens.push(this.readString())
      } else if (this.isIdentifierStart(char)) {
        tokens.push(this.readIdentifier())
      } else {
        tokens.push(this.readSymbol())
      }

      if (tokens.length > MAX_TOKENS) {
        throw new Error('Billing expression is too complex')
      }

      if (this.index === position) {
        throw new Error(
          `Unexpected character at position ${String(this.index + 1)}`
        )
      }
    }
    tokens.push({ type: 'eof', value: '', position: this.source.length })
    return tokens
  }

  private skipWhitespace() {
    while (
      this.index < this.source.length &&
      /\s/u.test(this.source[this.index])
    ) {
      this.index += 1
    }
  }

  private readNumber(): Token {
    const position = this.index
    const match = this.source
      .slice(this.index)
      .match(/^(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?/u)
    if (!match) {
      throw new Error(`Invalid number at position ${String(position + 1)}`)
    }
    this.index += match[0].length
    const value = Number(match[0])
    if (!Number.isFinite(value)) {
      throw new Error(`Invalid number at position ${String(position + 1)}`)
    }
    return { type: 'number', value: match[0], position }
  }

  private readString(): Token {
    const position = this.index
    const quote = this.source[this.index]
    this.index += 1
    let value = ''

    while (this.index < this.source.length) {
      const char = this.source[this.index]
      this.index += 1
      if (char === quote) {
        return { type: 'string', value, position }
      }
      if (char === '\n' || char === '\r') {
        throw new Error(
          `Unterminated string at position ${String(position + 1)}`
        )
      }
      if (char !== '\\') {
        value += char
        continue
      }

      if (this.index >= this.source.length) {
        break
      }
      const escaped = this.source[this.index]
      this.index += 1
      const simpleEscapes: Record<string, string> = {
        '"': '"',
        "'": "'",
        '\\': '\\',
        '/': '/',
        b: '\b',
        f: '\f',
        n: '\n',
        r: '\r',
        t: '\t',
      }
      if (escaped in simpleEscapes) {
        value += simpleEscapes[escaped]
        continue
      }
      if (escaped === 'u') {
        const hex = this.source.slice(this.index, this.index + 4)
        if (!/^[0-9a-fA-F]{4}$/u.test(hex)) {
          throw new Error(
            `Invalid Unicode escape at position ${String(this.index + 1)}`
          )
        }
        value += String.fromCharCode(Number.parseInt(hex, 16))
        this.index += 4
        continue
      }
      throw new Error(`Invalid string escape at position ${String(this.index)}`)
    }

    throw new Error(`Unterminated string at position ${String(position + 1)}`)
  }

  private readIdentifier(): Token {
    const position = this.index
    this.index += 1
    while (
      this.index < this.source.length &&
      this.isIdentifierPart(this.source[this.index])
    ) {
      this.index += 1
    }
    return {
      type: 'identifier',
      value: this.source.slice(position, this.index),
      position,
    }
  }

  private readSymbol(): Token {
    const position = this.index
    const twoCharacter = this.source.slice(this.index, this.index + 2)
    if (['&&', '||', '==', '!=', '<=', '>=', '**'].includes(twoCharacter)) {
      this.index += 2
      return { type: 'symbol', value: twoCharacter, position }
    }

    const value = this.source[this.index]
    if ('+-*/%<>()?:,!'.includes(value)) {
      this.index += 1
      return { type: 'symbol', value, position }
    }
    throw new Error(
      `Unexpected character "${value}" at position ${String(position + 1)}`
    )
  }

  private isDigit(char: string | undefined): boolean {
    return char !== undefined && char >= '0' && char <= '9'
  }

  private isIdentifierStart(char: string | undefined): boolean {
    return (
      char !== undefined &&
      ((char >= 'a' && char <= 'z') ||
        (char >= 'A' && char <= 'Z') ||
        char === '_')
    )
  }

  private isIdentifierPart(char: string | undefined): boolean {
    return this.isIdentifierStart(char) || this.isDigit(char)
  }
}

class BillingExpressionParser {
  private index = 0
  private depth = 0

  constructor(private readonly tokens: Token[]) {}

  parse(): ExpressionNode {
    const expression = this.parseConditional()
    const trailing = this.current()
    if (trailing.type !== 'eof') {
      throw new Error(
        `Unexpected token "${trailing.value}" at position ${String(trailing.position + 1)}`
      )
    }
    return expression
  }

  private parseConditional(): ExpressionNode {
    const condition = this.parseLogicalOr()
    if (!this.consume('?')) return condition

    return this.withDepth(() => {
      const consequent = this.parseConditional()
      this.expect(':')
      const alternate = this.parseConditional()
      return {
        type: 'conditional',
        condition,
        consequent,
        alternate,
      }
    })
  }

  private parseLogicalOr(): ExpressionNode {
    let expression = this.parseLogicalAnd()
    while (this.consume('||')) {
      expression = {
        type: 'binary',
        operator: '||',
        left: expression,
        right: this.parseLogicalAnd(),
      }
    }
    return expression
  }

  private parseLogicalAnd(): ExpressionNode {
    let expression = this.parseEquality()
    while (this.consume('&&')) {
      expression = {
        type: 'binary',
        operator: '&&',
        left: expression,
        right: this.parseEquality(),
      }
    }
    return expression
  }

  private parseEquality(): ExpressionNode {
    let expression = this.parseComparison()
    while (this.check('==') || this.check('!=')) {
      const operator = this.advance().value as '==' | '!='
      expression = {
        type: 'binary',
        operator,
        left: expression,
        right: this.parseComparison(),
      }
    }
    return expression
  }

  private parseComparison(): ExpressionNode {
    let expression = this.parseAdditive()
    while (
      this.check('<') ||
      this.check('<=') ||
      this.check('>') ||
      this.check('>=')
    ) {
      const operator = this.advance().value as '<' | '<=' | '>' | '>='
      expression = {
        type: 'binary',
        operator,
        left: expression,
        right: this.parseAdditive(),
      }
    }
    return expression
  }

  private parseAdditive(): ExpressionNode {
    let expression = this.parseMultiplicative()
    while (this.check('+') || this.check('-')) {
      const operator = this.advance().value as '+' | '-'
      expression = {
        type: 'binary',
        operator,
        left: expression,
        right: this.parseMultiplicative(),
      }
    }
    return expression
  }

  private parseMultiplicative(): ExpressionNode {
    let expression = this.parseUnary()
    while (this.check('*') || this.check('/') || this.check('%')) {
      const operator = this.advance().value as '*' | '/' | '%'
      expression = {
        type: 'binary',
        operator,
        left: expression,
        right: this.parseUnary(),
      }
    }
    return expression
  }

  private parseUnary(): ExpressionNode {
    if (this.check('+') || this.check('-') || this.check('!')) {
      const operator = this.advance().value as '+' | '-' | '!'
      return this.withDepth(() => ({
        type: 'unary',
        operator,
        operand: this.parseUnary(),
      }))
    }
    return this.parseExponentiation()
  }

  private parseExponentiation(): ExpressionNode {
    const expression = this.parsePrimary()
    if (!this.consume('**')) return expression

    return this.withDepth(() => ({
      type: 'binary',
      operator: '**',
      left: expression,
      right: this.parseUnary(),
    }))
  }

  private parsePrimary(): ExpressionNode {
    const token = this.advance()
    if (token.type === 'number') {
      return { type: 'literal', value: Number(token.value) }
    }
    if (token.type === 'string') {
      return { type: 'literal', value: token.value }
    }
    if (token.type === 'identifier') {
      if (token.value === 'true' || token.value === 'false') {
        return { type: 'literal', value: token.value === 'true' }
      }
      if (!this.consume('(')) {
        return { type: 'identifier', name: token.value }
      }
      return this.withDepth(() => {
        const argumentsList: ExpressionNode[] = []
        if (!this.check(')')) {
          do {
            argumentsList.push(this.parseConditional())
          } while (this.consume(','))
        }
        this.expect(')')
        return { type: 'call', name: token.value, arguments: argumentsList }
      })
    }
    if (token.value === '(') {
      return this.withDepth(() => {
        const expression = this.parseConditional()
        this.expect(')')
        return expression
      })
    }

    const printable = token.type === 'eof' ? 'end of expression' : token.value
    throw new Error(
      `Unexpected token "${printable}" at position ${String(token.position + 1)}`
    )
  }

  private withDepth<T>(parse: () => T): T {
    this.depth += 1
    if (this.depth > MAX_PARSE_DEPTH) {
      throw new Error('Billing expression nesting is too deep')
    }
    try {
      return parse()
    } finally {
      this.depth -= 1
    }
  }

  private current(): Token {
    return this.tokens[this.index]
  }

  private advance(): Token {
    const token = this.current()
    if (token.type !== 'eof') this.index += 1
    return token
  }

  private check(value: string): boolean {
    return this.current().value === value
  }

  private consume(value: string): boolean {
    if (!this.check(value)) return false
    this.advance()
    return true
  }

  private expect(value: string) {
    if (this.consume(value)) return
    const token = this.current()
    throw new Error(
      `Expected "${value}" at position ${String(token.position + 1)}`
    )
  }
}

class BillingExpressionValidator {
  constructor(private readonly environment: BillingExpressionEnvironment) {}

  validate(expression: ExpressionNode) {
    const result = this.validateNode(expression, 0)
    if (result !== 'number' && result !== 'unknown') {
      throw new Error('Billing expression result must be numeric')
    }
  }

  private validateNode(
    node: ExpressionNode,
    depth: number
  ): ExpressionStaticType {
    if (depth > MAX_EVALUATION_DEPTH) {
      throw new Error('Billing expression is too complex')
    }

    switch (node.type) {
      case 'literal':
        if (typeof node.value === 'number') return 'number'
        if (typeof node.value === 'string') return 'string'
        return 'boolean'
      case 'identifier':
        if (!Object.hasOwn(this.environment, node.name)) {
          throw new Error(`Unknown billing expression variable "${node.name}"`)
        }
        return 'number'
      case 'unary':
        return this.validateUnary(node, depth + 1)
      case 'binary':
        return this.validateBinary(node, depth + 1)
      case 'conditional': {
        const condition = this.validateNode(node.condition, depth + 1)
        const consequent = this.validateNode(node.consequent, depth + 1)
        const alternate = this.validateNode(node.alternate, depth + 1)
        if (condition !== 'boolean' && condition !== 'unknown') {
          throw new Error('Billing expression condition must be boolean')
        }
        return consequent === alternate ? consequent : 'unknown'
      }
      case 'call':
        return this.validateCall(node, depth + 1)
    }
  }

  private validateUnary(node: UnaryNode, depth: number): ExpressionStaticType {
    const operand = this.validateNode(node.operand, depth)
    if (node.operator === '!') {
      if (operand !== 'boolean' && operand !== 'unknown') {
        throw new Error('Billing expression unary "!" requires a boolean')
      }
      return 'boolean'
    }
    if (operand !== 'number' && operand !== 'unknown') {
      throw new Error(
        `Billing expression unary "${node.operator}" requires a number`
      )
    }
    return 'number'
  }

  private validateBinary(
    node: BinaryNode,
    depth: number
  ): ExpressionStaticType {
    const left = this.validateNode(node.left, depth)
    const right = this.validateNode(node.right, depth)

    if (node.operator === '&&' || node.operator === '||') {
      if (
        (left !== 'boolean' && left !== 'unknown') ||
        (right !== 'boolean' && right !== 'unknown')
      ) {
        throw new Error(
          `Billing expression operator "${node.operator}" requires booleans`
        )
      }
      return 'boolean'
    }
    if (node.operator === '==' || node.operator === '!=') {
      if (left !== 'unknown' && right !== 'unknown' && left !== right) {
        throw new Error('Billing expression comparison types do not match')
      }
      return 'boolean'
    }
    if (
      node.operator === '<' ||
      node.operator === '<=' ||
      node.operator === '>' ||
      node.operator === '>='
    ) {
      if (
        left !== 'unknown' &&
        right !== 'unknown' &&
        (left !== right || (left !== 'number' && left !== 'string'))
      ) {
        throw new Error('Billing expression comparison types do not match')
      }
      return 'boolean'
    }
    if (node.operator === '+') {
      if (left === right && (left === 'number' || left === 'string')) {
        return left
      }
      if (left === 'unknown' || right === 'unknown') return 'unknown'
      throw new Error(
        'Billing expression operator "+" requires matching numbers or strings'
      )
    }
    if (
      (left !== 'number' && left !== 'unknown') ||
      (right !== 'number' && right !== 'unknown')
    ) {
      throw new Error(
        `Billing expression operator "${node.operator}" requires numbers`
      )
    }
    return 'number'
  }

  private validateCall(node: CallNode, depth: number): ExpressionStaticType {
    const argumentTypes = node.arguments.map((argument) =>
      this.validateNode(argument, depth)
    )
    if (node.name === 'tier') {
      this.requireArgumentCount(node, argumentTypes, 2)
      if (argumentTypes[0] !== 'string' && argumentTypes[0] !== 'unknown') {
        throw new Error('tier() requires a string name')
      }
      if (argumentTypes[1] !== 'number' && argumentTypes[1] !== 'unknown') {
        throw new Error('tier() requires a numeric value')
      }
      return 'number'
    }
    if (node.name === 'max' || node.name === 'min') {
      this.requireArgumentCount(node, argumentTypes, 2)
      if (
        argumentTypes.some(
          (argument) => argument !== 'number' && argument !== 'unknown'
        )
      ) {
        throw new Error(`${node.name}() requires numeric arguments`)
      }
      return 'number'
    }
    if (node.name === 'abs' || node.name === 'ceil' || node.name === 'floor') {
      this.requireArgumentCount(node, argumentTypes, 1)
      if (argumentTypes[0] !== 'number' && argumentTypes[0] !== 'unknown') {
        throw new Error(`${node.name}() requires a numeric argument`)
      }
      return 'number'
    }
    throw new Error(`Unknown billing expression function "${node.name}"`)
  }

  private requireArgumentCount(
    node: CallNode,
    argumentTypes: ExpressionStaticType[],
    expected: number
  ) {
    if (argumentTypes.length !== expected) {
      throw new Error(
        `${node.name}() requires ${String(expected)} argument${expected === 1 ? '' : 's'}`
      )
    }
  }
}

class BillingExpressionEvaluator {
  private matchedTier = ''

  constructor(private readonly environment: BillingExpressionEnvironment) {}

  evaluate(expression: ExpressionNode): BillingExpressionResult {
    for (const [name, value] of Object.entries(this.environment)) {
      if (!Number.isFinite(value) || value < 0) {
        throw new Error(`Invalid estimator value for "${name}"`)
      }
    }

    const cost = this.expectNumber(this.evaluateNode(expression, 0))
    if (cost < 0) {
      throw new Error('Billing expression produced a negative cost')
    }
    return { cost, matchedTier: this.matchedTier }
  }

  private evaluateNode(node: ExpressionNode, depth: number): ExpressionValue {
    if (depth > MAX_EVALUATION_DEPTH) {
      throw new Error('Billing expression is too complex')
    }

    switch (node.type) {
      case 'literal':
        return node.value
      case 'identifier':
        if (!Object.hasOwn(this.environment, node.name)) {
          throw new Error(`Unknown billing expression variable "${node.name}"`)
        }
        return this.environment[node.name]
      case 'unary':
        return this.evaluateUnary(node, depth + 1)
      case 'binary':
        return this.evaluateBinary(node, depth + 1)
      case 'conditional':
        return this.expectBoolean(this.evaluateNode(node.condition, depth + 1))
          ? this.evaluateNode(node.consequent, depth + 1)
          : this.evaluateNode(node.alternate, depth + 1)
      case 'call':
        return this.evaluateCall(node, depth + 1)
    }
  }

  private evaluateUnary(node: UnaryNode, depth: number): ExpressionValue {
    const operand = this.evaluateNode(node.operand, depth)
    if (node.operator === '!') return !this.expectBoolean(operand)
    const value = this.expectNumber(operand)
    return this.ensureFinite(node.operator === '-' ? -value : value)
  }

  private evaluateBinary(node: BinaryNode, depth: number): ExpressionValue {
    if (node.operator === '&&') {
      const left = this.expectBoolean(this.evaluateNode(node.left, depth))
      return left
        ? this.expectBoolean(this.evaluateNode(node.right, depth))
        : false
    }
    if (node.operator === '||') {
      const left = this.expectBoolean(this.evaluateNode(node.left, depth))
      return left
        ? true
        : this.expectBoolean(this.evaluateNode(node.right, depth))
    }

    const left = this.evaluateNode(node.left, depth)
    const right = this.evaluateNode(node.right, depth)
    if (node.operator === '==') return left === right
    if (node.operator === '!=') return left !== right
    if (
      node.operator === '<' ||
      node.operator === '<=' ||
      node.operator === '>' ||
      node.operator === '>='
    ) {
      return this.compareValues(node.operator, left, right)
    }

    if (node.operator === '+') {
      if (typeof left === 'string' && typeof right === 'string') {
        return left + right
      }
      if (typeof left === 'number' && typeof right === 'number') {
        return this.ensureFinite(left + right)
      }
      throw new Error(
        'Billing expression operator "+" requires matching numbers or strings'
      )
    }

    const leftNumber = this.expectNumber(left)
    const rightNumber = this.expectNumber(right)
    switch (node.operator) {
      case '-':
        return this.ensureFinite(leftNumber - rightNumber)
      case '*':
        return this.ensureFinite(leftNumber * rightNumber)
      case '**':
        return this.ensureFinite(leftNumber ** rightNumber)
      case '/':
        return this.ensureFinite(leftNumber / rightNumber)
      case '%':
        return this.ensureFinite(leftNumber % rightNumber)
    }
  }

  private compareValues(
    operator: '<' | '<=' | '>' | '>=',
    left: ExpressionValue,
    right: ExpressionValue
  ): boolean {
    if (
      (typeof left !== 'number' && typeof left !== 'string') ||
      typeof left !== typeof right
    ) {
      throw new Error('Billing expression comparison types do not match')
    }
    switch (operator) {
      case '<':
        return left < right
      case '<=':
        return left <= right
      case '>':
        return left > right
      case '>=':
        return left >= right
    }
  }

  private evaluateCall(node: CallNode, depth: number): ExpressionValue {
    const argumentsList = node.arguments.map((argument) =>
      this.evaluateNode(argument, depth)
    )
    if (node.name === 'tier') {
      this.requireArgumentCount(node, argumentsList, 2)
      const name = argumentsList[0]
      if (typeof name !== 'string') {
        throw new Error('tier() requires a string name')
      }
      const value = this.expectNumber(argumentsList[1])
      this.matchedTier = name
      return value
    }
    if (node.name === 'max' || node.name === 'min') {
      this.requireArgumentCount(node, argumentsList, 2)
      const left = this.expectNumber(argumentsList[0])
      const right = this.expectNumber(argumentsList[1])
      return node.name === 'max' ? Math.max(left, right) : Math.min(left, right)
    }
    if (node.name === 'abs' || node.name === 'ceil' || node.name === 'floor') {
      this.requireArgumentCount(node, argumentsList, 1)
      const value = this.expectNumber(argumentsList[0])
      if (node.name === 'abs') return Math.abs(value)
      if (node.name === 'ceil') return Math.ceil(value)
      return Math.floor(value)
    }
    throw new Error(`Unknown billing expression function "${node.name}"`)
  }

  private requireArgumentCount(
    node: CallNode,
    argumentsList: ExpressionValue[],
    expected: number
  ) {
    if (argumentsList.length !== expected) {
      throw new Error(
        `${node.name}() requires ${String(expected)} argument${expected === 1 ? '' : 's'}`
      )
    }
  }

  private expectBoolean(value: ExpressionValue): boolean {
    if (typeof value !== 'boolean') {
      throw new Error('Billing expression condition must be boolean')
    }
    return value
  }

  private expectNumber(value: ExpressionValue): number {
    if (typeof value !== 'number') {
      throw new Error('Billing expression result must be numeric')
    }
    return this.ensureFinite(value)
  }

  private ensureFinite(value: number): number {
    if (!Number.isFinite(value)) {
      throw new Error('Billing expression produced a non-finite number')
    }
    return value
  }
}

export function evaluateBillingExpression(
  expression: string,
  environment: BillingExpressionEnvironment
): BillingExpressionResult {
  if (expression.length > MAX_EXPRESSION_LENGTH) {
    throw new Error('Billing expression is too long')
  }

  const body = expression.startsWith('v1:') ? expression.slice(3) : expression
  const tokens = new BillingExpressionTokenizer(body).tokenize()
  const syntaxTree = new BillingExpressionParser(tokens).parse()
  new BillingExpressionValidator(environment).validate(syntaxTree)
  return new BillingExpressionEvaluator(environment).evaluate(syntaxTree)
}
