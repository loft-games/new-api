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
import { zodResolver } from '@hookform/resolvers/zod'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { TFunction } from 'i18next'
import { useForm, type Resolver } from 'react-hook-form'
import { describe, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import {
  SubscriptionQuotaRules,
  SubscriptionQuotaUsage,
} from '../components/subscription-quota-rules'
import { SubscriptionQuotaRulesEditor } from '../components/subscription-quota-rules-editor'
import {
  formValuesToPlanPayload,
  getPlanFormSchema,
  PLAN_FORM_DEFAULTS,
  planToFormValues,
  type PlanFormValues,
} from '../lib/plan-form'
import type { SubscriptionPlan } from '../types'

const t = ((key: string) => key) as TFunction

function RulesForm(props: { onSubmit: (values: PlanFormValues) => void }) {
  const form = useForm<PlanFormValues>({
    resolver: zodResolver(getPlanFormSchema(t)) as Resolver<PlanFormValues>,
    defaultValues: { ...PLAN_FORM_DEFAULTS, title: 'Test plan' },
  })
  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(props.onSubmit)}>
        <SubscriptionQuotaRulesEditor form={form} />
        <Button type='submit'>Save</Button>
      </form>
    </Form>
  )
}

describe('subscription quota rules', () => {
  it('allows adding and removing rules while keeping at least one rule', async () => {
    const user = userEvent.setup()
    render(<RulesForm onSubmit={vi.fn()} />)
    expect(
      screen.getByRole('button', { name: 'Remove quota rule 1' })
    ).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Add quota rule' }))
    expect(screen.getByText('Quota rule 2')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Remove quota rule 1' })
    ).toBeEnabled()
    await user.click(
      screen.getByRole('button', { name: 'Remove quota rule 1' })
    )
    expect(screen.queryByText('Quota rule 2')).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Remove quota rule 1' })
    ).toBeDisabled()
  })

  it('converts five custom hours into 18000 seconds when saving', async () => {
    const user = userEvent.setup()
    const submitted = vi.fn()
    render(<RulesForm onSubmit={submitted} />)
    await user.click(screen.getByRole('combobox', { name: 'Reset Period' }))
    await user.click(await screen.findByRole('option', { name: /Custom/ }))
    const hours = screen.getByRole('spinbutton', { name: 'Custom hours' })
    fireEvent.change(hours, { target: { value: '5' } })
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(submitted).toHaveBeenCalled())
    expect(submitted.mock.calls[0][0].quota_rules[0]).toMatchObject({
      quota_reset_period: 'custom',
      quota_reset_custom_seconds: 18000,
    })
  })

  it('rejects an empty rule list and zero custom duration', () => {
    const schema = getPlanFormSchema(t)
    expect(
      schema.safeParse({
        ...PLAN_FORM_DEFAULTS,
        title: 'Test',
        quota_rules: [],
      }).success
    ).toBe(false)
    expect(
      schema.safeParse({
        ...PLAN_FORM_DEFAULTS,
        title: 'Test',
        quota_rules: [
          {
            total_amount: 1,
            quota_reset_period: 'custom',
            quota_reset_custom_seconds: 0,
          },
        ],
      }).success
    ).toBe(false)
  })

  it('preserves every rule and explicit unlimited quota through form round trips', () => {
    const plan = {
      id: 1,
      title: 'Test',
      price_amount: 1,
      currency: 'USD',
      duration_unit: 'month',
      duration_value: 1,
      enabled: true,
      allow_balance_pay: false,
      allow_wallet_overflow: false,
      sort_order: 0,
      max_purchase_per_user: 0,
      total_amount: 500000,
      quota_reset_period: 'never',
      quota_rules: [
        {
          total_amount: 500000,
          quota_reset_period: 'custom',
          quota_reset_custom_seconds: 18000,
        },
        {
          total_amount: 0,
          quota_reset_period: 'weekly',
          quota_reset_custom_seconds: 0,
        },
      ],
    } satisfies SubscriptionPlan
    expect(
      formValuesToPlanPayload(planToFormValues(plan)).plan.quota_rules
    ).toEqual(plan.quota_rules)
  })

  it('shows each rule and its own usage and next reset', () => {
    const rules = [
      {
        id: 1,
        total_amount: 1000000,
        quota_reset_period: 'custom' as const,
        quota_reset_custom_seconds: 18000,
      },
      {
        id: 2,
        total_amount: 0,
        quota_reset_period: 'weekly' as const,
        quota_reset_custom_seconds: 0,
      },
    ]
    const view = render(<SubscriptionQuotaRules rules={rules} />)
    expect(screen.getByText(/5 hours/)).toBeInTheDocument()
    expect(screen.getByText(/Weekly: Unlimited/)).toBeInTheDocument()
    view.rerender(
      <SubscriptionQuotaUsage
        rules={rules.map((rule, i) => ({
          ...rule,
          amount_used: 500000,
          last_reset_time: 0,
          next_reset_time: 18000 * (i + 1),
        }))}
      />
    )
    expect(screen.getAllByText(/Next reset/)).toHaveLength(2)
    expect(
      screen.getByRole('progressbar', { name: '5 hours' })
    ).toBeInTheDocument()
    expect(screen.getByText(/Remaining/)).toBeInTheDocument()
  })
})
