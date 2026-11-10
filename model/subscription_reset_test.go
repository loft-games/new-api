package model

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedSubscriptionResetPlan(t *testing.T, plan *SubscriptionPlan) {
	t.Helper()
	require.NoError(t, DB.Create(plan).Error)
}

func seedSubscriptionResetSub(t *testing.T, sub *UserSubscription) {
	t.Helper()
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Create(sub).Error)
	require.NoError(t, MigrateSubscriptionQuotaRules(DB))
}

func getSubscriptionResetSub(t *testing.T, id int) UserSubscription {
	t.Helper()
	var sub UserSubscription
	require.NoError(t, DB.Where("id = ?", id).First(&sub).Error)
	subs := []UserSubscription{sub}
	require.NoError(t, LoadUserSubscriptionQuotaRules(DB, subs))
	sub.AmountUsed = subs[0].QuotaRules[0].AmountUsed
	sub.LastResetTime = subs[0].QuotaRules[0].LastResetTime
	sub.NextResetTime = subs[0].QuotaRules[0].NextResetTime
	return sub
}

func TestAdminResetUserSubscriptionsByPlanResetsAllActiveMatchesAndAdvancesTime(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9101,
		Title:            "Pro",
		PriceAmount:      10,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	otherPlan := &SubscriptionPlan{
		Id:               9102,
		Title:            "Basic",
		PriceAmount:      1,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetPlan(t, otherPlan)

	activeEnd := now + 30*24*3600
	expiredEnd := now - 1
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9201, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 300, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9202, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9203, UserId: 101, PlanId: otherPlan.Id, AmountTotal: 100, AmountUsed: 60, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9204, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 700, StartTime: now - 7200, EndTime: expiredEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now - 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9205, UserId: 102, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 800, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9206, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 900, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled", LastResetTime: now - 3600, NextResetTime: now + 120})

	beforeReset := GetDBTimestamp()
	result, err := AdminResetUserSubscriptionsByPlan(101, plan.Id, true)
	afterReset := GetDBTimestamp()

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, plan.Id, result.PlanId)
	assert.Equal(t, 2, result.MatchedCount)
	assert.Equal(t, 2, result.ResetCount)
	assert.Equal(t, 1, result.UserCount)
	assert.Equal(t, []int{101}, result.AffectedUserIds)
	assert.True(t, result.AdvanceResetTime)

	for _, id := range []int{9201, 9202} {
		sub := getSubscriptionResetSub(t, id)
		assert.Zero(t, sub.AmountUsed)
		assert.GreaterOrEqual(t, sub.LastResetTime, beforeReset)
		assert.LessOrEqual(t, sub.LastResetTime, afterReset)
		assert.Equal(t, calcNextResetTime(time.Unix(sub.LastResetTime, 0), plan, sub.EndTime), sub.NextResetTime)
	}
	assert.EqualValues(t, 60, getSubscriptionResetSub(t, 9203).AmountUsed)
	assert.EqualValues(t, 700, getSubscriptionResetSub(t, 9204).AmountUsed)
	assert.EqualValues(t, 800, getSubscriptionResetSub(t, 9205).AmountUsed)
	assert.EqualValues(t, 900, getSubscriptionResetSub(t, 9206).AmountUsed)
}

func TestAdminResetUserSubscriptionsByPlanKeepsResetTimes(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9301,
		Title:            "Team",
		PriceAmount:      20,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      2000,
		QuotaResetPeriod: SubscriptionResetMonthly,
	}
	seedSubscriptionResetPlan(t, plan)

	lastReset := now - 86400
	nextReset := now + 86400
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9302, UserId: 201, PlanId: plan.Id, AmountTotal: 2000, AmountUsed: 1200, StartTime: now - 172800, EndTime: now + 30*24*3600, Status: "active", LastResetTime: lastReset, NextResetTime: nextReset})

	result, err := AdminResetUserSubscriptionsByPlan(201, plan.Id, false)

	require.NoError(t, err)
	assert.False(t, result.AdvanceResetTime)
	sub := getSubscriptionResetSub(t, 9302)
	assert.Zero(t, sub.AmountUsed)
	assert.Equal(t, lastReset, sub.LastResetTime)
	assert.Equal(t, nextReset, sub.NextResetTime)
}

