package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	CodexAuthFormatNewAPI      = "new-api"
	CodexAuthFormatCLIProxyAPI = "CLIProxyAPI"
	CodexAuthFormatSub2API     = "sub2api"
)

var ErrUnsupportedCodexAuthFormat = errors.New("unsupported codex auth json format")

type CodexAuthJSONNormalizeResult struct {
	Format               string         `json:"format"`
	Key                  *CodexOAuthKey `json:"key"`
	Warnings             []string       `json:"warnings"`
	SuggestedChannelName string         `json:"suggested_channel_name,omitempty"`
	SuggestedProxyURL    string         `json:"suggested_proxy_url,omitempty"`
}

type CodexAuthJSONExportOptions struct {
	Format      string
	ChannelID   int
	ChannelName string
	Key         *CodexOAuthKey
	ProxyURL    string
}

type codexAuthJSONRaw map[string]any

func NormalizeCodexAuthJSON(content string) (*CodexAuthJSONNormalizeResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("codex auth json is empty")
	}

	var raw codexAuthJSONRaw
	if err := common.Unmarshal([]byte(content), &raw); err != nil {
		return nil, errors.New("invalid codex auth json")
	}

	result, ok, err := normalizeCodexAuthFromNewAPI(raw)
	if ok {
		return result, err
	}
	result, ok, err = normalizeCodexAuthFromCLIProxyAPI(raw)
	if ok {
		return result, err
	}
	result, ok, err = normalizeCodexAuthFromSub2API(raw)
	if ok {
		return result, err
	}

	return nil, ErrUnsupportedCodexAuthFormat
}

func ExportCodexAuthJSON(options CodexAuthJSONExportOptions) (any, error) {
	key, err := normalizeCodexOAuthKey(options.Key)
	if err != nil {
		return nil, err
	}

	switch options.Format {
	case CodexAuthFormatNewAPI:
		return key, nil
	case CodexAuthFormatCLIProxyAPI:
		return map[string]any{
			"id":         fmt.Sprintf("new-api-channel-%d", options.ChannelID),
			"provider":   "codex",
			"label":      options.ChannelName,
			"status":     "active",
			"disabled":   false,
			"proxy_url":  strings.TrimSpace(options.ProxyURL),
			"metadata":   key,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		}, nil
	case CodexAuthFormatSub2API:
		credentials := map[string]any{
			"access_token":       key.AccessToken,
			"refresh_token":      key.RefreshToken,
			"id_token":           key.IDToken,
			"account_id":         key.AccountID,
			"chatgpt_account_id": key.AccountID,
			"email":              key.Email,
			"expired":            key.Expired,
			"last_refresh":       key.LastRefresh,
		}
		return map[string]any{
			"name":        options.ChannelName,
			"platform":    "openai",
			"type":        "oauth",
			"credentials": compactCodexAuthMap(credentials),
			"extra":       map[string]any{},
		}, nil
	default:
		return nil, ErrUnsupportedCodexAuthFormat
	}
}

func normalizeCodexAuthFromNewAPI(raw codexAuthJSONRaw) (*CodexAuthJSONNormalizeResult, bool, error) {
	key := codexOAuthKeyFromMap(raw)
	if strings.TrimSpace(key.AccessToken) == "" && strings.TrimSpace(key.AccountID) == "" {
		return nil, false, nil
	}
	key, warnings, err := finalizeCodexOAuthKey(key)
	if err != nil {
		return nil, true, err
	}
	return &CodexAuthJSONNormalizeResult{
		Format:   CodexAuthFormatNewAPI,
		Key:      key,
		Warnings: warnings,
	}, true, nil
}

func normalizeCodexAuthFromCLIProxyAPI(raw codexAuthJSONRaw) (*CodexAuthJSONNormalizeResult, bool, error) {
	provider := strings.ToLower(valueAsString(firstValue(raw, "provider", "Provider")))
	if provider != "codex" &&
		firstValue(raw, "token_data", "TokenData") == nil &&
		firstValue(raw, "storage", "Storage") == nil &&
		firstValue(raw, "StorageJSON", "storage_json") == nil {
		return nil, false, nil
	}

	tokenSource := mapFromValue(firstValue(raw, "metadata", "Metadata"))
	if tokenData := mapFromValue(firstValue(raw, "token_data", "TokenData")); len(tokenData) > 0 {
		tokenSource = tokenData
	}
	if bundle := mapFromValue(firstValue(raw, "codex_auth_bundle", "CodexAuthBundle")); len(bundle) > 0 {
		if tokenData := mapFromValue(firstValue(bundle, "token_data", "TokenData")); len(tokenData) > 0 {
			tokenSource = tokenData
		}
		if tokenSource != nil && tokenSource["last_refresh"] == nil {
			tokenSource["last_refresh"] = firstValue(bundle, "last_refresh", "LastRefresh")
		}
	}
	if storage := mapFromValue(firstValue(raw, "storage", "Storage")); len(storage) > 0 {
		tokenSource = storage
	}
	if storageJSON, ok := storageJSONMapFromValue(firstValue(raw, "StorageJSON", "storage_json")); ok {
		tokenSource = storageJSON
	}
	if len(tokenSource) == 0 {
		return nil, false, nil
	}

	key := codexOAuthKeyFromMap(tokenSource)
	key, warnings, err := finalizeCodexOAuthKey(key)
	if err != nil {
		return nil, true, err
	}

	return &CodexAuthJSONNormalizeResult{
		Format:               CodexAuthFormatCLIProxyAPI,
		Key:                  key,
		Warnings:             warnings,
		SuggestedChannelName: valueAsString(firstValue(raw, "label", "Label")),
		SuggestedProxyURL:    valueAsString(firstValue(raw, "proxy_url", "ProxyURL")),
	}, true, nil
}

