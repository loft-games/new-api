package codex

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRelayInfo(mode string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:   123,
			ChannelType: constant.ChannelTypeCodex,
			ChannelSetting: dto.ChannelSettings{
				CodexFingerprintMode: mode,
			},
		},
	}
}

func testOAuthKey() *OAuthKey {
	return &OAuthKey{AccountID: "acc-123"}
}

func TestNormalizeFingerprintModeDefaultsToSession(t *testing.T) {
	assert.Equal(t, fingerprintModeSession, normalizeFingerprintMode(""))
	assert.Equal(t, fingerprintModeSession, normalizeFingerprintMode("invalid"))
	assert.Equal(t, fingerprintModeOff, normalizeFingerprintMode("off"))
	assert.Equal(t, fingerprintModeDevice, normalizeFingerprintMode("device"))
	assert.Equal(t, fingerprintModeFull, normalizeFingerprintMode("full"))
}

func TestDeriveStableUUIDv4(t *testing.T) {
	a := deriveStableUUIDv4("seed-a")
	b := deriveStableUUIDv4("seed-a")
	c := deriveStableUUIDv4("seed-b")

	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	parsed, err := uuid.Parse(a)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(4), parsed.Version())
}

func TestResolveInstallationIDIsIsolatedByChannelAndAccount(t *testing.T) {
	firstInfo := testRelayInfo("session")
	secondInfo := testRelayInfo("session")
	secondInfo.ChannelId = 456

	firstAccount := &OAuthKey{AccountID: "acc-123"}
	secondAccount := &OAuthKey{AccountID: "acc-456"}

	first := resolveInstallationID(firstInfo, firstAccount)
	assert.Equal(t, first, resolveInstallationID(firstInfo, firstAccount))
	assert.NotEqual(t, first, resolveInstallationID(secondInfo, firstAccount))
	assert.NotEqual(t, first, resolveInstallationID(firstInfo, secondAccount))
}

func TestPostProcessRequestBodyOffModeDoesNotRewrite(t *testing.T) {
	body := []byte(`{"model":"gpt-5-codex","prompt_cache_key":"client-cache","client_metadata":{"x-codex-installation-id":"client-install"}}`)
	generatedHeaders := http.Header{}

	result, err := postProcessRequestBody(testRelayInfo("off"), testOAuthKey(), http.Header{}, generatedHeaders, bytes.NewReader(body))
	require.NoError(t, err)
	rewritten, err := io.ReadAll(result.body)
	require.NoError(t, err)

	assert.JSONEq(t, string(body), string(rewritten))
	assert.Empty(t, generatedHeaders)
	assert.False(t, result.state.enabled)
}

func TestPostProcessRequestBodyDeviceModeConvergesInstallationOnly(t *testing.T) {
	info := testRelayInfo("device")
	info.ChannelSetting.CodexDeviceID = "configured-device"
	body := []byte(`{"model":"gpt-5-codex","prompt_cache_key":"client-cache","client_metadata":{"x-codex-installation-id":"client-install","session_id":"client-session"}}`)
	generatedHeaders := http.Header{}

	result, err := postProcessRequestBody(info, testOAuthKey(), http.Header{}, generatedHeaders, bytes.NewReader(body))
	require.NoError(t, err)
	rewritten, err := io.ReadAll(result.body)
	require.NoError(t, err)

	assert.Contains(t, string(rewritten), `"x-codex-installation-id":"configured-device"`)
	assert.Contains(t, string(rewritten), `"prompt_cache_key":"client-cache"`)
	assert.Contains(t, string(rewritten), `"session_id":"client-session"`)
	assert.Equal(t, "configured-device", generatedHeaders.Get("x-codex-installation-id"))
	assert.Contains(t, generatedHeaders.Get("x-codex-turn-metadata"), `"installation_id":"configured-device"`)
	assert.Contains(t, string(rewritten), `"x-codex-turn-metadata":"{\"installation_id\":\"configured-device\"}"`)
	assert.False(t, result.state.enabled)
}

func TestPostProcessRequestBodyFullModeUsesAccountStableThread(t *testing.T) {
	info := testRelayInfo("full")
	body := []byte(`{"model":"gpt-5-codex","client_metadata":{"session_id":"client-session","turn_id":"turn-1"}}`)
	generatedHeaders := http.Header{}

	result, err := postProcessRequestBody(info, testOAuthKey(), http.Header{}, generatedHeaders, bytes.NewReader(body))
	require.NoError(t, err)
	rewritten, err := io.ReadAll(result.body)
	require.NoError(t, err)

	expectedSession := resolveSessionID(info, testOAuthKey())
	assert.Contains(t, string(rewritten), `"session_id":"`+expectedSession+`"`)
	assert.Equal(t, expectedSession, generatedHeaders.Get("thread-id"))
	assert.Equal(t, expectedSession+":0", generatedHeaders.Get("x-codex-window-id"))
	require.True(t, result.state.enabled)
	require.Len(t, result.state.turnIDs, 1)
	assert.Equal(t, result.state.turnIDs[0].confused, generatedHeaders.Get("x-client-request-id"))
}