func TestAdminResetUserSubscriptionsByPlanNoActiveMatchReturnsError(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:            9401,
		Title:         "Expired",
		PriceAmount:   10,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9402, UserId: 301, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, StartTime: now - 7200, EndTime: now - 1, Status: "active"})

	result, err := AdminResetUserSubscriptionsByPlan(301, plan.Id, true)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, strings.Contains(err.Error(), "This user has no active subscription to this plan"))
}

func TestAdminResetPlanSubscriptionsResetsAllActiveUsers(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9501,
		Title:            "Business",
		PriceAmount:      30,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      3000,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	seedSubscriptionResetPlan(t, plan)

	activeEnd := now + 30*24*3600
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9502, UserId: 401, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1000, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9503, UserId: 401, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1100, StartTime: now - 3500, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9504, UserId: 402, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1200, StartTime: now - 3400, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9505, UserId: 403, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1300, StartTime: now - 7200, EndTime: now - 1, Status: "active", LastResetTime: now - 3600, NextResetTime: now - 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9506, UserId: 404, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1400, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled", LastResetTime: now - 3600, NextResetTime: now + 10})

	result, err := AdminResetPlanSubscriptions(plan.Id, true)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.MatchedCount)
	assert.Equal(t, 3, result.ResetCount)
	assert.Equal(t, 2, result.UserCount)
	assert.Equal(t, []int{401, 402}, result.AffectedUserIds)
	for _, id := range []int{9502, 9503, 9504} {
		sub := getSubscriptionResetSub(t, id)
		assert.Zero(t, sub.AmountUsed)
		assert.Zero(t, sub.LastResetTime)
		assert.Zero(t, sub.NextResetTime)
	}
	assert.EqualValues(t, 1300, getSubscriptionResetSub(t, 9505).AmountUsed)
	assert.EqualValues(t, 1400, getSubscriptionResetSub(t, 9506).AmountUsed)
}

func TestAdminResetPlanSubscriptionsNoMatchSucceeds(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Id:            9601,
		Title:         "Empty",
		PriceAmount:   10,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	seedSubscriptionResetPlan(t, plan)

	result, err := AdminResetPlanSubscriptions(plan.Id, true)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Zero(t, result.MatchedCount)
	assert.Zero(t, result.ResetCount)
	assert.Zero(t, result.UserCount)
	assert.Empty(t, result.AffectedUserIds)
}

func quotaRulesFixture(t *testing.T, rules []SubscriptionPlanQuotaRule) *UserSubscription {
	t.Helper()
	truncateTables(t)
	plan := &SubscriptionPlan{Title: t.Name(), DurationUnit: SubscriptionDurationMonth, DurationValue: 1, QuotaRules: rules}
	require.NoError(t, DB.Create(plan).Error)
	var sub *UserSubscription
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		sub, err = CreateUserSubscriptionFromPlanTx(tx, 801, plan, "test")
		return err
	}))
	return sub
}

func quotaRuleUsage(t *testing.T, sub *UserSubscription) []UserSubscriptionQuotaRule {
	t.Helper()
	subs := []UserSubscription{*sub}
	require.NoError(t, LoadUserSubscriptionQuotaRules(DB, subs))
	return subs[0].QuotaRules
}

func TestSubscriptionQuotaRulesIndependentLimitsAndPeriodRefund(t *testing.T) {
	sub := quotaRulesFixture(t, []SubscriptionPlanQuotaRule{
		{SubscriptionQuotaSpec: SubscriptionQuotaSpec{100, SubscriptionResetCustom, 18000}},
		{SubscriptionQuotaSpec: SubscriptionQuotaSpec{150, SubscriptionResetWeekly, 0}},
	})
	first, err := PreConsumeUserSubscription("quota-first", 801, "model", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, sub.Id, first.UserSubscriptionId)
	_, err = PreConsumeUserSubscription("quota-blocked", 801, "model", 0, 1)
	require.ErrorContains(t, err, "subscription quota insufficient")
	rules := quotaRuleUsage(t, sub)
	assert.EqualValues(t, 100, rules[0].AmountUsed)
	assert.EqualValues(t, 100, rules[1].AmountUsed)
	oldPeriod := rules[0].CurrentPeriodId
	now := GetDBTimestamp()
	require.NoError(t, DB.Model(&UserSubscriptionQuotaRule{}).Where("id = ?", rules[0].Id).Updates(map[string]any{"last_reset_time": now - 18000, "next_reset_time": now}).Error)
	_, err = PreConsumeUserSubscription("quota-second", 801, "model", 0, 50)
	require.NoError(t, err)
	rules = quotaRuleUsage(t, sub)
	assert.EqualValues(t, 50, rules[0].AmountUsed)
	assert.EqualValues(t, 150, rules[1].AmountUsed)
	assert.NotEqual(t, oldPeriod, rules[0].CurrentPeriodId)
	_, err = PreConsumeUserSubscription("quota-week-blocked", 801, "model", 0, 1)
	require.ErrorContains(t, err, "subscription quota insufficient")
	require.NoError(t, RefundSubscriptionPreConsume("quota-first"))
	require.NoError(t, RefundSubscriptionPreConsume("quota-first"))
	rules = quotaRuleUsage(t, sub)
	assert.EqualValues(t, 50, rules[0].AmountUsed, "refund must not release the new five-hour period")
	assert.EqualValues(t, 50, rules[1].AmountUsed)
	var period SubscriptionQuotaPeriod
	require.NoError(t, DB.First(&period, oldPeriod).Error)
	assert.Zero(t, period.AmountUsed)
	_, err = PreConsumeUserSubscription("quota-after-refund", 801, "model", 0, 50)
	require.NoError(t, err)
}

