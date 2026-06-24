package rhel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCreateActivationKey(t *testing.T) {
	var tokenCalls atomic.Int32
	var keyCalls atomic.Int32

	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("token: expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("token: parse form: %v", err)
		}
		if r.FormValue("grant_type") != "client_credentials" {
			t.Errorf("token: grant_type = %q", r.FormValue("grant_type"))
		}
		if r.FormValue("client_id") != "test-id" {
			t.Errorf("token: client_id = %q", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "test-secret" {
			t.Errorf("token: client_secret = %q", r.FormValue("client_secret"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
	})

	mux.HandleFunc("/api/rhsm/v2/activation_keys", func(w http.ResponseWriter, r *http.Request) {
		keyCalls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("key: expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("key: Authorization = %q", got)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("key: decode body: %v", err)
		}
		if body["name"] != "task-42" {
			t.Errorf("key: name = %q", body["name"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"name": "task-42"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient("test-id", "test-secret")
	c.tokenURL = srv.URL + "/token"
	c.apiURL = srv.URL + "/api/rhsm/v2"
	c.httpClient = srv.Client()

	name, err := c.CreateActivationKey(context.Background(), "task-42")
	if err != nil {
		t.Fatalf("CreateActivationKey: %v", err)
	}
	if name != "task-42" {
		t.Errorf("name = %q, want %q", name, "task-42")
	}
	if tokenCalls.Load() != 1 {
		t.Errorf("token endpoint called %d times", tokenCalls.Load())
	}
	if keyCalls.Load() != 1 {
		t.Errorf("key endpoint called %d times", keyCalls.Load())
	}
}

func TestCreateActivationKey_TokenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()

	c := NewClient("bad-id", "bad-secret")
	c.tokenURL = srv.URL
	c.httpClient = srv.Client()

	_, err := c.CreateActivationKey(context.Background(), "task-1")
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestCreateActivationKey_KeyCreationError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
	})
	mux.HandleFunc("/api/rhsm/v2/activation_keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient("id", "secret")
	c.tokenURL = srv.URL + "/token"
	c.apiURL = srv.URL + "/api/rhsm/v2"
	c.httpClient = srv.Client()

	_, err := c.CreateActivationKey(context.Background(), "task-1")
	if err == nil {
		t.Fatal("expected error for forbidden response")
	}
}

func TestCreateActivationKey_WrappedResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
	})
	mux.HandleFunc("/api/rhsm/v2/activation_keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"body": []map[string]string{{"name": "wrapped-key"}},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient("id", "secret")
	c.tokenURL = srv.URL + "/token"
	c.apiURL = srv.URL + "/api/rhsm/v2"
	c.httpClient = srv.Client()

	name, err := c.CreateActivationKey(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "wrapped-key" {
		t.Errorf("name = %q, want %q", name, "wrapped-key")
	}
}
