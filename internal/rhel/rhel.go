package rhel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	defaultTokenURL         = "https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token"
	defaultActivationKeyURL = "https://console.redhat.com/api/rhsm/v2/activation_keys"
)

// Client interacts with the Red Hat Console API to manage activation keys.
type Client struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	tokenURL         string
	activationKeyURL string
}

// NewClient creates a Client using the given service account credentials.
func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:         clientID,
		clientSecret:     clientSecret,
		httpClient:       &http.Client{},
		tokenURL:         defaultTokenURL,
		activationKeyURL: defaultActivationKeyURL,
	}
}

func (c *Client) getToken() (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	resp, err := c.httpClient.PostForm(c.tokenURL, data)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed (status %d): %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}

	return tokenResp.AccessToken, nil
}

// CreateActivationKey creates a new activation key and returns its name.
func (c *Client) CreateActivationKey(name string) (string, error) {
	token, err := c.getToken()
	if err != nil {
		return "", err
	}

	body := map[string]string{
		"name": name,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshaling request body: %w", err)
	}

	req, err := http.NewRequest("POST", c.activationKeyURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating activation key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("activation key creation failed (status %d): %s", resp.StatusCode, respBody)
	}

	var keyResp struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&keyResp); err != nil {
		return "", fmt.Errorf("parsing activation key response: %w", err)
	}
	if keyResp.Name == "" {
		keyResp.Name = name
	}

	return keyResp.Name, nil
}

// DeleteActivationKey removes an activation key by name.
func (c *Client) DeleteActivationKey(name string) error {
	token, err := c.getToken()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", c.activationKeyURL+"/"+url.PathEscape(name), nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting activation key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("activation key deletion failed (status %d): %s", resp.StatusCode, body)
	}

	return nil
}
