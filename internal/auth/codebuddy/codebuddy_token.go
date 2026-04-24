// Package codebuddy provides authentication and token management functionality
// for Tencent Cloud CodeBuddy AI services. It handles OAuth2 token storage, serialization,
// and retrieval for maintaining authenticated sessions with the CodeBuddy API.
package codebuddy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
)

// CodeBuddyTokenStorage stores OAuth2 token information for Tencent CodeBuddy API authentication.
// It maintains compatibility with the existing auth system while adding CodeBuddy-specific fields
// for managing access tokens, refresh tokens, and user account information.
type CodeBuddyTokenStorage struct {
	// BearerToken is the OAuth2 access token used for authenticating API requests.
	BearerToken string `json:"bearer_token"`
	// AccessToken is an alias for BearerToken (alternative field name from API).
	AccessToken string `json:"access_token,omitempty"`
	// RefreshToken is used to obtain new access tokens when the current one expires.
	RefreshToken string `json:"refresh_token,omitempty"`
	// TokenType indicates the type of token, typically "Bearer".
	TokenType string `json:"token_type,omitempty"`
	// ExpiresIn is the duration in seconds until the access token expires.
	ExpiresIn int `json:"expires_in,omitempty"`
	// Scope defines the permissions granted for this token.
	Scope string `json:"scope,omitempty"`
	// Domain is the authentication domain.
	Domain string `json:"domain,omitempty"`
	// SessionState is the session identifier from the OAuth provider.
	SessionState string `json:"session_state,omitempty"`
	// UserID is the user identifier (typically email or username).
	UserID string `json:"user_id"`
	// Email is the CodeBuddy account email address associated with this token.
	Email string `json:"email,omitempty"`
	// CreatedAt is the Unix timestamp when the token was created.
	CreatedAt int64 `json:"created_at"`
	// UserInfo contains additional user information from JWT.
	UserInfo map[string]interface{} `json:"user_info,omitempty"`
	// Type indicates the authentication provider type, always "codebuddy" for this storage.
	Type string `json:"type"`
	// LastRefresh is the timestamp of the last token refresh operation.
	LastRefresh string `json:"last_refresh,omitempty"`

	// Metadata holds arbitrary key-value pairs injected via hooks.
	// It is not exported to JSON directly to allow flattening during serialization.
	Metadata map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata into the storage before saving.
func (ts *CodeBuddyTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the CodeBuddy token storage to a JSON file.
// This method creates the necessary directory structure and writes the token
// data in JSON format to the specified file path for persistent storage.
// It merges any injected metadata into the top-level JSON object.
//
// Parameters:
//   - authFilePath: The full path where the token file should be saved
//
// Returns:
//   - error: An error if the operation fails, nil otherwise
func (ts *CodeBuddyTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "codebuddy"
	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	// Merge metadata using helper
	data, errMerge := misc.MergeMetadata(ts, ts.Metadata)
	if errMerge != nil {
		return fmt.Errorf("failed to merge metadata: %w", errMerge)
	}

	if err = json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}
