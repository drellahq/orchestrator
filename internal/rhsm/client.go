package rhsm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	tokenURL = "https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token"
	rhsmAPI  = "https://api.access.redhat.com/api/rhsm/v2"
)

type Client struct {
	clientID     string
	clientSecret string
	orgID        string
	httpClient   *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func NewClient(clientID, clientSecret, orgID string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		orgID:        orgID,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// OrgID returns the configured organization ID.
func (c *Client) OrgID() string {
	return c.orgID
}

func (c *Client) CreateActivationKey(ctx context.Context, name string) error {
	token, err := c.token(ctx)
	if err != nil {
		return fmt.Errorf("obtaining access token: %w", err)
	}

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rhsmAPI+"/activation_keys", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("creating activation key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
}

func (c *Client) DeleteActivationKey(ctx context.Context, name string) error {
	token, err := c.token(ctx)
	if err != nil {
		return fmt.Errorf("obtaining access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, rhsmAPI+"/activation_keys/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting activation key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", readError(resp)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	c.accessToken = tokenResp.AccessToken
	// Refresh 30 seconds before expiry to avoid races.
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second - 30*time.Second)

	return c.accessToken, nil
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("RHSM API %s %s: %d %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, string(body))
}
