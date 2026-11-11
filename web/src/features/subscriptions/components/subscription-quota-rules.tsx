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
import { useTranslation } from 'react-i18next'

import { Progress } from '@/components/ui/progress'
import { formatQuota } from '@/lib/format'

import { formatResetPeriod, formatTimestamp } from '../lib/format'
import type { SubscriptionQuotaRule, UserSubscriptionQuotaRule } from '../types'

export function SubscriptionQuotaRules(props: {
  rules: SubscriptionQuotaRule[]
}) {
  const { t } = useTranslation()
  return (
    <div className='space-y-1 text-sm'>
      {props.rules.map((rule, i) => (
        <div key={rule.id ?? i}>
          {formatResetPeriod(rule, t)}:{' '}
          {rule.total_amount > 0
            ? formatQuota(rule.total_amount)
            : t('Unlimited')}
        </div>
      ))}
    </div>
  )
}

export function SubscriptionQuotaUsage(props: {
  rules: UserSubscriptionQuotaRule[]
}) {
  const { t } = useTranslation()
  return (
    <div className='space-y-3 text-sm'>
      {props.rules.map((rule, i) => (
        <div key={rule.id ?? i}>
          <div>
            {formatResetPeriod(rule, t)}:{' '}
            {rule.total_amount > 0
              ? `${formatQuota(rule.amount_used)}/${formatQuota(rule.total_amount)} · ${t('Remaining')} ${formatQuota(Math.max(0, rule.total_amount - rule.amount_used))}`
              : `${t('Unlimited')} · ${t('Used')} ${formatQuota(rule.amount_used)}`}
          </div>
          {rule.next_reset_time > 0 && (
            <div className='text-muted-foreground'>
              {t('Next reset')}: {formatTimestamp(rule.next_reset_time)}
            </div>
          )}
          {rule.total_amount > 0 && (
            <Progress
              aria-label={formatResetPeriod(rule, t)}
              value={Math.min(
                100,
                Math.max(0, (rule.amount_used / rule.total_amount) * 100)
              )}
              className='mt-1 h-1.5'
            />
          )}
        </div>
      ))}
    </div>
  )
}
