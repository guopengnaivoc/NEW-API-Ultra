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

import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  getVertexCredentialPresentation,
  getVertexCredentialSynchronization,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from '../channel-form'
import {
  VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
  VERTEX_SERVICE_ACCOUNT_MAX_KEYS,
  VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
  VertexServiceAccountError,
  processVertexServiceAccountFiles,
} from '../vertex-service-account'

type ControlledFile = {
  name: string
  size: number
  text: () => Promise<string>
}

const encoder = new TextEncoder()

function credential(
  suffix: string,
  extra: Record<string, unknown> = {}
): Record<string, unknown> {
  return {
    type: 'service_account',
    project_id: `project-${suffix}`,
    private_key: `private-${suffix}`,
    client_email: `account-${suffix}@example.com`,
    ...extra,
  }
}

function fileFromText(
  name: string,
  text: string,
  onRead: () => void = () => {}
): ControlledFile {
  return {
    name,
    size: encoder.encode(text).byteLength,
    async text() {
      onRead()
      return text
    },
  }
}

async function expectFailure(
  promise: Promise<unknown>,
  code: VertexServiceAccountError['code']
): Promise<VertexServiceAccountError> {
  try {
    await promise
  } catch (error) {
    assert.ok(error instanceof VertexServiceAccountError)
    assert.equal(error.code, code)
    return error
  }
  assert.fail(`expected ${code} failure`)
}

