package model

import (
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
)

// SubscriptionQuotaSpec is a billing limit, expressed in the host's quota units.
type SubscriptionQuotaSpec struct {
	TotalAmount             int64  `json:"total_amount" gorm:"not null"`
	QuotaResetPeriod        string `json:"quota_reset_period" gorm:"type:varchar(16);not null"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds" gorm:"not null"`
}

type SubscriptionPlanQuotaRule struct {
	Id                    int `json:"id"`
	PlanId                int `json:"-" gorm:"uniqueIndex:idx_plan_quota_position,priority:1"`
	Position              int `json:"-" gorm:"uniqueIndex:idx_plan_quota_position,priority:2"`
	SubscriptionQuotaSpec `gorm:"embedded"`
}

type UserSubscriptionQuotaRule struct {
	Id                    int `json:"id"`
	UserSubscriptionId    int `json:"-" gorm:"uniqueIndex:idx_sub_quota_position,priority:1"`
	Position              int `json:"-" gorm:"uniqueIndex:idx_sub_quota_position,priority:2"`
	SubscriptionQuotaSpec `gorm:"embedded"`
	CurrentPeriodId       int   `json:"-"`
	LastResetTime         int64 `json:"last_reset_time"`
	NextResetTime         int64 `json:"next_reset_time" gorm:"index"`
	AmountUsed            int64 `json:"amount_used" gorm:"-"`
}

type SubscriptionQuotaPeriod struct {
	Id         int
	RuleId     int `gorm:"index"`
	StartTime  int64
	AmountUsed int64 `gorm:"not null"`
}

// Allocations survive resets, so delayed refunds only release their original period.
type SubscriptionQuotaAllocation struct {
	Id        int
	RequestId string `gorm:"type:varchar(64);uniqueIndex:idx_request_quota_rule,priority:1"`
	RuleId    int    `gorm:"uniqueIndex:idx_request_quota_rule,priority:2"`
	PeriodId  int    `gorm:"index"`
	Amount    int64  `gorm:"not null"`
}

func ValidateSubscriptionQuotaRules(rules []SubscriptionPlanQuotaRule) error {
	if len(rules) == 0 {
		return errors.New("At least one quota rule is required")
	}
	for _, r := range rules {
		if r.TotalAmount < 0 {
			return errors.New("Total quota cannot be negative")
		}
		switch r.QuotaResetPeriod {
		case SubscriptionResetNever, SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly:
		case SubscriptionResetCustom:
			if r.QuotaResetCustomSeconds <= 0 || r.QuotaResetCustomSeconds > math.MaxInt64/int64(time.Second) {
				return errors.New("Custom reset cycle is out of range")
			}
		default:
			return errors.New("Invalid quota reset period")
		}
	}
	return nil
}

func (p *SubscriptionPlan) AfterCreate(tx *gorm.DB) error {
	rules := p.QuotaRules
	if rules == nil {
		rules = []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: SubscriptionQuotaSpec{p.TotalAmount, NormalizeResetPeriod(p.QuotaResetPeriod), p.QuotaResetCustomSeconds}}}
	}
	return ReplaceSubscriptionPlanQuotaRulesTx(tx, p, rules)
}

func ReplaceSubscriptionPlanQuotaRulesTx(tx *gorm.DB, plan *SubscriptionPlan, rules []SubscriptionPlanQuotaRule) error {
	if err := ValidateSubscriptionQuotaRules(rules); err != nil {
		return err
	}
	if err := tx.Where("plan_id = ?", plan.Id).Delete(&SubscriptionPlanQuotaRule{}).Error; err != nil {
		return err
	}
	for i := range rules {
		rules[i].Id = 0
		rules[i].PlanId = plan.Id
		rules[i].Position = i
		if rules[i].QuotaResetPeriod != SubscriptionResetCustom {
			rules[i].QuotaResetCustomSeconds = 0
		}
	}
	if err := tx.Create(&rules).Error; err != nil {
		return err
	}
	plan.QuotaRules = rules
	return nil
}

func LoadSubscriptionPlanQuotaRules(tx *gorm.DB, plans []SubscriptionPlan) error {
	if len(plans) == 0 {
		return nil
	}
	ids := make([]int, len(plans))
	for i := range plans {
		ids[i] = plans[i].Id
	}
	var rules []SubscriptionPlanQuotaRule
	if err := tx.Where("plan_id IN ?", ids).Order("plan_id, position").Find(&rules).Error; err != nil {
		return err
	}
	byPlan := make(map[int][]SubscriptionPlanQuotaRule)
	for _, r := range rules {
		byPlan[r.PlanId] = append(byPlan[r.PlanId], r)
	}
	for i := range plans {
		plans[i].QuotaRules = byPlan[plans[i].Id]
		if len(plans[i].QuotaRules) == 0 {
			return fmt.Errorf("subscription plan %d quota rules missing", plans[i].Id)
		}
	}
	return nil
}