func normalizeCodexAuthFromSub2API(raw codexAuthJSONRaw) (*CodexAuthJSONNormalizeResult, bool, error) {
	credentials := mapFromValue(raw["credentials"])
	if len(credentials) == 0 {
		return nil, false, nil
	}

	key := codexOAuthKeyFromMap(credentials)
	extra := mapFromValue(raw["extra"])
	if strings.TrimSpace(key.AccountID) == "" {
		key.AccountID = firstNonEmpty(
			valueAsString(credentials["chatgpt_account_id"]),
			valueAsString(firstValue(extra, "account_id", "chatgpt_account_id")),
		)
	}
	key, warnings, err := finalizeCodexOAuthKey(key)
	if err != nil {
		return nil, true, err
	}

	return &CodexAuthJSONNormalizeResult{
		Format:               CodexAuthFormatSub2API,
		Key:                  key,
		Warnings:             warnings,
		SuggestedChannelName: firstNonEmpty(valueAsString(raw["name"]), valueAsString(raw["label"])),
		SuggestedProxyURL: firstNonEmpty(
			valueAsString(raw["proxy_url"]),
			valueAsString(raw["proxy"]),
			valueAsString(firstValue(extra, "proxy_url", "proxy")),
		),
	}, true, nil
}

func finalizeCodexOAuthKey(key *CodexOAuthKey) (*CodexOAuthKey, []string, error) {
	normalized, err := normalizeCodexOAuthKey(key)
	if err != nil {
		return key, nil, err
	}

	warnings := make([]string, 0, 2)
	if strings.TrimSpace(normalized.RefreshToken) == "" {
		warnings = append(warnings, "refresh_token is empty; automatic credential refresh will be unavailable")
	}
	if strings.TrimSpace(normalized.IDToken) == "" {
		warnings = append(warnings, "id_token is empty")
	}
	return normalized, warnings, nil
}

func normalizeCodexOAuthKey(key *CodexOAuthKey) (*CodexOAuthKey, error) {
	if key == nil {
		return nil, errors.New("codex auth key is empty")
	}
	key.AccessToken = strings.TrimSpace(key.AccessToken)
	key.RefreshToken = strings.TrimSpace(key.RefreshToken)
	key.IDToken = strings.TrimSpace(key.IDToken)
	key.AccountID = strings.TrimSpace(key.AccountID)
	key.LastRefresh = strings.TrimSpace(key.LastRefresh)
	key.Email = strings.TrimSpace(key.Email)
	key.Type = strings.TrimSpace(key.Type)
	key.Expired = strings.TrimSpace(key.Expired)

	if key.AccessToken == "" {
		return nil, errors.New("codex auth json must include access_token")
	}
	if key.AccountID == "" {
		return nil, errors.New("codex auth json must include account_id")
	}
	if key.Type == "" {
		key.Type = "codex"
	}
	return key, nil
}

func codexOAuthKeyFromMap(raw map[string]any) *CodexOAuthKey {
	return &CodexOAuthKey{
		IDToken:      valueAsString(raw["id_token"]),
		AccessToken:  valueAsString(raw["access_token"]),
		RefreshToken: valueAsString(raw["refresh_token"]),
		AccountID:    firstNonEmpty(valueAsString(raw["account_id"]), valueAsString(raw["chatgpt_account_id"])),
		LastRefresh:  valueAsString(raw["last_refresh"]),
		Email:        valueAsString(raw["email"]),
		Type:         valueAsString(raw["type"]),
		Expired:      firstNonEmpty(valueAsString(raw["expired"]), valueAsString(raw["expires_at"])),
	}
}

func compactCodexAuthMap(raw map[string]any) map[string]any {
	compacted := make(map[string]any, len(raw))
	for key, value := range raw {
		if strings.TrimSpace(valueAsString(value)) != "" {
			compacted[key] = value
		}
	}
	return compacted
}

func mapFromValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func firstValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}
	return nil
}

func storageJSONMapFromValue(value any) (map[string]any, bool) {
	raw := valueAsString(value)
	if raw == "" {
		return nil, false
	}
	if parsed, ok := parseCodexAuthJSONMap(raw); ok {
		return parsed, true
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, false
	}
	return parseCodexAuthJSONMap(string(decoded))
}

func parseCodexAuthJSONMap(raw string) (map[string]any, bool) {
	var parsed map[string]any
	if err := common.Unmarshal([]byte(raw), &parsed); err != nil || len(parsed) == 0 {
		return nil, false
	}
	return parsed, true
}

func valueAsString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
