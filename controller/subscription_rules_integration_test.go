package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DSNs must refer to disposable databases. This exercises the real admin handlers,
// billing session, row locks and schema migrations, with no upstream provider calls.
func TestSubscriptionQuotaRulesDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "quota.db") + "?_pragma=journal_mode(WAL)")
			if dialect == common.DatabaseTypeMySQL {
				dsn := os.Getenv("TEST_QUOTA_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_QUOTA_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			}
			if dialect == common.DatabaseTypePostgreSQL {
				dsn := os.Getenv("TEST_QUOTA_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_QUOTA_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(8)
			if dialect == common.DatabaseTypeSQLite {
				sqlDB.SetMaxOpenConns(1)
			}
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			oldRedis, oldBatch := common.RedisEnabled, common.BatchUpdateEnabled
			model.DB, model.LOG_DB = db, db
			common.SetDatabaseTypes(dialect, dialect)
			common.RedisEnabled = false
			common.BatchUpdateEnabled = false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMain, oldLog)
				common.RedisEnabled = oldRedis
				common.BatchUpdateEnabled = oldBatch
				require.NoError(t, sqlDB.Close())
			})
			confirmPaymentComplianceForTest(t)
			tables := []any{&model.SubscriptionQuotaAllocation{}, &model.SubscriptionQuotaPeriod{}, &model.UserSubscriptionQuotaRule{}, &model.SubscriptionPlanQuotaRule{}, &model.SubscriptionPreConsumeRecord{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}}
			require.NoError(t, db.Migrator().DropTable(tables...))
			for range 2 {
				require.NoError(t, db.AutoMigrate(tables...))
				require.NoError(t, model.MigrateSubscriptionQuotaRules(db))
			}
			// Remove extension tables to reproduce the original schema before upgrading.
			require.NoError(t, db.Migrator().DropTable(tables[:4]...))
			versionQuery := "SELECT version()"
			if dialect == common.DatabaseTypeSQLite {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database: %s", version)
			// The released single-rule schema is represented by its original fields,
			// bypassing creation hooks. Repeated migrations must retain these values.
			now := time.Now().Unix()
			legacyPlan := &model.SubscriptionPlan{Id: 70001, Title: "legacy", TotalAmount: 100, QuotaResetPeriod: model.SubscriptionResetWeekly}
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(legacyPlan).Error)
			legacy := &model.UserSubscription{Id: 70001, PlanId: legacyPlan.Id, UserId: 70001, AmountTotal: 80, AmountUsed: 37, Status: "active", StartTime: now - 3600, EndTime: now + 86400, LastResetTime: now - 3600, NextResetTime: now + 7200}
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(legacy).Error)
			require.NoError(t, db.AutoMigrate(tables...))
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("quota_migration_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "subscription_quota_periods" {
					tx.AddError(fmt.Errorf("injected period creation failure"))
				}
			}))
			require.ErrorContains(t, model.MigrateSubscriptionQuotaRules(db), "injected period creation failure")
			require.NoError(t, db.Callback().Create().Remove("quota_migration_failure"))
			var partial int64
			require.NoError(t, db.Model(&model.UserSubscriptionQuotaRule{}).Count(&partial).Error)
			assert.Zero(t, partial, "failed migration must roll back the snapshot and its period")
			for range 2 {
				require.NoError(t, db.AutoMigrate(tables...))
				require.NoError(t, model.MigrateSubscriptionQuotaRules(db))
			}
			subs := []model.UserSubscription{*legacy}
			require.NoError(t, model.LoadUserSubscriptionQuotaRules(db, subs))
			require.Len(t, subs[0].QuotaRules, 1)
			assert.EqualValues(t, 37, subs[0].QuotaRules[0].AmountUsed)
			assert.EqualValues(t, 80, subs[0].QuotaRules[0].TotalAmount)
			assert.Equal(t, legacy.NextResetTime, subs[0].QuotaRules[0].NextResetTime)
			assert.Equal(t, legacy.LastResetTime, subs[0].QuotaRules[0].LastResetTime)

			assert.True(t, db.Migrator().HasIndex(&model.SubscriptionPlanQuotaRule{}, "idx_plan_quota_position"))
			assert.True(t, db.Migrator().HasIndex(&model.UserSubscriptionQuotaRule{}, "idx_sub_quota_position"))
			assert.True(t, db.Migrator().HasIndex(&model.SubscriptionQuotaAllocation{}, "idx_request_quota_rule"))

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/plans", AdminCreateSubscriptionPlan)
			router.PUT("/plans/:id", AdminUpdateSubscriptionPlan)
			router.POST("/bind", AdminBindSubscription)
			router.GET("/plans", AdminListSubscriptionPlans)
			invoke := func(method, path string, payload any) []byte {
				b, err := common.Marshal(payload)
				require.NoError(t, err)
				request := httptest.NewRequest(method, path, bytes.NewReader(b))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				assert.Equal(t, http.StatusOK, response.Code)
				return response.Body.Bytes()
			}
			plan := model.SubscriptionPlan{Title: "Five-hour and weekly", DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, QuotaRules: []model.SubscriptionPlanQuotaRule{
				{SubscriptionQuotaSpec: model.SubscriptionQuotaSpec{TotalAmount: 100, QuotaResetPeriod: model.SubscriptionResetCustom, QuotaResetCustomSeconds: 18000}},
				{SubscriptionQuotaSpec: model.SubscriptionQuotaSpec{TotalAmount: 150, QuotaResetPeriod: model.SubscriptionResetWeekly}},
			}}
			var created struct {
				Success bool                   `json:"success"`
				Data    model.SubscriptionPlan `json:"data"`
			}
			require.NoError(t, common.Unmarshal(invoke("POST", "/plans", AdminUpsertSubscriptionPlanRequest{Plan: plan}), &created))
			require.True(t, created.Success)
			invalid := plan
			invalid.QuotaRules = []model.SubscriptionPlanQuotaRule{}
			var rejected struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(invoke("POST", "/plans", AdminUpsertSubscriptionPlanRequest{Plan: invalid}), &rejected))
			assert.False(t, rejected.Success)
			plan = created.Data
			require.Len(t, plan.QuotaRules, 2)
			user := &model.User{Username: "quota-matrix", Quota: 1000, Group: "default", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(user).Error)
			var bound struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(invoke("POST", "/bind", AdminBindSubscriptionRequest{UserId: user.Id, PlanId: plan.Id}), &bound))
			require.True(t, bound.Success)
			var sub model.UserSubscription
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&sub).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
			// Exercise the production session including a streaming-style reservation.
			token := &model.Token{UserId: user.Id, Key: "quota-matrix-token", Name: "quota", RemainQuota: 1000, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(token).Error)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: "quota-matrix-session", UserSetting: dto.UserSetting{BillingPreference: "subscription_only"}}
			session, apiErr := service.NewBillingSession(c, info, 30)
			if apiErr != nil {
				t.Fatalf("billing failed: %v", apiErr.Err)
			}
			require.NotNil(t, session)
			require.NoError(t, session.ReserveBeforeSubmit(50))
			require.NoError(t, session.Settle(40))
			require.NoError(t, session.Settle(40))
			usage, err := model.GetSubscriptionRequestQuotaUsage(info.RequestId)
			require.NoError(t, err)
			require.Len(t, usage, 2)
			for _, r := range usage {
				assert.EqualValues(t, 40, r.AmountUsed)
				assert.EqualValues(t, 40, r.Consumed)
			}
			var chargedToken model.Token
			require.NoError(t, db.First(&chargedToken, token.Id).Error)
			assert.Equal(t, 960, chargedToken.RemainQuota, "multiple rules must not multiply token charges")
			var unchangedUser model.User
			require.NoError(t, db.First(&unchangedUser, user.Id).Error)
			assert.Equal(t, 1000, unchangedUser.Quota)
			require.NoError(t, model.RefundSubscriptionPreConsume(info.RequestId))
			require.NoError(t, model.RefundSubscriptionPreConsume(info.RequestId))
			usage, err = model.GetSubscriptionRequestQuotaUsage(info.RequestId)
			require.NoError(t, err)
			for _, r := range usage {
				assert.Zero(t, r.AmountUsed, "refund includes additional reservations and settlement")
			}
			// Concurrent reservations use independent SQL connections on MySQL/PostgreSQL.
			const workers = 8
			results := make(chan error, workers)
			var wg sync.WaitGroup
			for i := range workers {
				wg.Go(func() {
					_, err := model.PreConsumeUserSubscription(fmt.Sprintf("quota-concurrent-%d", i), user.Id, "model", 0, 30)
					results <- err
				})
			}
			wg.Wait()
			close(results)
			accepted := 0
			for err := range results {
				if err == nil {
					accepted++
				} else {
					require.ErrorContains(t, err, "subscription quota insufficient")
				}
			}
			assert.Equal(t, 3, accepted)
			_, err = model.AdminResetUserSubscriptionsByPlan(user.Id, plan.Id, false)
			require.NoError(t, err)
			retries := make(chan error, workers)
			for range workers {
				wg.Go(func() {
					_, err := model.PreConsumeUserSubscription("quota-identical-retry", user.Id, "model", 0, 30)
					retries <- err
				})
			}
			wg.Wait()
			close(retries)
			for err := range retries {
				require.NoError(t, err)
			}
			usage, err = model.GetSubscriptionRequestQuotaUsage("quota-identical-retry")
			require.NoError(t, err)
			require.Len(t, usage, 2)
			for _, r := range usage {
				assert.EqualValues(t, 30, r.AmountUsed)
			}
			require.NoError(t, model.RefundSubscriptionPreConsume("quota-identical-retry"))

			legacyUpdate := plan
			legacyUpdate.QuotaRules = nil
			require.NoError(t, common.Unmarshal(invoke("PUT", fmt.Sprintf("/plans/%d", plan.Id), AdminUpsertSubscriptionPlanRequest{Plan: legacyUpdate}), &rejected))
			assert.False(t, rejected.Success, "legacy fields cannot replace multiple rules")

			if dialect != common.DatabaseTypeSQLite {
				exerciseSubscriptionRefundRace(t, db, user, sub.Id)
			}
			exerciseSubscriptionRelayRequests(t, db, user, token)
			if dialect == common.DatabaseTypeSQLite {
				sqlDB.SetMaxOpenConns(8)
				results := make(chan error, workers)
				for i := range workers {
					wg.Go(func() {
						_, err := model.PreConsumeUserSubscription(fmt.Sprintf("sqlite-parallel-%d", i), user.Id, "model", 0, 10)
						results <- err
					})
				}
				wg.Wait()
				close(results)
				accepted := 0
				for err := range results {
					if err == nil {
						accepted++
					} else {
						assert.True(t, strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "subscription quota insufficient"), "%v", err)
					}
				}
				assert.GreaterOrEqual(t, accepted, 1)
				assert.LessOrEqual(t, accepted, 2)
				states := []model.UserSubscription{sub}
				require.NoError(t, model.LoadUserSubscriptionQuotaRules(db, states))
				for _, rule := range states[0].QuotaRules {
					assert.EqualValues(t, 80+accepted*10, rule.AmountUsed)
				}
				sqlDB.SetMaxOpenConns(1)
			}

			// Edits change future purchases, not the existing subscriber's rules.
			plan.QuotaRules = []model.SubscriptionPlanQuotaRule{{SubscriptionQuotaSpec: model.SubscriptionQuotaSpec{TotalAmount: 999, QuotaResetPeriod: model.SubscriptionResetMonthly}}}
			var updated struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(invoke("PUT", fmt.Sprintf("/plans/%d", plan.Id), AdminUpsertSubscriptionPlanRequest{Plan: plan}), &updated))
			require.True(t, updated.Success)
			snapshots := []model.UserSubscription{sub}
			require.NoError(t, model.LoadUserSubscriptionQuotaRules(db, snapshots))
			require.Len(t, snapshots[0].QuotaRules, 2)
			assert.EqualValues(t, 100, snapshots[0].QuotaRules[0].TotalAmount)
			assert.Equal(t, model.SubscriptionResetWeekly, snapshots[0].QuotaRules[1].QuotaResetPeriod)
			require.NoError(t, common.Unmarshal(invoke("POST", "/bind", AdminBindSubscriptionRequest{UserId: user.Id, PlanId: plan.Id}), &bound))
			require.True(t, bound.Success)
			var purchased model.UserSubscription
			require.NoError(t, db.Where("user_id = ?", user.Id).Order("id desc").First(&purchased).Error)
			fresh := []model.UserSubscription{purchased}
			require.NoError(t, model.LoadUserSubscriptionQuotaRules(db, fresh))
			require.Len(t, fresh[0].QuotaRules, 1)
			assert.EqualValues(t, 999, fresh[0].QuotaRules[0].TotalAmount)

		})
	}
}

