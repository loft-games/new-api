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
import { CheckCircle2, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

import {
  normalizeCodexAuthJSON,
  type CodexAuthJSONNormalizeResponse,
} from '../../api'

type CodexAuthJSONImportDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImport: (key: string, suggestedChannelName?: string) => void
}

export function CodexAuthJSONImportDialog(
  props: CodexAuthJSONImportDialogProps
) {
  const { t } = useTranslation()
  const [content, setContent] = useState('')
  const [isNormalizing, setIsNormalizing] = useState(false)
  const [normalized, setNormalized] =
    useState<CodexAuthJSONNormalizeResponse['data']>(undefined)

  const reset = () => {
    setContent('')
    setNormalized(undefined)
    setIsNormalizing(false)
  }

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      reset()
    }
    props.onOpenChange(nextOpen)
  }

  const handleNormalize = async () => {
    if (!content.trim()) {
      toast.error(t('Paste auth JSON first'))
      return
    }

    setIsNormalizing(true)
    try {
      const res = await normalizeCodexAuthJSON(content)
      if (!res.success || !res.data) {
        throw new Error(res.message || t('Failed to parse auth JSON'))
      }
      setNormalized(res.data)
      toast.success(t('Auth JSON parsed'))
    } catch (error) {
      setNormalized(undefined)
      toast.error(
        error instanceof Error ? error.message : t('Failed to parse auth JSON')
      )
    } finally {
      setIsNormalizing(false)
    }
  }

  const handleImport = () => {
    if (!normalized) {
      return
    }
    props.onImport(
      JSON.stringify(normalized.key, null, 2),
      normalized.suggested_channel_name
    )
    handleOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleOpenChange}
      title={t('Import Codex Auth JSON')}
      description={t(
        'Paste a new-api, CLIProxyAPI, or sub2api Codex auth JSON file to convert it into the current channel key format.'
      )}
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => handleOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant='outline'
            onClick={handleNormalize}
            disabled={isNormalizing}
          >
            {isNormalizing ? (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            ) : null}
            {t('Parse')}
          </Button>
          <Button type='button' onClick={handleImport} disabled={!normalized}>
            {t('Import')}
          </Button>
        </>
      }
      contentClassName='sm:max-w-3xl'
      contentHeight='min(60vh, 540px)'
    >
      <div className='flex flex-col gap-4'>
        <Textarea
          value={content}
          onChange={(event) => {
            setContent(event.target.value)
            setNormalized(undefined)
          }}
          placeholder={t('Paste Codex auth JSON here')}
          className='min-h-56 font-mono text-xs'
        />

        {normalized ? (
          <div className='space-y-3'>
            <Alert>
              <CheckCircle2 className='h-4 w-4' />
              <AlertDescription>
                {t('Detected format: {{format}}', {
                  format: normalized.format,
                })}
              </AlertDescription>
            </Alert>
            {normalized.suggested_channel_name ? (
              <p className='text-muted-foreground text-xs'>
                {t('Suggested channel name: {{name}}', {
                  name: normalized.suggested_channel_name,
                })}
              </p>
            ) : null}
            {normalized.suggested_proxy_url ? (
              <p className='text-muted-foreground text-xs'>
                {t('Suggested proxy URL: {{url}}', {
                  url: normalized.suggested_proxy_url,
                })}
              </p>
            ) : null}
            {normalized.warnings.length > 0 ? (
              <Alert className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'>
                <AlertDescription>
                  <ul className='list-disc space-y-1 pl-4'>
                    {normalized.warnings.map((warning) => (
                      <li key={warning}>{t(warning)}</li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            ) : null}
          </div>
        ) : null}
      </div>
    </Dialog>
  )
}
