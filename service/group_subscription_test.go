package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGroupSubscriptionBillingDatabaseMatrix(t *testing.T) {
	const optionKey = "group_ratio_setting.group_disable_subscription"
	original, err := config.ConfigToMap(ratio_setting.GetGroupRatioSetting())
	require.NoError(t, err)
	originalPolicy := original["group_disable_subscription"]
	if originalPolicy == "" {
		originalPolicy = "{}"
	}
	common.OptionMapRWMutex.Lock()
	optionMapWasNil := common.OptionMap == nil
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	oldOption, hadOption := common.OptionMap[optionKey]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{optionKey: originalPolicy}))
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if optionMapWasNil {
			common.OptionMap = nil
		} else if hadOption {
			common.OptionMap[optionKey] = oldOption
		} else {
			delete(common.OptionMap, optionKey)
		}
	})
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			switch dialect {
			case common.DatabaseTypeMySQL:
				dsn := os.Getenv("TEST_GROUP_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_GROUP_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case common.DatabaseTypePostgreSQL:
				dsn := os.Getenv("TEST_GROUP_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_GROUP_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			oldDB := model.DB
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB = db
			common.SetDatabaseTypes(dialect, oldLog)
			require.NoError(t, model.PurgeSubscriptionPlanCache())
			t.Cleanup(func() {
				model.DB = oldDB
				common.SetDatabaseTypes(oldMain, oldLog)
				require.NoError(t, model.PurgeSubscriptionPlanCache())
			})
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemTask{}, &model.User{}, &model.Token{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
			}
			query := "select version()"
			if dialect == common.DatabaseTypeSQLite {
				query = "select sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database: %s", version)
			require.NoError(t, model.UpdateOption(optionKey, "{}"))
			for _, invalid := range []string{`null`, `[]`, `{"PayAsYouGo":"true"}`, `{"PayAsYouGo":null}`} {
				require.Error(t, model.UpdateOption(optionKey, invalid))
				var saved model.Option
				require.NoError(t, db.Where(&model.Option{Key: optionKey}).First(&saved).Error)
				assert.Equal(t, "{}", saved.Value)
			}
			for index, tc := range []struct {
				name, preference, settings, group, userGroup string
				wallet                                       int
				wantSource                                   string
				wantError                                    bool
				wantReason                                   bool
			}{
				{name: "subscription first uses wallet", preference: "subscription_first", settings: `{"PayAsYouGo":true}`, wallet: 100, wantSource: BillingSourceWallet},
				{name: "wallet first uses wallet", preference: "wallet_first", settings: `{"PayAsYouGo":true}`, wallet: 100, wantSource: BillingSourceWallet},
				{name: "wallet only uses wallet", preference: "wallet_only", settings: `{"PayAsYouGo":true}`, wallet: 100, wantSource: BillingSourceWallet},
				{name: "subscription only reports balance error and reason", preference: "subscription_only", settings: `{"PayAsYouGo":true}`, wallet: 100, wantError: true, wantReason: true},
				{name: "subscription first cannot fall back from empty wallet", preference: "subscription_first", settings: `{"PayAsYouGo":true}`, wantError: true},
				{name: "wallet first cannot fall back from empty wallet", preference: "wallet_first", settings: `{"PayAsYouGo":true}`, wantError: true},
				{name: "missing setting preserves subscription", preference: "subscription_only", settings: `{}`, wallet: 100, wantSource: BillingSourceSubscription},
				{name: "unchecked setting preserves subscription", preference: "subscription_only", settings: `{"PayAsYouGo":false}`, wallet: 100, wantSource: BillingSourceSubscription},
				{name: "user group does not restrict a different selected group", preference: "subscription_only", settings: `{"PayAsYouGo":true}`, group: "default", userGroup: "PayAsYouGo", wallet: 100, wantSource: BillingSourceSubscription},
				{name: "arbitrary group name applies the policy", preference: "subscription_first", settings: `{"metered":true}`, group: "metered", wallet: 100, wantSource: BillingSourceWallet},
			} {
				t.Run(tc.name, func(t *testing.T) {
					user := model.User{Username: fmt.Sprintf("group_subscription_%d", index), Quota: tc.wallet, Status: common.UserStatusEnabled}
					require.NoError(t, db.Create(&user).Error)
					token := model.Token{UserId: user.Id, Key: fmt.Sprintf("group_subscription_%d", index), RemainQuota: 100, Status: common.TokenStatusEnabled}
					require.NoError(t, db.Create(&token).Error)
					plan := model.SubscriptionPlan{Title: "Group policy", TotalAmount: 100, DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1}
					require.NoError(t, db.Create(&plan).Error)
					subscription := model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: 100, StartTime: model.GetDBTimestamp(), EndTime: model.GetDBTimestamp() + 3600, Status: "active"}
					require.NoError(t, db.Create(&subscription).Error)
					require.NoError(t, model.UpdateOption(optionKey, tc.settings))
					var saved model.Option
					require.NoError(t, db.Where(&model.Option{Key: optionKey}).First(&saved).Error)
					assert.Equal(t, tc.settings, saved.Value)
					require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{optionKey: "{}"}))
					require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{optionKey: saved.Value}))
					t.Cleanup(func() {
						require.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.SubscriptionPreConsumeRecord{}).Error)
						require.NoError(t, db.Delete(&subscription).Error)
						require.NoError(t, db.Delete(&plan).Error)
						require.NoError(t, db.Unscoped().Delete(&token).Error)
						require.NoError(t, db.Unscoped().Delete(&user).Error)
					})
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					ctx.Request.Header.Set("Accept-Language", "zh-CN")
					info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, ForcePreConsume: true, RequestId: fmt.Sprintf("group-policy-%d-%s", index, dialect)}
					info.UserSetting.BillingPreference = tc.preference
					info.UsingGroup = tc.group
					if info.UsingGroup == "" {
						info.UsingGroup = "PayAsYouGo"
					}
					info.TokenGroup = info.UsingGroup
					info.UserGroup = tc.userGroup
					apiErr := PreConsumeBilling(ctx, 20, info)
					wantWallet, wantSubscription, wantToken := tc.wallet, int64(0), 100
					if tc.wantError {
						require.NotNil(t, apiErr)
						assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
						assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
						if tc.wantReason {
							assert.Contains(t, apiErr.Error(), "余额不足")
							assert.Contains(t, apiErr.Error(), "不允许使用订阅抵扣")
						}
					} else {
						require.Nil(t, apiErr)
						assert.Equal(t, tc.wantSource, info.BillingSource)
						info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 2}
						require.Nil(t, PreConsumeBilling(ctx, 30, info))
						require.NoError(t, info.Billing.Settle(25))
						wantToken -= 25
						if tc.wantSource == BillingSourceWallet {
							wantWallet -= 25
						} else {
							wantSubscription = 25
						}
					}
					require.NoError(t, db.First(&user, user.Id).Error)
					require.NoError(t, db.First(&subscription, subscription.Id).Error)
					require.NoError(t, db.First(&token, token.Id).Error)
					assert.Equal(t, wantWallet, user.Quota)
					assert.Equal(t, wantSubscription, subscription.AmountUsed)
					assert.Equal(t, wantToken, token.RemainQuota)
				})
			}
		})
	}
}
