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
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'

import {
  exportCodexAuthJSON,
  type CodexAuthJSONFormat,
  type CodexAuthJSONExportResponse,
} from '../../api'

type CodexAuthJSONExportDialogProps = {
  channelId: number
  format: CodexAuthJSONFormat
  onDownloaded: (filename: string, content: unknown) => void
  onOpenChange: (open: boolean) => void
}

export function CodexAuthJSONExportDialog(
  props: CodexAuthJSONExportDialogProps
) {
  const { t } = useTranslation()
  const startedRef = useRef(false)
  const channelId = props.channelId
  const format = props.format
  const onDownloaded = props.onDownloaded
  const onOpenChange = props.onOpenChange
  const {
    open,
    methods,
    state,
    executeVerification,
    withVerification,
    cancel,
    setCode,
    switchMethod,
  } = useSecureVerification()

  useEffect(() => {
    if (startedRef.current) {
      return
    }
    startedRef.current = true

    const runExport = async (proofToken?: string) => {
      const res: CodexAuthJSONExportResponse = await exportCodexAuthJSON(
        channelId,
        format,
        proofToken
      )
      if (!res.success || !res.data) {
        throw new Error(res.message || t('Failed to export auth JSON'))
      }
      onDownloaded(res.data.filename, res.data.content)
      onOpenChange(false)
      return res
    }

    withVerification(runExport, {
      scope: 'channel.key.read',
      preferredMethod: 'passkey',
      title: t('Verify to export Codex auth JSON'),
      description: t(
        'Use Passkey or 2FA to confirm your identity before exporting this channel credential.'
      ),
    }).catch((error: unknown) => {
      toast.error(
        error instanceof Error ? error.message : t('Failed to export auth JSON')
      )
      onOpenChange(false)
    })
  }, [
    channelId,
    format,
    onDownloaded,
    onOpenChange,
    t,
    withVerification,
  ])

  return (
    <SecureVerificationDialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) {
          cancel()
          onOpenChange(false)
        }
      }}
      methods={methods}
      state={state}
      onVerify={async (method, code) => {
        await executeVerification(method, code)
      }}
      onCancel={() => {
        cancel()
        onOpenChange(false)
      }}
      onCodeChange={setCode}
      onMethodChange={switchMethod}
    />
  )
}
