package codebuddy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

const (
	// CodeBuddyBaseURL is the base URL for CodeBuddy API.
	CodeBuddyBaseURL = "https://www.codebuddy.cn"
	// CodeBuddyAuthStateEndpoint is the URL for initiating the OAuth 2.0 device authorization flow.
	CodeBuddyAuthStateEndpoint = CodeBuddyBaseURL + "/v2/plugin/auth/state"
	// CodeBuddyAuthTokenEndpoint is the URL for polling the token after user authorization.
	CodeBuddyAuthTokenEndpoint = CodeBuddyBaseURL + "/v2/plugin/auth/token"
	// CodeBuddyAuthPlatform is the platform identifier for CLI authentication.
	CodeBuddyAuthPlatform = "CLI"
)

// AuthStateResponse represents the response from the auth state endpoint.
type AuthStateResponse struct {
	Code int `json:"code"`
	Data struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	} `json:"data"`
	Msg string `json:"msg"`
}

// TokenResponse represents the response from the token endpoint.
type TokenResponse struct {
	Code int `json:"code"`
	Data struct {
		AccessToken  string `json:"accessToken"`
		TokenType    string `json:"tokenType"`
		ExpiresIn    int    `json:"expiresIn"`
		RefreshToken string `json:"refreshToken"`
		SessionState string `json:"sessionState"`
		Scope        string `json:"scope"`
		Domain       string `json:"domain"`
	} `json:"data"`
	Msg string `json:"msg"`
}

// DeviceFlow represents the device flow authentication details.
type DeviceFlow struct {
	// State is the unique identifier for this authentication session.
	State string `json:"state"`
	// AuthURL is the URL where the user can authorize the device.
	AuthURL string `json:"auth_url"`
	// VerificationURI is the base verification URI.
	VerificationURI string `json:"verification_uri"`
	// ExpiresIn is the time in seconds until the state expires.
	ExpiresIn int `json:"expires_in"`
}

// CodeBuddyAuth manages authentication and token handling for the CodeBuddy API.
type CodeBuddyAuth struct {
	httpClient *http.Client
}

// NewCodeBuddyAuth creates a new CodeBuddyAuth instance with a proxy-configured HTTP client.
func NewCodeBuddyAuth(cfg *config.Config) *CodeBuddyAuth {
	return &CodeBuddyAuth{
		httpClient: util.SetProxy(&cfg.SDKConfig, &http.Client{}),
	}
}

// generateRequestID generates a random request ID for API calls.
func generateRequestID() string {
	uuid := make([]byte, 16)
	rand.Read(uuid)
	return fmt.Sprintf("%x", uuid)
}

// getCommonHeaders returns common headers required for CodeBuddy API calls.
func getCommonHeaders() map[string]string {
	requestID := generateRequestID()
	return map[string]string{
		"Host":                 "www.codebuddy.cn",
		"Accept":               "application/json, text/plain, */*",
		"Content-Type":         "application/json",
		"Cache-Control":        "no-cache",
		"Pragma":               "no-cache",
		"Connection":           "close",
		"X-Requested-With":     "XMLHttpRequest",
		"X-Domain":             "www.codebuddy.cn",
		"X-No-Authorization":   "true",
		"X-No-User-Id":         "true",
		"X-No-Enterprise-Id":   "true",
		"X-No-Department-Info": "true",
		"User-Agent":           "CLI/1.0.8 CodeBuddy/1.0.8",
		"X-Product":            "SaaS",
		"X-Request-ID":         requestID,
	}
}

// InitiateDeviceFlow starts the OAuth 2.0 device authorization flow and returns the device flow details.
func (ca *CodeBuddyAuth) InitiateDeviceFlow(ctx context.Context) (*DeviceFlow, error) {
	headers := getCommonHeaders()

	// Generate nonce for uniqueness
	nonce := generateNonce()
	stateURL := fmt.Sprintf("%s?platform=%s&nonce=%s", CodeBuddyAuthStateEndpoint, CodeBuddyAuthPlatform, nonce)

	// Create request body
	body := fmt.Sprintf(`{"nonce":"%s"}`, nonce)

	req, err := http.NewRequestWithContext(ctx, "POST", stateURL, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create state request: %w", err)
	}

	// Set headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := ca.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("state request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("state request failed: %d %s. Response: %s", resp.StatusCode, resp.Status, string(respBody))
	}

	var result AuthStateResponse
	if err = json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse state response: %w", err)
	}

	if result.Code != 0 || result.Data.State == "" {
		return nil, fmt.Errorf("state request failed: %s (code: %d)", result.Msg, result.Code)
	}

	return &DeviceFlow{
		State:           result.Data.State,
		AuthURL:         result.Data.AuthURL,
		VerificationURI: CodeBuddyBaseURL,
		ExpiresIn:       1800, // 30 minutes default
	}, nil
}