func (s *UserSubscription) AfterCreate(tx *gorm.DB) error {
	var rules []SubscriptionPlanQuotaRule
	if len(s.QuotaRules) > 0 {
		rules = make([]SubscriptionPlanQuotaRule, len(s.QuotaRules))
		for i := range s.QuotaRules {
			rules[i].SubscriptionQuotaSpec = s.QuotaRules[i].SubscriptionQuotaSpec
		}
	} else if err := tx.Where("plan_id = ?", s.PlanId).Order("position").Find(&rules).Error; err != nil {
		return err
	}
	if len(rules) == 0 {
		return errors.New("subscription plan quota rules missing")
	}
	return createSubscriptionQuotaSnapshot(tx, s, rules, false)
}

func createSubscriptionQuotaSnapshot(tx *gorm.DB, sub *UserSubscription, specs []SubscriptionPlanQuotaRule, legacy bool) error {
	if err := ValidateSubscriptionQuotaRules(specs); err != nil {
		return err
	}
	sub.QuotaRules = make([]UserSubscriptionQuotaRule, 0, len(specs))
	for i, spec := range specs {
		rule := UserSubscriptionQuotaRule{UserSubscriptionId: sub.Id, Position: i, SubscriptionQuotaSpec: spec.SubscriptionQuotaSpec, LastResetTime: sub.StartTime}
		rule.NextResetTime = calcNextResetTime(time.Unix(sub.StartTime, 0), &SubscriptionPlan{QuotaResetPeriod: spec.QuotaResetPeriod, QuotaResetCustomSeconds: spec.QuotaResetCustomSeconds}, sub.EndTime)
		used := int64(0)
		if legacy {
			rule.LastResetTime = sub.LastResetTime
			rule.NextResetTime = sub.NextResetTime
			used = sub.AmountUsed
		}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		period := SubscriptionQuotaPeriod{RuleId: rule.Id, StartTime: rule.LastResetTime, AmountUsed: used}
		if err := tx.Create(&period).Error; err != nil {
			return err
		}
		rule.CurrentPeriodId = period.Id
		if err := tx.Save(&rule).Error; err != nil {
			return err
		}
		rule.AmountUsed = used
		sub.QuotaRules = append(sub.QuotaRules, rule)
	}
	return nil
}

// MigrateSubscriptionQuotaRules is repeatable and must run before serving traffic.
func MigrateSubscriptionQuotaRules(db *gorm.DB) error {
	var plans []SubscriptionPlan
	if err := db.Find(&plans).Error; err != nil {
		return err
	}
	for i := range plans {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var parent SubscriptionPlan
			if err := lockForUpdate(tx).First(&parent, plans[i].Id).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&SubscriptionPlanQuotaRule{}).Where("plan_id = ?", plans[i].Id).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			return plans[i].AfterCreate(tx)
		}); err != nil {
			return err
		}
	}
	var batch []UserSubscription
	return db.Order("id").FindInBatches(&batch, 200, func(tx *gorm.DB, _ int) error {
		for i := range batch {
			if err := db.Transaction(func(inner *gorm.DB) error {
				var parent UserSubscription
				if err := lockForUpdate(inner).First(&parent, batch[i].Id).Error; err != nil {
					return err
				}
				var count int64
				if err := inner.Model(&UserSubscriptionQuotaRule{}).Where("user_subscription_id = ?", batch[i].Id).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return nil
				}
				var plan SubscriptionPlan
				if err := inner.First(&plan, batch[i].PlanId).Error; err != nil {
					return err
				}
				spec := SubscriptionQuotaSpec{batch[i].AmountTotal, NormalizeResetPeriod(plan.QuotaResetPeriod), plan.QuotaResetCustomSeconds}
				return createSubscriptionQuotaSnapshot(inner, &batch[i], []SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: spec}}, true)
			}); err != nil {
				return err
			}
		}
		return nil
	}).Error
}

func LoadUserSubscriptionQuotaRules(tx *gorm.DB, subs []UserSubscription) error {
	return loadUserSubscriptionQuotaRules(tx, subs, false)
}

