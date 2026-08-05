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
export const VERTEX_SERVICE_ACCOUNT_MAX_KEYS = 100
export const VERTEX_SERVICE_ACCOUNT_MAX_BYTES = 64 * 1024
export const VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES = 1024 * 1024

export type VertexServiceAccountErrorCode =
  | 'empty_selection'
  | 'too_many_keys'
  | 'key_too_large'
  | 'total_too_large'
  | 'read_failed'
  | 'invalid_json'
  | 'invalid_mode'
  | 'invalid_schema'

export class VertexServiceAccountError extends Error {
  readonly code: VertexServiceAccountErrorCode
  readonly fileName?: string
  readonly limit?: number

  constructor(
    code: VertexServiceAccountErrorCode,
    options: { fileName?: string; limit?: number } = {}
  ) {
    super(code)
    this.name = 'VertexServiceAccountError'
    this.code = code
    this.fileName = options.fileName
    this.limit = options.limit
  }
}

export type VertexServiceAccount = Record<string, unknown> & {
  type: 'service_account'
  project_id: string
  private_key: string
  client_email: string
}

export type VertexServiceAccountFile = {
  name: string
  size: number
  text: () => Promise<string>
}

const textEncoder = new TextEncoder()

function utf8ByteLength(value: string): number {
  return textEncoder.encode(value).byteLength
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0
}

function isVertexServiceAccount(value: unknown): value is VertexServiceAccount {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return false
  }

  const credential = value as Record<string, unknown>
  return (
    credential.type === 'service_account' &&
    isNonEmptyString(credential.project_id) &&
    isNonEmptyString(credential.private_key) &&
    isNonEmptyString(credential.client_email)
  )
}

function normalizeVertexServiceAccount(
  value: unknown,
  fileName?: string
): { credential: VertexServiceAccount; serialized: string; bytes: number } {
  if (!isVertexServiceAccount(value)) {
    throw new VertexServiceAccountError('invalid_schema', { fileName })
  }

  const serialized = JSON.stringify(value)
  const bytes = utf8ByteLength(serialized)
  if (bytes > VERTEX_SERVICE_ACCOUNT_MAX_BYTES) {
    throw new VertexServiceAccountError('key_too_large', {
      fileName,
      limit: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
    })
  }

  return { credential: value, serialized, bytes }
}

function getRawJSONArrayElements(value: string): string[] {
  const trimmed = value.trim()
  const elements: string[] = []
  let elementStart = -1
  let depth = 0
  let inString = false
  let escaped = false

  for (let index = 1; index < trimmed.length - 1; index += 1) {
    const character = trimmed[index]
    if (inString) {
      if (escaped) {
        escaped = false
      } else if (character === '\\') {
        escaped = true
      } else if (character === '"') {
        inString = false
      }
      continue
    }

    if (character === '"') {
      inString = true
    }
    if (elementStart === -1) {
      if (/\s/.test(character)) continue
      elementStart = index
    }

    if (character === '{' || character === '[') {
      depth += 1
    } else if (character === '}' || character === ']') {
      depth -= 1
    } else if (character === ',' && depth === 0) {
      elements.push(trimmed.slice(elementStart, index).trim())
      elementStart = -1
    }
  }

  if (elementStart !== -1) {
    elements.push(trimmed.slice(elementStart, -1).trim())
  }
  return elements
}

export function validateVertexServiceAccountValue(
  value: string,
  options: { batch: boolean; allowSingle?: boolean }
): VertexServiceAccount[] {
  const rawBytes = utf8ByteLength(value)
  if (rawBytes > VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES) {
    throw new VertexServiceAccountError('total_too_large', {
      limit: VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
    })
  }
  if (!options.batch && rawBytes > VERTEX_SERVICE_ACCOUNT_MAX_BYTES) {
    throw new VertexServiceAccountError('key_too_large', {
      limit: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
    })
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    throw new VertexServiceAccountError('invalid_json')
  }

  const arrayInput = Array.isArray(parsed)
  if (
    (!options.batch && arrayInput) ||
    (options.batch && !arrayInput && !options.allowSingle)
  ) {
    throw new VertexServiceAccountError('invalid_mode')
  }
  if (!arrayInput && rawBytes > VERTEX_SERVICE_ACCOUNT_MAX_BYTES) {
    throw new VertexServiceAccountError('key_too_large', {
      limit: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
    })
  }

  const candidates = arrayInput ? (parsed as unknown[]) : [parsed]
  if (candidates.length === 0) {
    throw new VertexServiceAccountError('invalid_schema')
  }
  if (candidates.length > VERTEX_SERVICE_ACCOUNT_MAX_KEYS) {
    throw new VertexServiceAccountError('too_many_keys', {
      limit: VERTEX_SERVICE_ACCOUNT_MAX_KEYS,
    })
  }

  const rawCandidates = arrayInput ? getRawJSONArrayElements(value) : [value]
  const credentials: VertexServiceAccount[] = []
  let normalizedBytes = 0
  for (const [index, candidate] of candidates.entries()) {
    if (
      utf8ByteLength(rawCandidates[index]) > VERTEX_SERVICE_ACCOUNT_MAX_BYTES
    ) {
      throw new VertexServiceAccountError('key_too_large', {
        limit: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
      })
    }
    const normalized = normalizeVertexServiceAccount(candidate)
    normalizedBytes += normalized.bytes
    if (normalizedBytes > VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES) {
      throw new VertexServiceAccountError('total_too_large', {
        limit: VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
      })
    }
    credentials.push(normalized.credential)
  }

  return credentials
}

export async function processVertexServiceAccountFiles(
  files: readonly VertexServiceAccountFile[],
  options: { batch: boolean }
): Promise<{ value: string; count: number }> {
  if (files.length === 0) {
    throw new VertexServiceAccountError('empty_selection')
  }

  const maxKeys = options.batch ? VERTEX_SERVICE_ACCOUNT_MAX_KEYS : 1
  if (files.length > maxKeys) {
    throw new VertexServiceAccountError('too_many_keys', { limit: maxKeys })
  }

  let declaredTotalBytes = 0
  for (const file of files) {
    if (file.size > VERTEX_SERVICE_ACCOUNT_MAX_BYTES) {
      throw new VertexServiceAccountError('key_too_large', {
        fileName: file.name,
        limit: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
      })
    }
    declaredTotalBytes += file.size
    if (declaredTotalBytes > VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES) {
      throw new VertexServiceAccountError('total_too_large', {
        limit: VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
      })
    }
  }

  const credentials: VertexServiceAccount[] = []
  let decodedTotalBytes = 0
  for (const file of files) {
    let text: string
    try {
      text = await file.text()
    } catch {
      throw new VertexServiceAccountError('read_failed', {
        fileName: file.name,
      })
    }

    const decodedBytes = utf8ByteLength(text)
    if (decodedBytes > VERTEX_SERVICE_ACCOUNT_MAX_BYTES) {
      throw new VertexServiceAccountError('key_too_large', {
        fileName: file.name,
        limit: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
      })
    }
    decodedTotalBytes += decodedBytes
    if (decodedTotalBytes > VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES) {
      throw new VertexServiceAccountError('total_too_large', {
        limit: VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
      })
    }

    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      throw new VertexServiceAccountError('invalid_json', {
        fileName: file.name,
      })
    }

    credentials.push(
      normalizeVertexServiceAccount(parsed, file.name).credential
    )
  }

  const value = JSON.stringify(options.batch ? credentials : credentials[0])
  if (utf8ByteLength(value) > VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES) {
    throw new VertexServiceAccountError('total_too_large', {
      limit: VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
    })
  }

  return { value, count: credentials.length }
}