// generateNonce generates a random nonce string.
func generateNonce() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return fmt.Sprintf("%x", bytes)
}

// PollForToken polls the token endpoint with the state to obtain an access token.
func (ca *CodeBuddyAuth) PollForToken(state string) (*CodeBuddyTokenStorage, error) {
	pollInterval := 5 * time.Second
	maxAttempts := 60 // 5 minutes max

	for attempt := 0; attempt < maxAttempts; attempt++ {
		tokenURL := fmt.Sprintf("%s?state=%s", CodeBuddyAuthTokenEndpoint, state)

		req, err := http.NewRequest("GET", tokenURL, nil)
		if err != nil {
			log.Warnf("Failed to create token request: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		// Set headers for token polling
		headers := getCommonHeaders()
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := ca.httpClient.Do(req)
		if err != nil {
			log.Warnf("Token poll attempt %d/%d failed: %v", attempt+1, maxAttempts, err)
			time.Sleep(pollInterval)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			log.Warnf("Failed to read response body: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Warnf("Token poll attempt %d/%d failed: %d", attempt+1, maxAttempts, resp.StatusCode)
			time.Sleep(pollInterval)
			continue
		}

		var result TokenResponse
		if err = json.Unmarshal(body, &result); err != nil {
			log.Warnf("Failed to parse token response: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		// Check for pending status (code 11217)
		if result.Code == 11217 {
			fmt.Printf("Polling attempt %d/%d...\n\n", attempt+1, maxAttempts)
			time.Sleep(pollInterval)
			continue
		}

		// Check for success
		if result.Code == 0 && result.Data.AccessToken != "" {
			// Parse user info from JWT token
			userID, userInfo := parseJWTToken(result.Data.AccessToken)

			tokenStorage := &CodeBuddyTokenStorage{
				BearerToken:  result.Data.AccessToken,
				AccessToken:  result.Data.AccessToken,
				RefreshToken: result.Data.RefreshToken,
				TokenType:    result.Data.TokenType,
				ExpiresIn:    result.Data.ExpiresIn,
				Scope:        result.Data.Scope,
				Domain:       result.Data.Domain,
				SessionState: result.Data.SessionState,
				UserID:       userID,
				UserInfo:     userInfo,
				CreatedAt:    time.Now().Unix(),
				LastRefresh:  time.Now().Format(time.RFC3339),
			}

			return tokenStorage, nil
		}

		// Other error codes
		if result.Code != 0 {
			log.Warnf("Token poll returned error code %d: %s", result.Code, result.Msg)
		}

		time.Sleep(pollInterval)
	}

	return nil, fmt.Errorf("authentication timeout. Please restart the authentication process")
}

// parseJWTToken parses the JWT token to extract user information.
func parseJWTToken(token string) (string, map[string]interface{}) {
	userID := "unknown"
	userInfo := make(map[string]interface{})

	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return userID, userInfo
	}

	// Decode the payload part (second part of JWT)
	payload := parts[1]

	// Add padding if needed
	if l := len(payload) % 4; l > 0 {
		payload += strings.Repeat("=", 4-l)
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try raw URLEncoding
		decoded, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return userID, userInfo
		}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return userID, userInfo
	}

	// Extract user ID (prefer email)
	if email, ok := claims["email"].(string); ok && email != "" {
		userID = email
	} else if preferredUsername, ok := claims["preferred_username"].(string); ok && preferredUsername != "" {
		userID = preferredUsername
	} else if sub, ok := claims["sub"].(string); ok && sub != "" {
		userID = sub
	}

	userInfo = claims

	return userID, userInfo
}

// RefreshTokens attempts to refresh the access token using a refresh token.
// Note: CodeBuddy may not support refresh tokens, this is a placeholder for future use.
func (ca *CodeBuddyAuth) RefreshTokens(ctx context.Context, refreshToken string) (*CodeBuddyTokenStorage, error) {
	// CodeBuddy's current OAuth implementation may not support refresh tokens
	// This is a placeholder for future implementation
	return nil, fmt.Errorf("token refresh not supported by CodeBuddy, please re-authenticate")
}

// CreateTokenStorage creates a CodeBuddyTokenStorage object from token data.
func (ca *CodeBuddyAuth) CreateTokenStorage(tokenData *CodeBuddyTokenStorage) *CodeBuddyTokenStorage {
	tokenStorage := &CodeBuddyTokenStorage{
		BearerToken:  tokenData.BearerToken,
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		TokenType:    tokenData.TokenType,
		ExpiresIn:    tokenData.ExpiresIn,
		Scope:        tokenData.Scope,
		Domain:       tokenData.Domain,
		SessionState: tokenData.SessionState,
		UserID:       tokenData.UserID,
		UserInfo:     tokenData.UserInfo,
		CreatedAt:    tokenData.CreatedAt,
		LastRefresh:  time.Now().Format(time.RFC3339),
	}

	return tokenStorage
}

// UpdateTokenStorage updates an existing token storage with new token data.
func (ca *CodeBuddyAuth) UpdateTokenStorage(storage *CodeBuddyTokenStorage, tokenData *CodeBuddyTokenStorage) {
	storage.BearerToken = tokenData.BearerToken
	storage.AccessToken = tokenData.AccessToken
	storage.RefreshToken = tokenData.RefreshToken
	storage.TokenType = tokenData.TokenType
	storage.ExpiresIn = tokenData.ExpiresIn
	storage.Scope = tokenData.Scope
	storage.Domain = tokenData.Domain
	storage.SessionState = tokenData.SessionState
	storage.UserID = tokenData.UserID
	storage.UserInfo = tokenData.UserInfo
	storage.CreatedAt = tokenData.CreatedAt
	storage.LastRefresh = time.Now().Format(time.RFC3339)
}

// GetCodeBuddyHeaders generates headers for CodeBuddy API requests.
func GetCodeBuddyHeaders(bearerToken string, userID string) map[string]string {
	requestID := generateRequestID()
	conversationID := generateRequestID()

	return map[string]string{
		"Host":                        "www.codebuddy.cn",
		"Accept":                      "application/json",
		"Content-Type":                "application/json",
		"X-Requested-With":            "XMLHttpRequest",
		"x-stainless-arch":            "x64",
		"x-stainless-lang":            "js",
		"x-stainless-os":              "Windows",
		"x-stainless-package-version": "5.10.1",
		"x-stainless-retry-count":     "0",
		"x-stainless-runtime":         "node",
		"x-stainless-runtime-version": "v22.13.1",
		"X-Conversation-ID":           conversationID,
		"X-Conversation-Request-ID":   generateRequestID(),
		"X-Conversation-Message-ID":   generateRequestID(),
		"X-Request-ID":                requestID,
		"X-Agent-Intent":              "craft",
		"X-IDE-Type":                  "CLI",
		"X-IDE-Name":                  "CLI",
		"X-IDE-Version":               "1.0.7",
		"Authorization":               fmt.Sprintf("Bearer %s", bearerToken),
		"X-Domain":                    "www.codebuddy.cn",
		"User-Agent":                  "CLI/1.0.7 CodeBuddy/1.0.7",
		"X-Product":                   "SaaS",
		"X-User-Id":                   userID,
	}
}

// IsTokenExpired checks if the token has expired.
func IsTokenExpired(storage *CodeBuddyTokenStorage) bool {
	if storage.CreatedAt == 0 || storage.ExpiresIn == 0 {
		return false // Unknown expiration, assume valid
	}

	expiryTime := storage.CreatedAt + int64(storage.ExpiresIn)
	// Add 5 minute buffer before actual expiry
	bufferTime := int64(300)
	return time.Now().Unix() >= (expiryTime - bufferTime)
}

// RefreshTokensWithRetry attempts to refresh tokens with a specified number of retries upon failure.
func (ca *CodeBuddyAuth) RefreshTokensWithRetry(ctx context.Context, refreshToken string, maxRetries int) (*CodeBuddyTokenStorage, error) {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		tokenData, err := ca.RefreshTokens(ctx, refreshToken)
		if err == nil {
			return tokenData, nil
		}

		lastErr = err
		log.Warnf("Token refresh attempt %d failed: %v", attempt+1, err)
	}

	return nil, fmt.Errorf("token refresh failed after %d attempts: %w", maxRetries, lastErr)
}