describe('Vertex service account file processing', () => {
  test('publishes the fixed domain limits', () => {
    assert.equal(VERTEX_SERVICE_ACCOUNT_MAX_KEYS, 100)
    assert.equal(VERTEX_SERVICE_ACCOUNT_MAX_BYTES, 64 * 1024)
    assert.equal(VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES, 1024 * 1024)
  })

  test('rejects an excessive batch count before reading any file', async () => {
    let reads = 0
    const files = Array.from(
      { length: VERTEX_SERVICE_ACCOUNT_MAX_KEYS + 1 },
      (_, index) =>
        fileFromText(
          `${index}.json`,
          JSON.stringify(credential(String(index))),
          () => {
            reads += 1
          }
        )
    )

    await expectFailure(
      processVertexServiceAccountFiles(files, { batch: true }),
      'too_many_keys'
    )
    assert.equal(reads, 0)
  })

  test('rejects more than one file in single mode before reading', async () => {
    let reads = 0
    const files = ['a', 'b'].map((suffix) =>
      fileFromText(`${suffix}.json`, JSON.stringify(credential(suffix)), () => {
        reads += 1
      })
    )

    await expectFailure(
      processVertexServiceAccountFiles(files, { batch: false }),
      'too_many_keys'
    )
    assert.equal(reads, 0)
  })

  test('rejects an oversized declared file before reading any file', async () => {
    let reads = 0
    const oversized: ControlledFile = {
      name: 'oversized.json',
      size: VERTEX_SERVICE_ACCOUNT_MAX_BYTES + 1,
      async text() {
        reads += 1
        return JSON.stringify(credential('oversized'))
      },
    }

    await expectFailure(
      processVertexServiceAccountFiles([oversized], { batch: true }),
      'key_too_large'
    )
    assert.equal(reads, 0)
  })

  test('rejects excessive declared aggregate size before reading any file', async () => {
    let reads = 0
    const files = Array.from({ length: 17 }, (_, index) => ({
      name: `${index}.json`,
      size: VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
      async text() {
        reads += 1
        return JSON.stringify(credential(String(index)))
      },
    }))

    await expectFailure(
      processVertexServiceAccountFiles(files, { batch: true }),
      'total_too_large'
    )
    assert.equal(reads, 0)
  })

  test('reads valid files sequentially', async () => {
    let resolveFirst!: (value: string) => void
    let resolveSecond!: (value: string) => void
    const reads: string[] = []
    const firstText = JSON.stringify(credential('first'))
    const secondText = JSON.stringify(credential('second'))
    const files: ControlledFile[] = [
      {
        name: 'first.json',
        size: encoder.encode(firstText).byteLength,
        text() {
          reads.push('first')
          return new Promise((resolve) => {
            resolveFirst = resolve
          })
        },
      },
      {
        name: 'second.json',
        size: encoder.encode(secondText).byteLength,
        text() {
          reads.push('second')
          return new Promise((resolve) => {
            resolveSecond = resolve
          })
        },
      },
    ]

    const processing = processVertexServiceAccountFiles(files, { batch: true })
    await Promise.resolve()
    assert.deepEqual(reads, ['first'])

    resolveFirst(firstText)
    await Promise.resolve()
    await Promise.resolve()
    assert.deepEqual(reads, ['first', 'second'])

    resolveSecond(secondText)
    const result = await processing
    assert.equal(
      result.value,
      JSON.stringify([credential('first'), credential('second')])
    )
  })

  test('rechecks decoded UTF-8 bytes after reading', async () => {
    const padding = '界'.repeat(Math.ceil(VERTEX_SERVICE_ACCOUNT_MAX_BYTES / 3))
    const text = JSON.stringify(credential('decoded', { padding }))
    const file: ControlledFile = {
      name: 'decoded.json',
      size: 1,
      async text() {
        return text
      },
    }

    await expectFailure(
      processVertexServiceAccountFiles([file], { batch: false }),
      'key_too_large'
    )
  })

  test('rejects decoded aggregate size beyond 1 MiB when every file is individually valid', async () => {
    const files = Array.from({ length: 20 }, (_, index) => {
      const text = JSON.stringify(
        credential(String(index), { padding: 'x'.repeat(54 * 1024) })
      )
      assert.ok(
        encoder.encode(text).byteLength < VERTEX_SERVICE_ACCOUNT_MAX_BYTES
      )
      return {
        name: `${index}.json`,
        size: 1,
        async text() {
          return text
        },
      }
    })

    const error = await expectFailure(
      processVertexServiceAccountFiles(files, { batch: true }),
      'total_too_large'
    )
    assert.equal(error.message, 'total_too_large')
  })

  test('rejects malformed JSON and invalid service-account schemas', async () => {
    await expectFailure(
      processVertexServiceAccountFiles(
        [fileFromText('malformed.json', '{"type":')],
        { batch: false }
      ),
      'invalid_json'
    )

    for (const [name, value] of [
      ['array', [credential('nested')]],
      ['primitive', 'secret'],
      ['wrong-type', { ...credential('wrong'), type: 'authorized_user' }],
      ['missing-project', { ...credential('missing'), project_id: undefined }],
      ['blank-key', { ...credential('blank'), private_key: '   ' }],
      ['blank-email', { ...credential('email'), client_email: '' }],
    ] as const) {
      await expectFailure(
        processVertexServiceAccountFiles(
          [fileFromText(`${name}.json`, JSON.stringify(value))],
          { batch: false }
        ),
        'invalid_schema'
      )
    }
  })

  test('preserves additional fields in valid single and batch payloads', async () => {
    const first = credential('one', {
      token_uri: 'https://oauth2.googleapis.com/token',
      universe_domain: 'googleapis.com',
    })
    const second = credential('two', { private_key_id: 'key-id' })

    const single = await processVertexServiceAccountFiles(
      [fileFromText('one.json', JSON.stringify(first))],
      { batch: false }
    )
    assert.equal(single.value, JSON.stringify(first))

    const batch = await processVertexServiceAccountFiles(
      [
        fileFromText('one.json', JSON.stringify(first)),
        fileFromText('two.json', JSON.stringify(second)),
      ],
      { batch: true }
    )
    assert.equal(batch.value, JSON.stringify([first, second]))
  })
})