// Exercise the real OpenAI response parsers and billing entry points against a
// local upstream. Authentication and channel selection are outside this fixture.
func exerciseSubscriptionRelayRequests(t *testing.T, db *gorm.DB, user *model.User, token *model.Token) {
	t.Helper()
	service.InitHttpClient()
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() { constant.StreamingTimeout = oldTimeout }()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request dto.GeneralOpenAIRequest
		if err := common.DecodeJson(r.Body, &request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Model == "quota-failure" {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, `{"error":{"message":"test failure","type":"server_error"}}`)
			return
		}
		if request.Stream != nil && *request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"model\":\"quota-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"model\":\"quota-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":20,\"total_tokens\":40}}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-test","object":"chat.completion","model":"quota-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":20,"total_tokens":40}}`)
	}))
	defer upstream.Close()
	channel := &model.Channel{Name: "quota-upstream", Key: "test-key", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(channel).Error)
	oldLog := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	defer func() { common.LogConsumeEnabled = oldLog }()
	for _, mode := range []string{"synchronous", "streaming", "failure"} {
		t.Run(mode, func(t *testing.T) {
			stream := mode == "streaming"
			name := "quota-model"
			pre := 30
			if mode == "failure" {
				name = "quota-failure"
				pre = 5
			}
			request := &dto.GeneralOpenAIRequest{Model: name, Stream: &stream, StreamOptions: &dto.StreamOptions{IncludeUsage: true}, Messages: []dto.Message{{Role: "user", Content: "hello"}}}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, name)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(ctx, constant.ContextKeyChannelKey, "test-key")
			requestID := "quota-relay-" + mode
			ctx.Set(common.RequestIdKey, requestID)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: requestID, Request: request, OriginModelName: name, UsingGroup: "default", UserGroup: "default", IsStream: stream, StartTime: time.Now(), FirstResponseTime: time.Now(), RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: relaytypes.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}, UserSetting: dto.UserSetting{BillingPreference: "subscription_only"}, PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			apiErr := service.PreConsumeBilling(ctx, pre, info)
			if apiErr != nil {
				t.Fatalf("pre-consume: %v", apiErr.Err)
			}
			if mode == "failure" {
				require.NoError(t, info.Billing.ReserveBeforeSubmit(15))
			}
			apiErr = relay.TextHelper(ctx, info)
			if mode == "failure" {
				require.NotNil(t, apiErr)
				done := make(chan struct{}, 1)
				const callback = "quota_request_refund"
				require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" && tx.Error == nil {
						select {
						case done <- struct{}{}:
						default:
						}
					}
				}))
				require.NotNil(t, relay.RefundFailedRequestBilling(ctx, info, apiErr))
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("request refund did not complete")
				}
				require.NoError(t, db.Callback().Update().Remove(callback))
			} else {
				if apiErr != nil {
					t.Fatalf("relay: %v", apiErr.Err)
				}
				assert.Equal(t, http.StatusOK, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "ok")
				if stream {
					assert.Contains(t, recorder.Body.String(), "[DONE]")
				}
			}
			usage, err := model.GetSubscriptionRequestQuotaUsage(requestID)
			require.NoError(t, err)
			require.Len(t, usage, 2)
			for _, rule := range usage {
				if mode == "failure" {
					assert.Zero(t, rule.Consumed)
					assert.EqualValues(t, 80, rule.AmountUsed)
				} else {
					assert.EqualValues(t, 40, rule.Consumed)
				}
			}
		})
	}
}

// Pause the refund after its initial lookup, then commit an extra reservation.
// This catches stale repeatable-read snapshots without sleeps or timing races.
func exerciseSubscriptionRefundRace(t *testing.T, db *gorm.DB, user *model.User, subID int) {
	t.Helper()
	const requestID = "quota-refund-snapshot"
	_, err := model.PreConsumeUserSubscription(requestID, user.Id, "model", 0, 10)
	require.NoError(t, err)
	paused, release := make(chan struct{}), make(chan struct{})
	once := make(chan struct{}, 1)
	once <- struct{}{}
	const callback = "quota_refund_snapshot"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		record, ok := tx.Statement.Dest.(*model.SubscriptionPreConsumeRecord)
		if !ok || record.RequestId != requestID {
			return
		}
		select {
		case <-once:
			close(paused)
			<-release
		default:
		}
	}))
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	defer func() { require.NoError(t, db.Callback().Query().Remove(callback)) }()
	finished := make(chan error, 1)
	go func() { finished <- model.RefundSubscriptionPreConsume(requestID) }()
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("refund lookup did not start")
	}
	require.NoError(t, model.AdjustSubscriptionRequestQuota(requestID, subID, 10, true))
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("refund did not finish")
	}
	usage, err := model.GetSubscriptionRequestQuotaUsage(requestID)
	require.NoError(t, err)
	require.Len(t, usage, 2)
	for _, rule := range usage {
		assert.Zero(t, rule.Consumed)
		assert.Zero(t, rule.AmountUsed)
	}
}
