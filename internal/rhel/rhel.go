package rhel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultTokenURL = "https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token"
	defaultAPIURL   = "https://console.redhat.com/api/rhsm/v2"
)

// Client interacts with the Red Hat Hybrid Cloud Console API.
type Client struct {
	clientID     string
	clientSecret string
	tokenURL     string
	apiURL       string
	httpClient   *http.Client
}

// NewClient creates a Client for the RHSM API.
func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     defaultTokenURL,
		apiURL:       defaultAPIURL,
		httpClient:   http.DefaultClient,
	}
}

// token obtains an OAuth2 access token using client credentials.
func (c *Client) token(ctx context.Context) (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {"api.console"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed (%d): %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}
	return tokenResp.AccessToken, nil
}

// CreateActivationKey creates a new activation key and returns its name.
func (c *Client) CreateActivationKey(ctx context.Context, name string) (string, error) {
	token, err := c.token(ctx)
	if err != nil {
		return "", fmt.Errorf("obtaining access token: %w", err)
	}

	payload := map[string]string{
		"name":         name,
		"serviceLevel": "Self-Support",
		"role":         "Red Hat Enterprise Linux Server",
		"usage":        "Development/Test",
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling activation key request: %w", err)
	}

	endpoint := c.apiURL + "/activation_keys"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payloadJSON)))
	if err != nil {
		return "", fmt.Errorf("building activation key request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating activation key: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading activation key response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("activation key creation failed (%d): %s", resp.StatusCode, body)
	}

	var keyResp struct {
		Body []struct {
			Name string `json:"name"`
		} `json:"body"`
	}
	if err := json.Unmarshal(body, &keyResp); err != nil {
		// Some API versions return the key directly
		var direct struct {
			Name string `json:"name"`
		}
		if err2 := json.Unmarshal(body, &direct); err2 != nil {
			return "", fmt.Errorf("parsing activation key response: %w", err)
		}
		if direct.Name != "" {
			return direct.Name, nil
		}
		return "", fmt.Errorf("parsing activation key response: %w", err)
	}
	if len(keyResp.Body) > 0 && keyResp.Body[0].Name != "" {
		return keyResp.Body[0].Name, nil
	}

	// Fall back to the name we requested
	return name, nil
}
