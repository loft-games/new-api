package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexAuthJSON(t *testing.T) {
	tests := []struct {
		name                 string
		content              string
		wantFormat           string
		wantAccountID        string
		wantAccessToken      string
		wantRefreshToken     string
		wantSuggestedName    string
		wantSuggestedProxy   string
		wantWarningSubstring string
	}{
		{
			name: "new-api flat key",
			content: `{
				"access_token": " access-new ",
				"refresh_token": "refresh-new",
				"id_token": "id-new",
				"account_id": "acct-new",
				"email": "user@example.com"
			}`,
			wantFormat:       CodexAuthFormatNewAPI,
			wantAccountID:    "acct-new",
			wantAccessToken:  "access-new",
			wantRefreshToken: "refresh-new",
		},
		{
			name: "cliproxyapi metadata auth",
			content: `{
				"id": "main",
				"provider": "codex",
				"label": "Main Codex",
				"proxy_url": "http://127.0.0.1:7890",
				"metadata": {
					"access_token": "access-cli",
					"refresh_token": "refresh-cli",
					"id_token": "id-cli",
					"account_id": "acct-cli",
					"last_refresh": "2026-08-12T00:00:00Z"
				}
			}`,
			wantFormat:         CodexAuthFormatCLIProxyAPI,
			wantAccountID:      "acct-cli",
			wantAccessToken:    "access-cli",
			wantRefreshToken:   "refresh-cli",
			wantSuggestedName:  "Main Codex",
			wantSuggestedProxy: "http://127.0.0.1:7890",
		},
		{
			name: "cliproxyapi token data",
			content: `{
				"token_data": {
					"access_token": "access-token-data",
					"refresh_token": "refresh-token-data",
					"account_id": "acct-token-data"
				}
			}`,
			wantFormat:       CodexAuthFormatCLIProxyAPI,
			wantAccountID:    "acct-token-data",
			wantAccessToken:  "access-token-data",
			wantRefreshToken: "refresh-token-data",
		},
		{
			name: "cliproxyapi storage json",
			content: `{
				"Provider": "codex",
				"Label": "Storage Codex",
				"StorageJSON": "eyJhY2Nlc3NfdG9rZW4iOiJhY2Nlc3Mtc3RvcmFnZSIsInJlZnJlc2hfdG9rZW4iOiJyZWZyZXNoLXN0b3JhZ2UiLCJhY2NvdW50X2lkIjoiYWNjdC1zdG9yYWdlIn0="
			}`,
			wantFormat:        CodexAuthFormatCLIProxyAPI,
			wantAccountID:     "acct-storage",
			wantAccessToken:   "access-storage",
			wantRefreshToken:  "refresh-storage",
			wantSuggestedName: "Storage Codex",
		},
		{
			name: "sub2api credentials with chatgpt account id",
			content: `{
				"name": "Sub Account",
				"credentials": {
					"access_token": "access-sub",
					"refresh_token": "refresh-sub",
					"id_token": "id-sub",
					"chatgpt_account_id": "acct-sub",
					"email": "sub@example.com",
					"expires_at": "2026-08-13T00:00:00Z"
				}
			}`,
			wantFormat:        CodexAuthFormatSub2API,
			wantAccountID:     "acct-sub",
			wantAccessToken:   "access-sub",
			wantRefreshToken:  "refresh-sub",
			wantSuggestedName: "Sub Account",
		},
		{
			name: "missing refresh token returns warning",
			content: `{
				"access_token": "access-no-refresh",
				"account_id": "acct-no-refresh"
			}`,
			wantFormat:           CodexAuthFormatNewAPI,
			wantAccountID:        "acct-no-refresh",
			wantAccessToken:      "access-no-refresh",
			wantWarningSubstring: "refresh_token is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NormalizeCodexAuthJSON(tt.content)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.Key)

			assert.Equal(t, tt.wantFormat, result.Format)
			assert.Equal(t, tt.wantAccountID, result.Key.AccountID)
			assert.Equal(t, tt.wantAccessToken, result.Key.AccessToken)
			assert.Equal(t, tt.wantRefreshToken, result.Key.RefreshToken)
			assert.Equal(t, "codex", result.Key.Type)
			assert.Equal(t, tt.wantSuggestedName, result.SuggestedChannelName)
			assert.Equal(t, tt.wantSuggestedProxy, result.SuggestedProxyURL)
			if tt.wantWarningSubstring != "" {
				require.NotEmpty(t, result.Warnings)
				assert.Contains(t, result.Warnings[0], tt.wantWarningSubstring)
			}
		})
	}
}

func TestNormalizeCodexAuthJSONRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantErrText string
	}{
		{
			name:        "invalid json",
			content:     `{`,
			wantErrText: "invalid codex auth json",
		},
		{
			name:        "unknown format",
			content:     `{"foo":"bar"}`,
			wantErrText: ErrUnsupportedCodexAuthFormat.Error(),
		},
		{
			name: "flat key missing account id",
			content: `{
				"access_token": "access-missing-account"
			}`,
			wantErrText: "codex auth json must include account_id",
		},
		{
			name: "missing access token",
			content: `{
				"provider": "codex",
				"metadata": {"account_id": "acct-missing-access"}
			}`,
			wantErrText: "codex auth json must include access_token",
		},
		{
			name: "missing account id",
			content: `{
				"credentials": {"access_token": "access-missing-account"}
			}`,
			wantErrText: "codex auth json must include account_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeCodexAuthJSON(tt.content)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrText)
		})
	}
}

func TestExportCodexAuthJSON(t *testing.T) {
	key := &CodexOAuthKey{
		IDToken:      "id",
		AccessToken:  "access",
		RefreshToken: "refresh",
		AccountID:    "acct",
		LastRefresh:  "2026-08-12T00:00:00Z",
		Email:        "user@example.com",
		Expired:      "2026-08-13T00:00:00Z",
	}

	t.Run("new-api", func(t *testing.T) {
		exported, err := ExportCodexAuthJSON(CodexAuthJSONExportOptions{
			Format: CodexAuthFormatNewAPI,
			Key:    key,
		})
		require.NoError(t, err)

		exportedKey, ok := exported.(*CodexOAuthKey)
		require.True(t, ok)
		assert.Equal(t, "access", exportedKey.AccessToken)
		assert.Equal(t, "acct", exportedKey.AccountID)
		assert.Equal(t, "codex", exportedKey.Type)
	})

	t.Run("CLIProxyAPI", func(t *testing.T) {
		exported, err := ExportCodexAuthJSON(CodexAuthJSONExportOptions{
			Format:      CodexAuthFormatCLIProxyAPI,
			ChannelID:   42,
			ChannelName: "Main Codex",
			Key:         key,
			ProxyURL:    "http://127.0.0.1:7890",
		})
		require.NoError(t, err)

		exportedMap, ok := exported.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "codex", exportedMap["provider"])
		assert.Equal(t, "Main Codex", exportedMap["label"])
		assert.Equal(t, "http://127.0.0.1:7890", exportedMap["proxy_url"])
		require.NotNil(t, exportedMap["metadata"])
	})

	t.Run("sub2api", func(t *testing.T) {
		exported, err := ExportCodexAuthJSON(CodexAuthJSONExportOptions{
			Format:      CodexAuthFormatSub2API,
			ChannelName: "Main Codex",
			Key:         key,
		})
		require.NoError(t, err)

		exportedMap, ok := exported.(map[string]any)
		require.True(t, ok)
		credentials, ok := exportedMap["credentials"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "access", credentials["access_token"])
		assert.Equal(t, "acct", credentials["account_id"])
		assert.Equal(t, "acct", credentials["chatgpt_account_id"])
	})

	t.Run("unsupported format", func(t *testing.T) {
		_, err := ExportCodexAuthJSON(CodexAuthJSONExportOptions{
			Format: "unknown",
			Key:    key,
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnsupportedCodexAuthFormat))
	})
}