func loadUserSubscriptionQuotaRules(tx *gorm.DB, subs []UserSubscription, locked bool) error {
	if len(subs) == 0 {
		return nil
	}
	ids := make([]int, len(subs))
	for i := range subs {
		ids[i] = subs[i].Id
	}
	query := tx
	if locked {
		query = lockForUpdate(tx)
	}
	var rules []UserSubscriptionQuotaRule
	if err := query.Where("user_subscription_id IN ?", ids).Order("user_subscription_id, position").Find(&rules).Error; err != nil {
		return err
	}
	periodIds := make([]int, len(rules))
	for i := range rules {
		periodIds[i] = rules[i].CurrentPeriodId
	}
	var periods []SubscriptionQuotaPeriod
	if len(periodIds) > 0 {
		periodQuery := tx
		if locked {
			periodQuery = lockForUpdate(tx)
		}
		if err := periodQuery.Where("id IN ?", periodIds).Find(&periods).Error; err != nil {
			return err
		}
	}
	byPeriod := make(map[int]SubscriptionQuotaPeriod)
	for _, p := range periods {
		byPeriod[p.Id] = p
	}
	bySub := make(map[int][]UserSubscriptionQuotaRule)
	for _, r := range rules {
		period, ok := byPeriod[r.CurrentPeriodId]
		if !ok || period.RuleId != r.Id {
			return errors.New("subscription quota period missing")
		}
		r.AmountUsed = period.AmountUsed
		bySub[r.UserSubscriptionId] = append(bySub[r.UserSubscriptionId], r)
	}
	for i := range subs {
		subs[i].QuotaRules = bySub[subs[i].Id]
		if len(subs[i].QuotaRules) == 0 {
			return fmt.Errorf("subscription %d quota rules missing", subs[i].Id)
		}
		// Project the first rule for legacy API readers; accounting only reads rules.
		first := subs[i].QuotaRules[0]
		subs[i].AmountTotal = first.TotalAmount
		subs[i].AmountUsed = first.AmountUsed
		subs[i].LastResetTime = first.LastResetTime
		subs[i].NextResetTime = first.NextResetTime
	}
	return nil
}

func refreshSubscriptionQuotaTx(tx *gorm.DB, sub *UserSubscription, now int64, manual, advance bool) error {
	subs := []UserSubscription{*sub}
	if err := loadUserSubscriptionQuotaRules(tx, subs, true); err != nil {
		return err
	}
	sub.QuotaRules = subs[0].QuotaRules
	for i := range sub.QuotaRules {
		r := &sub.QuotaRules[i]
		due := r.NextResetTime > 0 && r.NextResetTime <= now
		if !manual && !due {
			continue
		}
		base := r.LastResetTime
		if base <= 0 {
			base = sub.StartTime
		}
		next := r.NextResetTime
		plan := &SubscriptionPlan{QuotaResetPeriod: r.QuotaResetPeriod, QuotaResetCustomSeconds: r.QuotaResetCustomSeconds}
		if manual && advance {
			base = now
			next = calcNextResetTime(time.Unix(base, 0), plan, sub.EndTime)
			if next == 0 {
				base = 0
			}
		} else if due {
			if r.QuotaResetPeriod == SubscriptionResetCustom {
				elapsed := (now - base) / r.QuotaResetCustomSeconds
				base += elapsed * r.QuotaResetCustomSeconds
				next = calcNextResetTime(time.Unix(base, 0), plan, sub.EndTime)
			} else {
				// Calendar periods jump directly to the boundary containing now.
				t := time.Unix(now, 0)
				switch r.QuotaResetPeriod {
				case SubscriptionResetDaily:
					base = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Unix()
				case SubscriptionResetWeekly:
					days := (int(t.Weekday()) + 6) % 7
					base = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, -days).Unix()
				case SubscriptionResetMonthly:
					base = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).Unix()
				}
				next = calcNextResetTime(time.Unix(base, 0), plan, sub.EndTime)
			}
		}
		period := SubscriptionQuotaPeriod{RuleId: r.Id, StartTime: now}
		if err := tx.Create(&period).Error; err != nil {
			return err
		}
		r.CurrentPeriodId = period.Id
		r.LastResetTime = base
		r.NextResetTime = next
		r.AmountUsed = 0
		if err := tx.Save(r).Error; err != nil {
			return err
		}
	}
	return nil
}

