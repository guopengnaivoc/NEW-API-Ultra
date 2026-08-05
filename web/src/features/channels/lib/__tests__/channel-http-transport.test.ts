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
  HTTP_PROTOCOL_AUTO,
  HTTP_PROTOCOL_HTTP1,
  buildSettingJSON,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../channel-form'

function validForm(
  overrides: Partial<ChannelFormValues> = {}
): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'HTTP transport channel',
    models: 'gpt-5',
    ...overrides,
  }
}

function channelWithStoredSettings(options: {
  type?: number
  setting?: string | null
  settings?: string
  isMultiKey?: boolean
}) {
  return channelSchema.parse({
    id: 1,
    type: options.type ?? 1,
    key: '',
    status: 1,
    name: 'Stored HTTP transport channel',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'gpt-5',
    group: 'default',
    other: options.type === 41 ? '{"default":"us-central1"}' : '',
    setting: options.setting,
    settings: options.settings ?? '{}',
    channel_info: {
      is_multi_key: options.isMultiKey ?? false,
      multi_key_size: options.isMultiKey ? 2 : 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
  })
}

describe('channel HTTP transport contract', () => {
  test('rejects an unsupported HTTP protocol', () => {
    const result = channelFormSchema.safeParse({
      ...validForm(),
      http_protocol: 'http2',
    })

    assert.equal(result.success, false)
    assert.equal(
      result.error?.issues.some((issue) => issue.path[0] === 'http_protocol'),
      true
    )
  })

  test('accepts only integer shard counts within the supported bounds', () => {
    for (const shards of [0, 9, 1.5]) {
      const result = channelFormSchema.safeParse({
        ...validForm(),
        http2_connection_shards: shards,
      })

      assert.equal(result.success, false, `expected ${shards} to be rejected`)
      assert.equal(
        result.error?.issues.some(
          (issue) => issue.path[0] === 'http2_connection_shards'
        ),
        true
      )
    }

    for (const shards of [1, 8]) {
      assert.equal(
        channelFormSchema.safeParse({
          ...validForm(),
          http2_connection_shards: shards,
        }).success,
        true,
        `expected ${shards} to be accepted`
      )
    }
  })

  test('requires one shard when HTTP/1 is selected', () => {
    const invalid = channelFormSchema.safeParse({
      ...validForm(),
      http_protocol: HTTP_PROTOCOL_HTTP1,
      http2_connection_shards: 2,
    })
    const valid = channelFormSchema.safeParse({
      ...validForm(),
      http_protocol: HTTP_PROTOCOL_HTTP1,
      http2_connection_shards: 1,
    })

    assert.equal(invalid.success, false)
    assert.equal(
      invalid.error?.issues.some(
        (issue) => issue.path[0] === 'http2_connection_shards'
      ),
      true
    )
    assert.equal(valid.success, true)
  })

  test('forces stored HTTP/1 channels to one shard', () => {
    const defaults = transformChannelToFormDefaults(
      channelWithStoredSettings({
        setting: JSON.stringify({
          http_protocol: HTTP_PROTOCOL_HTTP1,
          http2_connection_shards: 8,
        }),
      })
    )

    assert.equal(defaults.http_protocol, HTTP_PROTOCOL_HTTP1)
    assert.equal(defaults.http2_connection_shards, 1)
  })

  test('falls back safely when stored transport JSON is malformed', () => {
    const defaults = transformChannelToFormDefaults(
      channelWithStoredSettings({ setting: '{' })
    )

    assert.equal(defaults.setting, '{}')
    assert.equal(defaults.http_protocol, HTTP_PROTOCOL_AUTO)
    assert.equal(defaults.http2_connection_shards, 1)
  })

  test('does not coerce wrong stored value types', () => {
    const defaults = transformChannelToFormDefaults(
      channelWithStoredSettings({
        setting: JSON.stringify({
          force_format: 'true',
          thinking_to_content: 1,
          proxy: { url: 'https://proxy.example' },
          http_protocol: ['http1'],
          http2_connection_shards: '8',
          pass_through_body_enabled: 'true',
          system_prompt: 7,
          system_prompt_override: 1,
        }),
      })
    )

    assert.equal(defaults.force_format, false)
    assert.equal(defaults.thinking_to_content, false)
    assert.equal(defaults.proxy, '')
    assert.equal(defaults.http_protocol, HTTP_PROTOCOL_AUTO)
    assert.equal(defaults.http2_connection_shards, 1)
    assert.equal(defaults.pass_through_body_enabled, false)
    assert.equal(defaults.system_prompt, '')
    assert.equal(defaults.system_prompt_override, false)
  })

  test('omits automatic protocol and one default shard when serializing', () => {
    const setting = JSON.parse(
      buildSettingJSON(
        validForm({
          http_protocol: HTTP_PROTOCOL_AUTO,
          http2_connection_shards: 1,
        })
      )
    ) as Record<string, unknown>

    assert.equal('http_protocol' in setting, false)
    assert.equal('http2_connection_shards' in setting, false)
  })

  test('retains non-default shards for automatic protocol', () => {
    const setting = JSON.parse(
      buildSettingJSON(
        validForm({
          http_protocol: HTTP_PROTOCOL_AUTO,
          http2_connection_shards: 4,
        })
      )
    ) as Record<string, unknown>

    assert.equal('http_protocol' in setting, false)
    assert.equal(setting.http2_connection_shards, 4)
  })

  test('serializes HTTP/1 without an incompatible shard count', () => {
    const setting = JSON.parse(
      buildSettingJSON(
        validForm({
          http_protocol: HTTP_PROTOCOL_HTTP1,
          http2_connection_shards: 8,
        })
      )
    ) as Record<string, unknown>

    assert.equal(setting.http_protocol, HTTP_PROTOCOL_HTTP1)
    assert.equal('http2_connection_shards' in setting, false)
  })

  test('keeps unrelated settings and Vertex service-account edit state', () => {
    const defaults = transformChannelToFormDefaults(
      channelWithStoredSettings({
        type: 41,
        isMultiKey: true,
        setting: JSON.stringify({
          force_format: true,
          proxy: 'https://proxy.example',
          http_protocol: HTTP_PROTOCOL_AUTO,
          http2_connection_shards: 3,
          system_prompt: 'Keep this prompt',
          system_prompt_override: true,
        }),
        settings: JSON.stringify({
          vertex_key_type: 'json',
          vendor_metadata: { keep: true },
        }),
      })
    )

    assert.equal(defaults.force_format, true)
    assert.equal(defaults.proxy, 'https://proxy.example')
    assert.equal(defaults.system_prompt, 'Keep this prompt')
    assert.equal(defaults.system_prompt_override, true)
    assert.equal(defaults.http_protocol, HTTP_PROTOCOL_AUTO)
    assert.equal(defaults.http2_connection_shards, 3)
    assert.equal(defaults.vertex_key_type, 'json')
    assert.equal(defaults.has_existing_vertex_service_account_key, true)
    assert.equal(defaults.has_existing_multi_key, true)
    assert.equal(defaults.multi_key_mode, 'multi_to_single')
    assert.equal(channelFormSchema.safeParse(defaults).success, true)

    const payload = transformFormDataToUpdatePayload(defaults, 1)
    const setting = JSON.parse(payload.setting || '{}') as Record<
      string,
      unknown
    >
    const settings = JSON.parse(payload.settings || '{}') as Record<
      string,
      unknown
    >

    assert.equal(setting.force_format, true)
    assert.equal(setting.proxy, 'https://proxy.example')
    assert.equal(setting.system_prompt, 'Keep this prompt')
    assert.equal(setting.system_prompt_override, true)
    assert.equal(setting.http2_connection_shards, 3)
    assert.deepEqual(settings.vendor_metadata, { keep: true })
    assert.equal(settings.vertex_key_type, 'json')
  })
})