func TestSubscriptionQuotaRulesReserveRollbackSettlementAndSnapshot(t *testing.T) {
	sub := quotaRulesFixture(t, []SubscriptionPlanQuotaRule{
		{SubscriptionQuotaSpec: SubscriptionQuotaSpec{100, SubscriptionResetDaily, 0}},
		{SubscriptionQuotaSpec: SubscriptionQuotaSpec{60, SubscriptionResetWeekly, 0}},
	})
	_, err := PreConsumeUserSubscription("quota-reserve", 801, "model", 0, 50)
	require.NoError(t, err)
	require.Error(t, AdjustSubscriptionRequestQuota("quota-reserve", sub.Id, 20, true))
	for _, r := range quotaRuleUsage(t, sub) {
		assert.EqualValues(t, 50, r.AmountUsed)
	}
	// Actual usage is recorded even when it exceeds the original reservation.
	require.NoError(t, AdjustSubscriptionRequestQuota("quota-reserve", sub.Id, 20, false))
	for _, r := range quotaRuleUsage(t, sub) {
		assert.EqualValues(t, 70, r.AmountUsed)
	}
	_, err = PreConsumeUserSubscription("quota-over-limit", 801, "model", 0, 1)
	require.ErrorContains(t, err, "subscription quota insufficient")
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ReplaceSubscriptionPlanQuotaRulesTx(tx, &SubscriptionPlan{Id: sub.PlanId}, []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{1000, SubscriptionResetMonthly, 0}}})
	}))
	rules := quotaRuleUsage(t, sub)
	require.Len(t, rules, 2)
	assert.EqualValues(t, 60, rules[1].TotalAmount)
	assert.Equal(t, SubscriptionResetWeekly, rules[1].QuotaResetPeriod)
	resetNow := GetDBTimestamp()
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return refreshSubscriptionQuotaTx(tx, sub, resetNow, true, false) }))
	for _, r := range quotaRuleUsage(t, sub) {
		assert.Zero(t, r.AmountUsed)
	}
	require.NoError(t, AdjustSubscriptionRequestQuota("quota-reserve", sub.Id, -20, false))
	for _, r := range quotaRuleUsage(t, sub) {
		assert.Zero(t, r.AmountUsed, "late settlement must stay in the archived period")
	}
}

func TestSubscriptionQuotaRulesIdempotencyMissingRulesAndOverflow(t *testing.T) {
	sub := quotaRulesFixture(t, []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{0, SubscriptionResetNever, 0}}})
	_, err := PreConsumeUserSubscription("quota-retry", 801, "model", 0, 25)
	require.NoError(t, err)
	_, err = PreConsumeUserSubscription("quota-retry", 801, "model", 0, 25)
	require.NoError(t, err)
	assert.EqualValues(t, 25, quotaRuleUsage(t, sub)[0].AmountUsed)
	require.Error(t, AdjustSubscriptionRequestQuota("quota-retry", sub.Id, math.MaxInt64, false))
	assert.EqualValues(t, 25, quotaRuleUsage(t, sub)[0].AmountUsed)
	// Cleanup must retain request identities needed for delayed task refunds.
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "quota-retry").UpdateColumn("updated_at", GetDBTimestamp()-30*86400).Error)
	removed, err := CleanupSubscriptionPreConsumeRecords(7 * 86400)
	require.NoError(t, err)
	assert.Zero(t, removed)
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Delete(&UserSubscriptionQuotaRule{}).Error)
	_, err = PreConsumeUserSubscription("quota-missing", 801, "model", 0, 1)
	require.ErrorContains(t, err, "quota rules missing")
}

