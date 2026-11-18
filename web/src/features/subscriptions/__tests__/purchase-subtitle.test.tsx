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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

import { SubscriptionPurchaseDialog } from '../components/dialogs/subscription-purchase-dialog'
import { subscriptionPlanSchema } from '../types'

it('shows the complete multiline plan subtitle in the purchase dialog', () => {
  const subtitle = `Access selected models during your subscription.\n${'LongPlanDescription'.repeat(20)}`
  const plan = subscriptionPlanSchema.parse({
    id: 1,
    title: 'Pro',
    subtitle,
    price_amount: 10,
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'never',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 1000,
    quota_rules: [
      {
        total_amount: 1000,
        quota_reset_period: 'never',
        quota_reset_custom_seconds: 0,
      },
    ],
  })
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <SubscriptionPurchaseDialog open onOpenChange={vi.fn()} plan={{ plan }} />
    </QueryClientProvider>
  )
  const description = screen.getByText(
    (_, element) => element?.textContent === subtitle
  )
  expect(description).toBeVisible()
  expect(description).toHaveClass('whitespace-pre-wrap', 'break-words')
  expect(description).not.toHaveClass('truncate')
})

it.each([undefined, ''])(
  'opens a plan with subtitle %s without an empty description',
  (subtitle) => {
    const plan = subscriptionPlanSchema.parse({
      id: 1,
      title: 'Basic',
      subtitle,
      price_amount: 1,
      duration_unit: 'month',
      duration_value: 1,
      quota_reset_period: 'never',
      enabled: true,
      sort_order: 0,
      max_purchase_per_user: 0,
      total_amount: 100,
      quota_rules: [
        {
          total_amount: 100,
          quota_reset_period: 'never',
          quota_reset_custom_seconds: 0,
        },
      ],
    })
    render(
      <QueryClientProvider client={new QueryClient()}>
        <SubscriptionPurchaseDialog
          open
          onOpenChange={vi.fn()}
          plan={{ plan }}
        />
      </QueryClientProvider>
    )
    expect(screen.getByRole('dialog')).toBeVisible()
    expect(screen.queryByRole('paragraph')).not.toBeInTheDocument()
  }
)
