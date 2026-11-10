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
import { useFieldArray, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getCurrencyLabel } from '@/lib/currency'

import { getResetPeriodOptions } from '../constants'
import type { PlanFormValues } from '../lib/plan-form'

export function SubscriptionQuotaRulesEditor(props: {
  form: UseFormReturn<PlanFormValues>
}) {
  const { t } = useTranslation()
  const rules = useFieldArray({
    control: props.form.control,
    name: 'quota_rules',
  })
  const values = props.form.watch('quota_rules')
  return (
    <div className='space-y-4'>
      <FormDescription>
        {t('Every quota rule must have sufficient remaining quota')}
      </FormDescription>
      {rules.fields.map((rule, index) => (
        <div key={rule.id} className='space-y-3 rounded-md border p-3'>
          <div className='flex items-center justify-between gap-2'>
            <span className='text-sm font-medium'>
              {t('Quota rule {{number}}', { number: index + 1 })}
            </span>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={rules.fields.length === 1}
              aria-label={t('Remove quota rule {{number}}', {
                number: index + 1,
              })}
              onClick={() => rules.remove(index)}
            >
              {t('Delete')}
            </Button>
          </div>
          <FormField
            control={props.form.control}
            name={`quota_rules.${index}.total_amount`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Quota ({{currency}})', { currency: getCurrencyLabel() })}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='number'
                    min={0}
                    step='any'
                    onChange={(e) =>
                      field.onChange(
                        e.target.value === '' ? '' : Number(e.target.value)
                      )
                    }
                  />
                </FormControl>
                <FormDescription>{t('0 means unlimited')}</FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={props.form.control}
            name={`quota_rules.${index}.quota_reset_period`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Reset Period')}</FormLabel>
                <Select value={field.value} onValueChange={field.onChange}>
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {getResetPeriodOptions(t).map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />
          {values[index]?.quota_reset_period === 'custom' && (
            <FormField
              control={props.form.control}
              name={`quota_rules.${index}.quota_reset_custom_seconds`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Custom hours')}</FormLabel>
                  <FormControl>
                    <Input
                      name={field.name}
                      ref={field.ref}
                      onBlur={field.onBlur}
                      value={field.value / 3600}
                      type='number'
                      min={1 / 3600}
                      step='any'
                      onChange={(e) =>
                        field.onChange(
                          Math.round(Number(e.target.value) * 3600)
                        )
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
        </div>
      ))}
      <Button
        type='button'
        variant='outline'
        onClick={() =>
          rules.append({
            total_amount: 0,
            quota_reset_period: 'never',
            quota_reset_custom_seconds: 0,
          })
        }
      >
        {t('Add quota rule')}
      </Button>
      {props.form.formState.errors.quota_rules?.root?.message && (
        <p role='alert' className='text-destructive text-sm'>
          {props.form.formState.errors.quota_rules.root.message}
        </p>
      )}
    </div>
  )
}
