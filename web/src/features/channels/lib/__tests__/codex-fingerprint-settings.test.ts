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

import { CHANNEL_TYPE_CODEX } from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  transformChannelToFormDefaults,
} from '../channel-form'
import type { Channel } from '../../types'

function channelWithSetting(type: number, setting: string): Channel {
  return {
    id: 1,
    type,
    key: '',
    openai_organization: null,
    test_model: '',
    status: 1,
    name: 'test',
    weight: 0,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    base_url: '',
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models: 'gpt-5-codex',
    group: 'default',
    used_quota: 0,
    model_mapping: '',
    status_code_mapping: '',
    priority: 0,
    auto_ban: 1,
    other_info: '',
    tag: '',
    setting,
    param_override: '',
    header_override: '',
    remark: '',
    max_input_tokens: 0,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '{}',
  }
}

describe('Codex fingerprint channel settings', () => {
  test('defaults old Codex settings to session mode', () => {
    const defaults = transformChannelToFormDefaults(
      channelWithSetting(CHANNEL_TYPE_CODEX, '{}')
    )

    assert.equal(defaults.codex_fingerprint_mode, 'session')
    assert.equal(defaults.codex_device_id, '')
  })

  test('serializes Codex fingerprint settings only for Codex channels', () => {
    const codexSetting = JSON.parse(
      buildSettingJSON({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        type: CHANNEL_TYPE_CODEX,
        codex_fingerprint_mode: 'full',
        codex_device_id: 'device-123',
      })
    )

    assert.equal(codexSetting.codex_fingerprint_mode, 'full')
    assert.equal(codexSetting.codex_device_id, 'device-123')

    const openAISetting = JSON.parse(
      buildSettingJSON({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        type: 1,
        codex_fingerprint_mode: 'full',
        codex_device_id: 'device-123',
      })
    )

    assert.equal('codex_fingerprint_mode' in openAISetting, false)
    assert.equal('codex_device_id' in openAISetting, false)
  })
})