func TestSubscriptionQuotaRulesMigrationPreservesLegacyState(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := &SubscriptionPlan{Title: "legacy", TotalAmount: 100, QuotaResetPeriod: SubscriptionResetWeekly}
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
	sub := &UserSubscription{PlanId: plan.Id, UserId: 801, AmountTotal: 75, AmountUsed: 47, StartTime: now - 3600, EndTime: now + 86400*30, LastResetTime: now - 3600, NextResetTime: now + 7200, Status: "active"}
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Create(sub).Error)
	require.NoError(t, MigrateSubscriptionQuotaRules(DB))
	require.NoError(t, MigrateSubscriptionQuotaRules(DB))
	rules := quotaRuleUsage(t, sub)
	require.Len(t, rules, 1)
	assert.EqualValues(t, 75, rules[0].TotalAmount)
	assert.EqualValues(t, 47, rules[0].AmountUsed)
	assert.Equal(t, sub.LastResetTime, rules[0].LastResetTime)
	assert.Equal(t, sub.NextResetTime, rules[0].NextResetTime)
	var count int64
	require.NoError(t, DB.Model(&SubscriptionQuotaPeriod{}).Where("rule_id = ?", rules[0].Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestSubscriptionQuotaRulesValidationAndCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []SubscriptionPlanQuotaRule
	}{
		{"empty", []SubscriptionPlanQuotaRule{}},
		{"negative", []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{-1, SubscriptionResetNever, 0}}}},
		{"unknown", []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{1, "unknown", 0}}}},
		{"zero duration", []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{1, SubscriptionResetCustom, 0}}}},
		{"overflow", []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{1, SubscriptionResetCustom, math.MaxInt64}}}},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Error(t, ValidateSubscriptionQuotaRules(tc.rules)) })
	}
	base := time.Date(2026, 11, 8, 23, 59, 59, 0, time.Local)
	assert.Equal(t, time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local).Unix(), calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetWeekly}, 0))
	assert.Equal(t, base.Unix()+18000, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetCustom, QuotaResetCustomSeconds: 18000}, 0))
	assert.Zero(t, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetCustom, QuotaResetCustomSeconds: 18000}, base.Unix()+100))
}

func TestSubscriptionQuotaRulesSkipIdleCyclesAndKeepOriginalAnchor(t *testing.T) {
	sub := quotaRulesFixture(t, []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{100, SubscriptionResetCustom, 18000}}})
	start := sub.StartTime
	rules := quotaRuleUsage(t, sub)
	_, err := PreConsumeUserSubscription("idle-first", 801, "model", 0, 50)
	require.NoError(t, err)
	_, err = AdminDeleteUserSubscription(sub.Id)
	require.ErrorContains(t, err, "billing history")
	now := start + 4*18000 + 123
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return refreshSubscriptionQuotaTx(tx, sub, now, false, false) }))
	current := quotaRuleUsage(t, sub)[0]
	assert.Zero(t, current.AmountUsed)
	assert.Equal(t, start+4*18000, current.LastResetTime)
	assert.Equal(t, start+5*18000, current.NextResetTime)
	assert.NotEqual(t, rules[0].CurrentPeriodId, current.CurrentPeriodId)
	require.NoError(t, AdjustSubscriptionRequestQuota("idle-first", sub.Id, 20, false))
	assert.Zero(t, quotaRuleUsage(t, sub)[0].AmountUsed)
	require.NoError(t, RefundSubscriptionPreConsume("idle-first"))
	var old SubscriptionQuotaPeriod
	require.NoError(t, DB.First(&old, rules[0].CurrentPeriodId).Error)
	assert.Zero(t, old.AmountUsed, "full refund includes the additional settlement in the original period")
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", sub.Id).Update("end_time", GetDBTimestamp()-1).Error)
	_, err = PreConsumeUserSubscription("expired", 801, "model", 0, 1)
	require.ErrorContains(t, err, "no active subscription")
}