// AdjustSubscriptionRequestQuota always adjusts the periods selected by pre-consume.
// reserve checks limits; settlement records actual usage even above a limit.
func AdjustSubscriptionRequestQuota(requestId string, subId int, delta int64, reserve bool) error {
	if delta == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return adjustSubscriptionRequestQuotaTx(tx, requestId, subId, delta, reserve)
	})
}

func adjustSubscriptionRequestQuotaTx(tx *gorm.DB, requestId string, subId int, delta int64, reserve bool) error {
	var sub UserSubscription
	if err := lockForUpdate(tx).First(&sub, subId).Error; err != nil {
		return err
	}
	var record SubscriptionPreConsumeRecord
	if err := lockForUpdate(tx).Where("request_id = ? AND user_subscription_id = ?", requestId, subId).First(&record).Error; err != nil {
		return err
	}
	if record.Status == "refunded" {
		return errors.New("subscription request already refunded")
	}
	var allocations []SubscriptionQuotaAllocation
	if err := lockForUpdate(tx).Where("request_id = ?", requestId).Order("rule_id").Find(&allocations).Error; err != nil {
		return err
	}
	var ruleCount int64
	if err := tx.Model(&UserSubscriptionQuotaRule{}).Where("user_subscription_id = ?", subId).Count(&ruleCount).Error; err != nil {
		return err
	}
	if len(allocations) == 0 || int64(len(allocations)) != ruleCount {
		return errors.New("subscription quota allocations missing")
	}
	for i := range allocations {
		a := &allocations[i]
		var p SubscriptionQuotaPeriod
		if err := lockForUpdate(tx).First(&p, a.PeriodId).Error; err != nil {
			return err
		}
		if delta > 0 && (p.AmountUsed > math.MaxInt64-delta || a.Amount > math.MaxInt64-delta) {
			return errors.New("subscription quota overflow")
		}
		var r UserSubscriptionQuotaRule
		if err := lockForUpdate(tx).First(&r, a.RuleId).Error; err != nil {
			return err
		}
		if r.UserSubscriptionId != subId || p.RuleId != r.Id {
			return errors.New("subscription quota accounting mismatch")
		}
		applied := delta
		if delta < 0 {
			applied = max(delta, -a.Amount)
		}
		if reserve && applied > 0 {
			if r.TotalAmount > 0 && (p.AmountUsed > r.TotalAmount || applied > r.TotalAmount-p.AmountUsed) {
				return fmt.Errorf("subscription quota insufficient, rule=%d reset=%d", r.Id, r.NextResetTime)
			}
		}
		p.AmountUsed += applied
		a.Amount += applied
		if p.AmountUsed < 0 {
			return errors.New("subscription quota accounting mismatch")
		}
		if err := tx.Save(&p).Error; err != nil {
			return err
		}
		if err := tx.Save(a).Error; err != nil {
			return err
		}
	}
	return nil
}

func LockSubscriptionPlanTx(tx *gorm.DB, id int, plan *SubscriptionPlan) error {
	return lockForUpdate(tx).Where("id = ?", id).First(plan).Error
}

// SubscriptionRequestQuotaUsage describes each limit independently for logs and notices.
type SubscriptionRequestQuotaUsage struct {
	RuleId                  int    `json:"rule_id"`
	TotalAmount             int64  `json:"total_amount"`
	AmountUsed              int64  `json:"amount_used"`
	Consumed                int64  `json:"consumed"`
	QuotaResetPeriod        string `json:"quota_reset_period"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds"`
	NextResetTime           int64  `json:"next_reset_time"`
}

func GetSubscriptionRequestQuotaUsage(requestId string) ([]SubscriptionRequestQuotaUsage, error) {
	return subscriptionRequestQuotaUsageTx(DB, requestId)
}

func subscriptionRequestQuotaUsageTx(tx *gorm.DB, requestId string) ([]SubscriptionRequestQuotaUsage, error) {
	var usage []SubscriptionRequestQuotaUsage
	err := tx.Table("subscription_quota_allocations AS a").
		Select("a.rule_id, a.amount AS consumed, r.total_amount, r.quota_reset_period, r.quota_reset_custom_seconds, r.next_reset_time, p.amount_used").
		Joins("JOIN user_subscription_quota_rules AS r ON r.id = a.rule_id").
		Joins("JOIN subscription_quota_periods AS p ON p.id = a.period_id").
		Where("a.request_id = ?", requestId).Order("r.position").Scan(&usage).Error
	if err != nil {
		return nil, err
	}
	if len(usage) == 0 {
		return nil, errors.New("subscription quota allocations missing")
	}
	return usage, nil
}
