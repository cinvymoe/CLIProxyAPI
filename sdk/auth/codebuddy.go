package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// CodeBuddyAuthenticator implements the device flow login for Tencent CodeBuddy accounts.
type CodeBuddyAuthenticator struct{}

// NewCodeBuddyAuthenticator constructs a CodeBuddy authenticator.
func NewCodeBuddyAuthenticator() *CodeBuddyAuthenticator {
	return &CodeBuddyAuthenticator{}
}

func (a *CodeBuddyAuthenticator) Provider() string {
	return "codebuddy"
}

func (a *CodeBuddyAuthenticator) RefreshLead() *time.Duration {
	return new(20 * time.Minute)
}

func (a *CodeBuddyAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	authSvc := codebuddy.NewCodeBuddyAuth(cfg)

	deviceFlow, err := authSvc.InitiateDeviceFlow(ctx)
	if err != nil {
		return nil, fmt.Errorf("codebuddy device flow initiation failed: %w", err)
	}

	authURL := deviceFlow.AuthURL

	if !opts.NoBrowser {
		fmt.Println("Opening browser for CodeBuddy authentication")
		if !browser.IsAvailable() {
			log.Warn("No browser available; please open the URL manually")
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		} else if err = browser.OpenURL(authURL); err != nil {
			log.Warnf("Failed to open browser automatically: %v", err)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		}
	} else {
		fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
	}

	fmt.Println("Waiting for CodeBuddy authentication...")

	tokenStorage, err := authSvc.PollForToken(deviceFlow.State)
	if err != nil {
		return nil, fmt.Errorf("codebuddy authentication failed: %w", err)
	}

	email := ""
	if opts.Metadata != nil {
		email = opts.Metadata["email"]
		if email == "" {
			email = opts.Metadata["alias"]
		}
	}

	// Try to get email from token info
	if email == "" && tokenStorage.UserInfo != nil {
		if e, ok := tokenStorage.UserInfo["email"].(string); ok && e != "" {
			email = e
		} else if pu, ok := tokenStorage.UserInfo["preferred_username"].(string); ok && pu != "" {
			email = pu
		}
	}

	if email == "" {
		email = tokenStorage.UserID
	}

	if email == "" && opts.Prompt != nil {
		email, err = opts.Prompt("Please input your email address or alias for CodeBuddy:")
		if err != nil {
			return nil, err
		}
	}

	email = strings.TrimSpace(email)
	if email == "" {
		email = "codebuddy-user"
	}

	tokenStorage.Email = email

	fileName := fmt.Sprintf("codebuddy-%s.json", tokenStorage.Email)
	metadata := map[string]any{
		"email":   tokenStorage.Email,
		"user_id": tokenStorage.UserID,
	}

	fmt.Println("CodeBuddy authentication successful")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}