function vertexForm(
  key: string,
  multiKeyMode: 'single' | 'batch' | 'multi_to_single' = 'single',
  hasExistingVertexServiceAccountKey = false
) {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Vertex',
    type: 41,
    key,
    models: 'gemini-2.5-pro',
    other: 'us-central1',
    vertex_key_type: 'json' as const,
    multi_key_mode: multiKeyMode,
    has_existing_vertex_service_account_key: hasExistingVertexServiceAccountKey,
  }
}

function vertexMultiKeyEditDefaults(
  vertexKeyType: 'json' | 'api_key',
  options: { setting?: string; settings?: string } = {}
) {
  const channel = channelSchema.parse({
    id: 1,
    type: 41,
    key: '',
    status: 1,
    name: 'Vertex multi-key',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'gemini-2.5-pro',
    group: 'default',
    other: '{"default":"us-central1"}',
    setting: options.setting,
    settings:
      options.settings ?? JSON.stringify({ vertex_key_type: vertexKeyType }),
    channel_info: {
      is_multi_key: true,
      multi_key_size: 2,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
  })
  return transformChannelToFormDefaults(channel)
}

describe('Vertex manual credential validation', () => {
  test('repairs malformed stored settings and treats an unknown Vertex mode as service accounts', () => {
    const defaults = vertexMultiKeyEditDefaults('json', {
      setting: '{',
      settings: JSON.stringify({ vertex_key_type: 'legacy-mode' }),
    })

    assert.equal(defaults.setting, '{}')
    assert.equal(
      defaults.settings,
      JSON.stringify({ vertex_key_type: 'legacy-mode' })
    )
    assert.equal(defaults.vertex_key_type, 'json')
    assert.equal(defaults.has_existing_vertex_service_account_key, true)
    assert.equal(channelFormSchema.safeParse(defaults).success, true)

    const payload = transformFormDataToUpdatePayload(defaults, 1)
    assert.deepEqual(JSON.parse(payload.setting || '{}'), {
      force_format: false,
      thinking_to_content: false,
      proxy: '',
      pass_through_body_enabled: false,
      system_prompt: '',
      system_prompt_override: false,
    })
    assert.equal(JSON.parse(payload.settings || '{}').vertex_key_type, 'json')
  })

  test('repairs null stored settings before opening the editor', () => {
    const defaults = vertexMultiKeyEditDefaults('json', {
      setting: 'null',
      settings: 'null',
    })

    assert.equal(defaults.setting, '{}')
    assert.equal(defaults.settings, '{}')
    assert.equal(channelFormSchema.safeParse(defaults).success, true)
  })

  test('repairs malformed stored type-specific settings before opening the editor', () => {
    const defaults = vertexMultiKeyEditDefaults('json', { settings: '{' })

    assert.equal(defaults.settings, '{}')
    assert.equal(defaults.vertex_key_type, 'json')
    assert.equal(channelFormSchema.safeParse(defaults).success, true)
  })

  test('uses the target API-key presentation and replacement payload when leaving service-account multi-key mode', () => {
    const defaults = vertexMultiKeyEditDefaults('json')
    assert.equal(defaults.has_existing_multi_key, true)
    const presentation = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'api_key',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: true,
    })

    assert.equal(presentation.multiKeyMode, 'single')
    assert.equal(presentation.requiresReplacement, true)

    const result = channelFormSchema.safeParse({
      ...defaults,
      vertex_key_type: 'api_key',
      multi_key_mode: presentation.multiKeyMode,
      key_mode: presentation.requiresReplacement ? 'replace' : 'append',
      key: 'replacement-api-key',
    })

    assert.ok(result.success)
    const payload = transformFormDataToUpdatePayload(result.data, 1)
    assert.equal(payload.key, 'replacement-api-key')
    assert.equal(
      JSON.parse(payload.settings || '{}').vertex_key_type,
      'api_key'
    )
  })

  test('uses single-key presentation for a non-Vertex target and owns the forced replace mode', () => {
    const transitionOptions = {
      channelType: 1,
      targetVertexKeyType: 'json' as const,
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: true,
      currentKeyMode: 'append' as const,
      forcedRestoreKeyMode: null,
    }

    const transition = getVertexCredentialPresentation(transitionOptions)

    assert.equal(transition.multiKeyMode, 'single')
    assert.equal(transition.keyMode, 'replace')
    assert.equal(transition.forcedRestoreKeyMode, 'append')
  })

  test('rejects multiple replacement API keys when leaving service-account multi-key mode', () => {
    const defaults = vertexMultiKeyEditDefaults('json')
    const result = channelFormSchema.safeParse({
      ...defaults,
      type: 1,
      models: 'gpt-4o',
      other: '',
      multi_key_mode: 'single',
      key_mode: 'replace',
      key: 'sk-first\nsk-second',
    })

    assert.equal(result.success, false)
    assert.equal(
      result.error?.issues.find((issue) => issue.path[0] === 'key')?.message,
      'A single replacement key is required when leaving Vertex AI service-account mode'
    )
  })

  test('refuses to build a multi-line single-target replacement payload', () => {
    const defaults = vertexMultiKeyEditDefaults('json')

    assert.throws(
      () =>
        transformFormDataToUpdatePayload(
          {
            ...defaults,
            type: 1,
            models: 'gpt-4o',
            other: '',
            multi_key_mode: 'single',
            key_mode: 'replace',
            key: 'sk-first\nsk-second',
          },
          1
        ),
      /single replacement key/
    )
  })

  test('keeps multiline JSON valid in service-account mode', () => {
    const value = JSON.stringify(credential('pretty'), null, 2)
    assert.ok(value.includes('\n'))

    const result = channelFormSchema.safeParse(vertexForm(value))
    assert.equal(result.success, true)
  })

  test('uses the target service-account presentation and replacement payload when entering from API-key multi-key mode', () => {
    const defaults = vertexMultiKeyEditDefaults('api_key')
    assert.equal(defaults.has_existing_multi_key, true)
    const presentation = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
    })

    assert.equal(presentation.multiKeyMode, 'multi_to_single')
    assert.equal(presentation.requiresReplacement, true)

    const result = channelFormSchema.safeParse({
      ...defaults,
      vertex_key_type: 'json',
      multi_key_mode: presentation.multiKeyMode,
      key_mode: presentation.requiresReplacement ? 'replace' : 'append',
      key: JSON.stringify([credential('replacement')]),
    })

    assert.ok(result.success)
    const payload = transformFormDataToUpdatePayload(result.data, 1)
    assert.equal(JSON.parse(payload.settings || '{}').vertex_key_type, 'json')
    assert.deepEqual(JSON.parse(payload.key || ''), [credential('replacement')])
  })

  test('restores append after an automatically forced JSON to API to JSON round trip', () => {
    const toApi = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'api_key',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: true,
      currentKeyMode: 'append',
      forcedRestoreKeyMode: null,
    })
    const backToJson = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: true,
      currentKeyMode: toApi.keyMode,
      forcedRestoreKeyMode: toApi.forcedRestoreKeyMode,
    })

    assert.equal(toApi.keyMode, 'replace')
    assert.equal(toApi.forcedRestoreKeyMode, 'append')
    assert.equal(backToJson.keyMode, 'append')
    assert.equal(backToJson.forcedRestoreKeyMode, null)
  })

  test('restores append after an automatically forced API to JSON to API round trip', () => {
    const toJson = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
      currentKeyMode: 'append',
      forcedRestoreKeyMode: null,
    })
    const backToApi = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'api_key',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
      currentKeyMode: toJson.keyMode,
      forcedRestoreKeyMode: toJson.forcedRestoreKeyMode,
    })

    assert.equal(toJson.keyMode, 'replace')
    assert.equal(toJson.forcedRestoreKeyMode, 'append')
    assert.equal(backToApi.keyMode, 'append')
    assert.equal(backToApi.forcedRestoreKeyMode, null)
  })

  test('preserves an explicit replace selection across a compatible round trip', () => {
    const toApi = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'api_key',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: true,
      currentKeyMode: 'replace',
      forcedRestoreKeyMode: null,
    })
    const backToJson = getVertexCredentialPresentation({
      channelType: 41,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: true,
      currentKeyMode: toApi.keyMode,
      forcedRestoreKeyMode: toApi.forcedRestoreKeyMode,
    })

    assert.equal(toApi.forcedRestoreKeyMode, null)
    assert.equal(backToJson.keyMode, 'replace')
    assert.equal(backToJson.forcedRestoreKeyMode, null)
  })

  test('restores non-Vertex form state after abandoning a service-account transition', () => {
    const toServiceAccount = getVertexCredentialSynchronization({
      isEditing: true,
      sourceChannelType: 1,
      channelType: 41,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
      multiKeyMode: 'single',
      keyMode: 'append',
      forcedRestoreKeyMode: null,
    })
    const backToSource = getVertexCredentialSynchronization({
      isEditing: true,
      sourceChannelType: 1,
      channelType: 1,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
      multiKeyMode: toServiceAccount.multiKeyMode,
      keyMode: toServiceAccount.keyMode,
      forcedRestoreKeyMode: toServiceAccount.forcedRestoreKeyMode ?? null,
    })

    assert.equal(toServiceAccount.shouldSynchronize, true)
    assert.deepEqual(
      {
        multiKeyMode: toServiceAccount.multiKeyMode,
        keyMode: toServiceAccount.keyMode,
        forcedRestoreKeyMode: toServiceAccount.forcedRestoreKeyMode,
      },
      {
        multiKeyMode: 'multi_to_single',
        keyMode: 'replace',
        forcedRestoreKeyMode: 'append',
      }
    )
    assert.equal(backToSource.shouldSynchronize, true)
    assert.deepEqual(
      {
        multiKeyMode: backToSource.multiKeyMode,
        keyMode: backToSource.keyMode,
        forcedRestoreKeyMode: backToSource.forcedRestoreKeyMode,
      },
      {
        multiKeyMode: 'single',
        keyMode: 'append',
        forcedRestoreKeyMode: null,
      }
    )
  })

  test('restores the single presentation without changing an explicit replace selection', () => {
    const toServiceAccount = getVertexCredentialSynchronization({
      isEditing: true,
      sourceChannelType: 1,
      channelType: 41,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
      multiKeyMode: 'single',
      keyMode: 'replace',
      forcedRestoreKeyMode: null,
    })
    const backToSource = getVertexCredentialSynchronization({
      isEditing: true,
      sourceChannelType: 1,
      channelType: 1,
      targetVertexKeyType: 'json',
      hasExistingMultiKey: true,
      hasExistingVertexServiceAccountKey: false,
      multiKeyMode: toServiceAccount.multiKeyMode,
      keyMode: toServiceAccount.keyMode,
      forcedRestoreKeyMode: toServiceAccount.forcedRestoreKeyMode ?? null,
    })

    assert.equal(toServiceAccount.forcedRestoreKeyMode, null)
    assert.equal(backToSource.shouldSynchronize, true)
    assert.deepEqual(
      {
        multiKeyMode: backToSource.multiKeyMode,
        keyMode: backToSource.keyMode,
      },
      {
        multiKeyMode: 'single',
        keyMode: 'replace',
      }
    )
  })

  test('requires replacement instead of appending API keys when entering service-account multi-key mode', () => {
    const defaults = vertexMultiKeyEditDefaults('api_key')
    const result = channelFormSchema.safeParse({
      ...defaults,
      vertex_key_type: 'json',
      multi_key_mode: 'multi_to_single',
      key_mode: 'append',
      key: JSON.stringify([credential('replacement')]),
    })

    assert.equal(result.success, false)
    assert.equal(
      result.error?.issues.find((issue) => issue.path[0] === 'key_mode')
        ?.message,
      'Replace mode is required when leaving Vertex AI service-account mode'
    )
  })

  test('accepts an array when editing an existing multi-key channel', () => {
    const editDefaults = vertexMultiKeyEditDefaults('json')

    assert.equal(editDefaults.multi_key_mode, 'multi_to_single')
    const result = channelFormSchema.safeParse({
      ...editDefaults,
      key: JSON.stringify([
        credential('edit-first'),
        credential('edit-second'),
      ]),
    })
    assert.equal(result.success, true)
  })

  test('accepts one object when editing an existing multi-key channel', () => {
    const editDefaults = vertexMultiKeyEditDefaults('json')

    const result = channelFormSchema.safeParse({
      ...editDefaults,
      key: JSON.stringify(credential('edit-single')),
    })
    assert.equal(result.success, true)
  })

  test('rejects one object during multi-key channel creation', () => {
    const result = channelFormSchema.safeParse(
      vertexForm(JSON.stringify(credential('create-single')), 'multi_to_single')
    )
    assert.equal(result.success, false)
  })

  test('allows an existing multi-key edit to replace service accounts with an API key', () => {
    const editDefaults = vertexMultiKeyEditDefaults('json')

    const result = channelFormSchema.safeParse({
      ...editDefaults,
      vertex_key_type: 'api_key',
      key: 'replacement-api-key',
      key_mode: 'replace',
    })

    assert.ok(result.success)
    const payload = transformFormDataToUpdatePayload(result.data, 1)
    assert.equal(payload.key, 'replacement-api-key')
    assert.equal(
      JSON.parse(payload.settings || '{}').vertex_key_type,
      'api_key'
    )
  })

  test('requires a replacement key and replace mode for the API-key transition', () => {
    const editDefaults = vertexMultiKeyEditDefaults('json')
    const missingKey = channelFormSchema.safeParse({
      ...editDefaults,
      vertex_key_type: 'api_key',
      key: '',
      key_mode: 'replace',
    })
    const appendMode = channelFormSchema.safeParse({
      ...editDefaults,
      vertex_key_type: 'api_key',
      key: 'replacement-api-key',
      key_mode: 'append',
    })

    assert.equal(missingKey.success, false)
    assert.equal(appendMode.success, false)
  })

  test('requires an explicit replacement when changing to a non-Vertex provider', () => {
    const editDefaults = vertexMultiKeyEditDefaults('json')
    const missingKey = channelFormSchema.safeParse({
      ...editDefaults,
      type: 1,
      models: 'gpt-4o',
      other: '',
      key: '',
      key_mode: 'replace',
    })
    const appendMode = channelFormSchema.safeParse({
      ...editDefaults,
      type: 1,
      models: 'gpt-4o',
      other: '',
      key: 'sk-openai-replacement',
      key_mode: 'append',
    })

    assert.equal(missingKey.success, false)
    assert.equal(appendMode.success, false)
  })

  test('normalizes a service-account edit into a non-Vertex replacement payload', () => {
    const editDefaults = vertexMultiKeyEditDefaults('json')
    const result = channelFormSchema.safeParse({
      ...editDefaults,
      type: 1,
      models: 'gpt-4o',
      other: '',
      key: 'sk-openai-replacement',
      key_mode: 'replace',
    })

    assert.ok(result.success)
    const payload = transformFormDataToUpdatePayload(result.data, 1)
    assert.equal(payload.type, 1)
    assert.equal(payload.key, 'sk-openai-replacement')
    assert.equal(
      'vertex_key_type' in JSON.parse(payload.settings || '{}'),
      false
    )
  })

  test('keeps an existing multi-key API-key channel in single form mode', () => {
    const editDefaults = vertexMultiKeyEditDefaults('api_key')

    assert.equal(editDefaults.multi_key_mode, 'single')
    assert.equal(channelFormSchema.safeParse(editDefaults).success, true)
  })

  test('requires a replacement when entering service-account mode', () => {
    const transition = channelFormSchema.safeParse(vertexForm(''))
    assert.equal(transition.success, false)

    const existingServiceAccount = channelFormSchema.safeParse(
      vertexForm('', 'single', true)
    )
    assert.equal(existingServiceAccount.success, true)
  })

  test('uses the service-account schema for manually pasted JSON', () => {
    const invalid = channelFormSchema.safeParse(
      vertexForm(JSON.stringify({ arbitrary: 'object' }))
    )
    assert.equal(invalid.success, false)

    const validWithExtras = channelFormSchema.safeParse(
      vertexForm(
        JSON.stringify(
          credential('manual', {
            auth_uri: 'https://accounts.google.com/o/oauth2/auth',
          })
        )
      )
    )
    assert.equal(validWithExtras.success, true)
  })

  test('enforces the same per-key byte limit for manually pasted JSON', () => {
    const oversized = JSON.stringify(
      credential('manual-size', {
        padding: 'x'.repeat(VERTEX_SERVICE_ACCOUNT_MAX_BYTES),
      })
    )

    const result = channelFormSchema.safeParse(vertexForm(oversized))
    assert.equal(result.success, false)
  })

  test('counts raw JSON whitespace toward the manual per-key limit', () => {
    const key = JSON.stringify(credential('manual-whitespace'))
    const padded = key.replace(
      '{',
      `{${' '.repeat(VERTEX_SERVICE_ACCOUNT_MAX_BYTES)}`
    )
    assert.ok(
      encoder.encode(padded).byteLength > VERTEX_SERVICE_ACCOUNT_MAX_BYTES
    )

    const result = channelFormSchema.safeParse(vertexForm(padded))
    assert.equal(result.success, false)
  })

  test('rejects an array in single mode', () => {
    const result = channelFormSchema.safeParse(
      vertexForm(JSON.stringify([credential('single-array')]))
    )
    assert.equal(result.success, false)
  })

  test('accepts an array in batch mode', () => {
    const result = channelFormSchema.safeParse(
      vertexForm(JSON.stringify([credential('batch-array')]), 'batch')
    )
    assert.equal(result.success, true)
  })

  test('counts raw element whitespace toward the batch per-key limit', () => {
    const key = JSON.stringify(credential('batch-whitespace'))
    const paddedKey = key.replace(
      '{',
      `{${' '.repeat(VERTEX_SERVICE_ACCOUNT_MAX_BYTES)}`
    )
    const result = channelFormSchema.safeParse(
      vertexForm(`[${paddedKey}]`, 'batch')
    )
    assert.equal(result.success, false)
  })

  test('enforces the same batch count limit for manually pasted JSON', () => {
    const keys = Array.from(
      { length: VERTEX_SERVICE_ACCOUNT_MAX_KEYS + 1 },
      (_, index) => credential(String(index))
    )

    const result = channelFormSchema.safeParse(
      vertexForm(JSON.stringify(keys), 'batch')
    )
    assert.equal(result.success, false)
  })

  test('rejects a pasted batch above 1 MiB when every credential is individually valid', () => {
    const keys = Array.from({ length: 20 }, (_, index) =>
      credential(String(index), { padding: 'x'.repeat(54 * 1024) })
    )
    const value = JSON.stringify(keys)
    assert.ok(
      encoder.encode(value).byteLength > VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES
    )
    for (const key of keys) {
      assert.ok(
        encoder.encode(JSON.stringify(key)).byteLength <
          VERTEX_SERVICE_ACCOUNT_MAX_BYTES
      )
    }

    const result = channelFormSchema.safeParse(vertexForm(value, 'batch'))
    assert.equal(result.success, false)
    assert.equal(
      result.error?.issues.find((issue) => issue.path[0] === 'key')?.message,
      'Vertex AI service account keys exceed the 1 MiB total limit'
    )
  })
})
