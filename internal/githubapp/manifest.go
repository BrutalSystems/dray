package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Manifest is a GitHub App manifest: the settings of an App created through
// GitHub's manifest flow, where a person confirms on github.com and GitHub
// hands the new App's credentials back once, by a short-lived code.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

// HookAttributes is the App's webhook.
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// DrayManifest is the App Dray needs to deploy on merge: read code and CI
// runs, write check runs, and hear when workflow runs finish. Nothing more.
func DrayManifest(name, homepage, webhookURL, redirectURL string, webhookActive bool) Manifest {
	return Manifest{
		Name:           name,
		URL:            homepage,
		HookAttributes: HookAttributes{URL: webhookURL, Active: webhookActive},
		RedirectURL:    redirectURL,
		DefaultPermissions: map[string]string{
			"contents": "read",
			"actions":  "read",
			"checks":   "write",
			"metadata": "read",
		},
		DefaultEvents: []string{"workflow_run"},
	}
}

// NewAppURL is where an org's admin confirms a manifest.
func NewAppURL(org, state string) string {
	return "https://github.com/organizations/" + url.PathEscape(org) + "/settings/apps/new?state=" + url.QueryEscape(state)
}

// Created is what GitHub returns for a new App, once. PEM and WebhookSecret
// are secrets: they go to the secret store and are never printed.
type Created struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	HTMLURL       string `json:"html_url"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
}

// Convert exchanges the code from the manifest flow for the new App's
// credentials. The code works once, for an hour.
func Convert(ctx context.Context, client *http.Client, baseURL, code string) (*Created, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		return nil, &apiError{Status: resp.Status, Message: m.Message}
	}
	var c Created
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("reading the new App: %w", err)
	}
	if c.ID == 0 || c.PEM == "" {
		return nil, fmt.Errorf("GitHub returned no App ID or key")
	}
	return &c, nil
}