func TestPostProcessRequestBodySessionModeMapsPromptAndTurn(t *testing.T) {
	info := testRelayInfo("session")
	body := []byte(`{"model":"gpt-5-codex","prompt_cache_key":"client-cache","client_metadata":{"x-codex-window-id":"client-cache:0","turn_id":"turn-1","x-codex-turn-metadata":"{\"prompt_cache_key\":\"client-cache\",\"turn_id\":\"turn-1\",\"window_id\":\"client-cache:0\",\"sandbox\":\"seccomp\"}"}}`)
	clientHeaders := http.Header{}
	clientHeaders.Set("session-id", "client-session")
	clientHeaders.Set("x-codex-turn-metadata", `{"prompt_cache_key":"client-cache","turn_id":"turn-1","window_id":"client-cache:0","sandbox":"seccomp"}`)
	generatedHeaders := http.Header{}
	copyIdentityInputHeaders(generatedHeaders, clientHeaders)

	result, err := postProcessRequestBody(info, testOAuthKey(), clientHeaders, generatedHeaders, bytes.NewReader(body))
	require.NoError(t, err)
	rewritten, err := io.ReadAll(result.body)
	require.NoError(t, err)

	require.True(t, result.state.enabled)
	require.NotEmpty(t, result.state.promptCacheKey)
	require.NotEmpty(t, result.state.turnIDs)
	assert.NotContains(t, string(rewritten), "client-cache")
	assert.NotContains(t, string(rewritten), "turn-1")
	assert.Contains(t, string(rewritten), result.state.promptCacheKey)
	assert.Equal(t, result.state.promptCacheKey, generatedHeaders.Get("thread-id"))
	assert.Equal(t, result.state.promptCacheKey+":0", generatedHeaders.Get("x-codex-window-id"))
	assert.Contains(t, generatedHeaders.Get("x-codex-turn-metadata"), result.state.turnIDs[0].confused)
}

func TestResponseExposeRestoresPromptAndTurn(t *testing.T) {
	state := identityState{
		enabled:                true,
		originalPromptCacheKey: "client-cache",
		promptCacheKey:         "mapped-cache",
		turnIDs: []identityReplacement{
			{original: "turn-client", confused: "turn-mapped"},
		},
	}

	body := []byte(`{"prompt_cache_key":"mapped-cache","turn_id":"turn-mapped"}`)
	rewritten := applyResponseExpose(body, state)

	assert.JSONEq(t, `{"prompt_cache_key":"client-cache","turn_id":"turn-client"}`, string(rewritten))
}

func TestPostProcessRequestBodyPassThroughSkipsRewrite(t *testing.T) {
	info := testRelayInfo("session")
	info.ChannelSetting.PassThroughBodyEnabled = true
	body := []byte(`{"prompt_cache_key":"client-cache"}`)
	generatedHeaders := http.Header{}

	result, err := postProcessRequestBody(info, testOAuthKey(), http.Header{}, generatedHeaders, bytes.NewReader(body))
	require.NoError(t, err)
	rewritten, err := io.ReadAll(result.body)
	require.NoError(t, err)

	assert.JSONEq(t, string(body), string(rewritten))
	assert.Empty(t, generatedHeaders)
	assert.False(t, result.state.enabled)
}

func TestApplyGeneratedHeadersPreservesHeaderOverridePriority(t *testing.T) {
	info := testRelayInfo("session")
	info.ChannelMeta.HeadersOverride = map[string]interface{}{
		"x-codex-window-id": "admin-window",
		"x-admin-only":      "admin-value",
	}
	generatedHeaders := http.Header{}
	generatedHeaders.Set("x-codex-window-id", "generated-window")
	generatedHeaders.Set("session-id", "generated-session")

	applyGeneratedHeaders(info, generatedHeaders)

	require.True(t, info.UseRuntimeHeadersOverride)
	assert.Equal(t, "admin-window", info.RuntimeHeadersOverride["x-codex-window-id"])
	assert.Equal(t, "admin-value", info.RuntimeHeadersOverride["x-admin-only"])
	assert.Equal(t, "generated-session", info.RuntimeHeadersOverride["session-id"])
}
