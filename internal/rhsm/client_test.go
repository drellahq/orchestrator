package rhsm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCreateActivationKey(t *testing.T) {
	var gotBody map[string]string
	var gotAuth string

	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-123",
			"expires_in":   300,
		})
	}))
	defer sso.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)

		if r.Method != http.MethodPost || r.URL.Path != "/activation_keys" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"body": map[string]any{"name": gotBody["name"]}})
	}))
	defer api.Close()

	c := newTestClient(sso.URL, api.URL, "cid", "csecret", "org-1")

	if err := c.CreateActivationKey(context.Background(), "drella-test-task"); err != nil {
		t.Fatalf("CreateActivationKey: %v", err)
	}

	if gotAuth != "Bearer tok-123" {
		t.Errorf("auth = %q, want Bearer tok-123", gotAuth)
	}
	if gotBody["name"] != "drella-test-task" {
		t.Errorf("body.name = %q, want drella-test-task", gotBody["name"])
	}
}

func TestDeleteActivationKey(t *testing.T) {
	var gotPath string

	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-456",
			"expires_in":   300,
		})
	}))
	defer sso.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()

	c := newTestClient(sso.URL, api.URL, "cid", "csecret", "org-1")

	if err := c.DeleteActivationKey(context.Background(), "drella-test-task"); err != nil {
		t.Fatalf("DeleteActivationKey: %v", err)
	}

	if gotPath != "/activation_keys/drella-test-task" {
		t.Errorf("path = %q, want /activation_keys/drella-test-task", gotPath)
	}
}

func TestCreateActivationKeyAPIError(t *testing.T) {
	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"expires_in":   300,
		})
	}))
	defer sso.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden"))
	}))
	defer api.Close()

	c := newTestClient(sso.URL, api.URL, "cid", "csecret", "org-1")

	err := c.CreateActivationKey(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q should mention 403", err)
	}
}

func TestTokenCaching(t *testing.T) {
	var tokenCalls atomic.Int32

	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-cached",
			"expires_in":   300,
		})
	}))
	defer sso.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"body":{}}`))
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer api.Close()

	c := newTestClient(sso.URL, api.URL, "cid", "csecret", "org-1")

	ctx := context.Background()
	c.CreateActivationKey(ctx, "key1")
	c.CreateActivationKey(ctx, "key2")
	c.DeleteActivationKey(ctx, "key1")

	if got := tokenCalls.Load(); got != 1 {
		t.Errorf("token endpoint called %d times, want 1 (should be cached)", got)
	}
}

func TestTokenAuthFailure(t *testing.T) {
	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid credentials"))
	}))
	defer sso.Close()

	c := newTestClient(sso.URL, "http://unused", "bad-id", "bad-secret", "org-1")

	err := c.CreateActivationKey(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
	if !strings.Contains(err.Error(), "access token") {
		t.Errorf("error %q should mention access token", err)
	}
}

// newTestClient creates a Client that uses test server URLs instead of
// production Red Hat SSO and RHSM endpoints.
func newTestClient(ssoURL, apiURL, clientID, clientSecret, orgID string) *Client {
	c := NewClient(clientID, clientSecret, orgID)
	// Override the package-level URLs by swapping the internal fields.
	// We use a wrapper transport to rewrite request URLs.
	c.httpClient = &http.Client{
		Transport: &urlRewriter{
			tokenURL: ssoURL,
			apiURL:   apiURL,
		},
	}
	return c
}

type urlRewriter struct {
	tokenURL string
	apiURL   string
}

func (u *urlRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	original := req.URL.String()
	if strings.HasPrefix(original, tokenURL) {
		req = req.Clone(req.Context())
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(u.tokenURL, "http://")
		req.URL.Path = "/"
	} else if strings.HasPrefix(original, rhsmAPI) {
		req = req.Clone(req.Context())
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(u.apiURL, "http://")
		req.URL.Path = strings.TrimPrefix(original, rhsmAPI)
	}
	return http.DefaultTransport.RoundTrip(req)
}
