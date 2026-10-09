package github

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

// AppConfig is the credentials for one GitHub App installation. The private
// key is the App's PEM; AppID identifies the App; InstallationID identifies
// the specific repo/org installation whose token we mint.
type AppConfig struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPEM  []byte
}

// NewInstallationClient returns a Client authenticated as a GitHub App
// installation. ghinstallation mints and refreshes the short-lived
// installation token under the hood (JWT signed with the App key → installation
// access token), injecting Authorization on each request; the Client carries no
// static token. This is the unattended-path counterpart to FromEnv's PAT.
func NewInstallationClient(cfg AppConfig) (*Client, error) {
	if cfg.AppID == 0 || cfg.InstallationID == 0 || len(cfg.PrivateKeyPEM) == 0 {
		return nil, fmt.Errorf("github: AppConfig needs AppID, InstallationID and PrivateKeyPEM")
	}
	tr, err := ghinstallation.New(http.DefaultTransport, cfg.AppID, cfg.InstallationID, cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("github: installation transport: %w", err)
	}
	return &Client{
		HTTP: &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}, nil
}
