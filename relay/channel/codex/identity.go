package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/google/uuid"
)

type fingerprintMode string

const (
	fingerprintModeOff     fingerprintMode = "off"
	fingerprintModeDevice  fingerprintMode = "device"
	fingerprintModeSession fingerprintMode = "session"
	fingerprintModeFull    fingerprintMode = "full"

	identityStateContextKey = "codex_identity_state"
)

type identityReplacement struct {
	original string
	confused string
}

type identityState struct {
	enabled                bool
	originalPromptCacheKey string
	promptCacheKey         string
	turnIDs                []identityReplacement
}

type fingerprintIDs struct {
	mode           fingerprintMode
	installationID string
	sessionID      string
	threadID       string
	turnID         string
	windowID       string
}

type requestPostProcessResult struct {
	body  io.Reader
	state identityState
}

func normalizeFingerprintMode(mode string) fingerprintMode {
	switch fingerprintMode(strings.ToLower(strings.TrimSpace(mode))) {
	case fingerprintModeOff:
		return fingerprintModeOff
	case fingerprintModeDevice:
		return fingerprintModeDevice
	case fingerprintModeFull:
		return fingerprintModeFull
	default:
		return fingerprintModeSession
	}
}

func deriveStableUUIDv4(seed string) string {
	h := sha256.Sum256([]byte(seed))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

func identitySeed(info *relaycommon.RelayInfo, oauthKey *OAuthKey) string {
	channelID := 0
	if info != nil {
		channelID = info.ChannelId
	}
	accountID := ""
	if oauthKey != nil {
		accountID = strings.TrimSpace(oauthKey.AccountID)
	}
	return fmt.Sprintf("new-api:codex:identity:v1:%d:%s", channelID, accountID)
}

func resolveInstallationID(info *relaycommon.RelayInfo, oauthKey *OAuthKey) string {
	if info != nil {
		deviceID := strings.TrimSpace(info.ChannelSetting.CodexDeviceID)
		if deviceID != "" {
			return deviceID
		}
	}
	return deriveStableUUIDv4(identitySeed(info, oauthKey) + ":installation")
}

func resolveSessionID(info *relaycommon.RelayInfo, oauthKey *OAuthKey) string {
	return deriveStableUUIDv4(identitySeed(info, oauthKey) + ":session")
}

func resolveThreadID(info *relaycommon.RelayInfo, oauthKey *OAuthKey, clientSessionID string) string {
	clientSessionID = strings.TrimSpace(clientSessionID)
	if clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv4(identitySeed(info, oauthKey) + ":thread:" + clientSessionID)
}

func resolveFingerprintIDs(info *relaycommon.RelayInfo, oauthKey *OAuthKey, headers http.Header, body map[string]any) *fingerprintIDs {
	if info == nil {
		return nil
	}
	mode := normalizeFingerprintMode(info.ChannelSetting.CodexFingerprintMode)
	if mode == fingerprintModeOff {
		return nil
	}

	ids := &fingerprintIDs{
		mode:           mode,
		installationID: resolveInstallationID(info, oauthKey),
	}
	if mode == fingerprintModeDevice {
		return ids
	}

	clientSessionID := extractClientSessionID(headers)
	if clientSessionID == "" {
		clientSessionID = firstString(body, "prompt_cache_key")
	}
	ids.sessionID = resolveSessionID(info, oauthKey)
	if mode == fingerprintModeFull {
		ids.threadID = ids.sessionID
	} else {
		ids.threadID = resolveThreadID(info, oauthKey, clientSessionID)
		if ids.threadID == "" {
			ids.threadID = ids.sessionID
		}
	}
	ids.turnID = uuid.NewString()
	ids.windowID = ids.threadID + ":0"
	return ids
}

func extractClientSessionID(headers http.Header) string {
	if headers == nil {
		return ""
	}
	if value := strings.TrimSpace(headers.Get("session-id")); value != "" {
		return value
	}
	return strings.TrimSpace(headers.Get("session_id"))
}

func firstString(body map[string]any, path string) string {
	if body == nil || path == "" {
		return ""
	}
	parts := strings.Split(path, ".")
	var current any = body
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[part]
	}
	value, ok := current.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func setString(body map[string]any, path string, value string) {
	if body == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	current := body
	for _, part := range parts[:len(parts)-1] {
		next, _ := current[part].(map[string]any)
		if next == nil {
			next = make(map[string]any)
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func ensureClientMetadata(body map[string]any) map[string]any {
	existing, _ := body["client_metadata"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
		body["client_metadata"] = existing
	}
	return existing
}

func applyFingerprintToBody(body map[string]any, ids *fingerprintIDs) {
	if body == nil || ids == nil {
		return
	}
	clientMetadata := ensureClientMetadata(body)
	clientMetadata["x-codex-installation-id"] = ids.installationID

	if ids.mode == fingerprintModeDevice {
		rewriteTurnMetadataString(clientMetadata, map[string]any{
			"installation_id": ids.installationID,
		})
		return
	}

	clientMetadata["session_id"] = ids.sessionID
	clientMetadata["thread_id"] = ids.threadID
	clientMetadata["turn_id"] = ids.turnID
	clientMetadata["x-codex-window-id"] = ids.windowID
	rewriteTurnMetadataString(clientMetadata, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
	})
}

func applyFingerprintToHeaders(headers http.Header, ids *fingerprintIDs) {
	if headers == nil || ids == nil {
		return
	}
	headers.Set("x-codex-installation-id", ids.installationID)
	if ids.mode == fingerprintModeDevice {
		rewriteHeaderTurnMetadata(headers, map[string]any{
			"installation_id": ids.installationID,
		})
		return
	}
	headers.Set("x-codex-window-id", ids.windowID)
	headers.Set("x-client-request-id", ids.turnID)
	headers.Set("session-id", ids.sessionID)
	headers.Set("session_id", ids.sessionID)
	headers.Set("thread-id", ids.threadID)
	rewriteHeaderTurnMetadata(headers, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
	})
}

func rewriteHeaderTurnMetadata(headers http.Header, fields map[string]any) {
	if headers == nil || len(fields) == 0 {
		return
	}
	if rebuilt, ok := rewriteTurnMetadataJSON(headers.Get("x-codex-turn-metadata"), fields); ok {
		headers.Set("x-codex-turn-metadata", rebuilt)
	}
}

func rewriteTurnMetadataString(container map[string]any, fields map[string]any) {
	if container == nil || len(fields) == 0 {
		return
	}
	raw, _ := container["x-codex-turn-metadata"].(string)
	if rebuilt, ok := rewriteTurnMetadataJSON(raw, fields); ok {
		container["x-codex-turn-metadata"] = rebuilt
	}
}

func rewriteTurnMetadataJSON(raw string, fields map[string]any) (string, bool) {
	metadata := make(map[string]any)
	if strings.TrimSpace(raw) != "" {
		if err := common.UnmarshalJsonStr(raw, &metadata); err != nil {
			metadata = make(map[string]any)
		}
	}
	for key, value := range fields {
		metadata[key] = value
	}
	rebuilt, err := common.Marshal(metadata)
	if err != nil {
		return "", false
	}
	return string(rebuilt), true
}

func applyIdentityConfuseToBody(info *relaycommon.RelayInfo, oauthKey *OAuthKey, body map[string]any) identityState {
	state := identityState{}
	promptCacheKey := firstString(body, "prompt_cache_key")
	if promptCacheKey != "" {
		state.enabled = true
		state.originalPromptCacheKey = promptCacheKey
		state.promptCacheKey = confuseUUID(info, oauthKey, "prompt-cache", promptCacheKey)
		body["prompt_cache_key"] = state.promptCacheKey
	}

	if rawTurnMetadata := firstString(body, "client_metadata.x-codex-turn-metadata"); rawTurnMetadata != "" {
		state.enabled = true
		updated := applyTurnMetadataIdentityConfuse(rawTurnMetadata, &state, info, oauthKey)
		setString(body, "client_metadata.x-codex-turn-metadata", updated)
	}

	if turnID := firstString(body, "client_metadata.turn_id"); turnID != "" {
		state.enabled = true
		setString(body, "client_metadata.turn_id", state.confuseTurnID(info, oauthKey, turnID))
	}

	if state.promptCacheKey != "" && firstString(body, "client_metadata.x-codex-window-id") != "" {
		setString(body, "client_metadata.x-codex-window-id", state.promptCacheKey+":0")
	}
	return state
}

func applyIdentityConfuseToHeaders(headers http.Header, state *identityState, info *relaycommon.RelayInfo, oauthKey *OAuthKey) {
	if headers == nil || state == nil || !state.enabled {
		return
	}
	if rawTurnMetadata := strings.TrimSpace(headers.Get("x-codex-turn-metadata")); rawTurnMetadata != "" {
		headers.Set("x-codex-turn-metadata", applyTurnMetadataIdentityConfuse(rawTurnMetadata, state, info, oauthKey))
	}
}

func applyTurnMetadataIdentityConfuse(rawTurnMetadata string, state *identityState, info *relaycommon.RelayInfo, oauthKey *OAuthKey) string {
	if state == nil || !state.enabled {
		return rawTurnMetadata
	}
	var metadata map[string]any
	if err := common.UnmarshalJsonStr(rawTurnMetadata, &metadata); err != nil {
		return rawTurnMetadata
	}
	if state.promptCacheKey != "" {
		if _, ok := metadata["prompt_cache_key"]; ok {
			metadata["prompt_cache_key"] = state.promptCacheKey
		}
		if _, ok := metadata["window_id"]; ok {
			metadata["window_id"] = state.promptCacheKey + ":0"
		}
	}
	if turnID, ok := metadata["turn_id"].(string); ok && strings.TrimSpace(turnID) != "" {
		metadata["turn_id"] = state.confuseTurnID(info, oauthKey, turnID)
	}
	rebuilt, err := common.Marshal(metadata)
	if err != nil {
		return rawTurnMetadata
	}
	return string(rebuilt)
}

func (state *identityState) confuseTurnID(info *relaycommon.RelayInfo, oauthKey *OAuthKey, turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if state == nil || !state.enabled || turnID == "" {
		return turnID
	}
	for _, replacement := range state.turnIDs {
		if replacement.original == turnID || replacement.confused == turnID {
			return replacement.confused
		}
	}
	confused := confuseUUID(info, oauthKey, "turn", turnID)
	state.turnIDs = append(state.turnIDs, identityReplacement{original: turnID, confused: confused})
	return confused
}

func confuseUUID(info *relaycommon.RelayInfo, oauthKey *OAuthKey, kind string, value string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(identitySeed(info, oauthKey)+":confuse:"+kind+":"+strings.TrimSpace(value))).String()
}

func applyResponseExpose(payload []byte, state identityState) []byte {
	payload = replaceResponseValue(payload, state.promptCacheKey, state.originalPromptCacheKey)
	for _, replacement := range state.turnIDs {
		payload = replaceResponseValue(payload, replacement.confused, replacement.original)
	}
	return payload
}

func replaceResponseValue(payload []byte, from string, to string) []byte {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if len(payload) == 0 || from == "" || to == "" || from == to || !bytes.Contains(payload, []byte(from)) {
		return payload
	}
	return bytes.ReplaceAll(payload, []byte(from), []byte(to))
}

func postProcessRequestBody(info *relaycommon.RelayInfo, oauthKey *OAuthKey, clientHeaders http.Header, generatedHeaders http.Header, requestBody io.Reader) (*requestPostProcessResult, error) {
	if requestBody == nil || info == nil || info.ChannelSetting.PassThroughBodyEnabled {
		return &requestPostProcessResult{body: requestBody}, nil
	}
	mode := normalizeFingerprintMode(info.ChannelSetting.CodexFingerprintMode)
	if mode == fingerprintModeOff {
		return &requestPostProcessResult{body: requestBody}, nil
	}

	raw, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &requestPostProcessResult{body: bytes.NewReader(raw)}, nil
	}

	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	state := identityState{}
	ids := resolveFingerprintIDs(info, oauthKey, clientHeaders, body)
	if ids != nil && ids.mode != fingerprintModeDevice {
		state = applyIdentityConfuseToBody(info, oauthKey, body)
		alignFingerprintIDsWithIdentityState(ids, state)
	}
	applyFingerprintToBody(body, ids)
	applyFingerprintToHeaders(generatedHeaders, ids)
	applyIdentityConfuseToHeaders(generatedHeaders, &state, info, oauthKey)

	rewritten, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &requestPostProcessResult{
		body:  bytes.NewReader(rewritten),
		state: state,
	}, nil
}

func alignFingerprintIDsWithIdentityState(ids *fingerprintIDs, state identityState) {
	if ids == nil {
		return
	}
	if ids.mode == fingerprintModeSession && state.promptCacheKey != "" {
		ids.threadID = state.promptCacheKey
		ids.windowID = state.promptCacheKey + ":0"
	}
	if len(state.turnIDs) > 0 {
		ids.turnID = state.turnIDs[0].confused
	}
}
