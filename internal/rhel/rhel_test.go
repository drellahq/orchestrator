package rhel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCreateActivationKey(t *testing.T) {
	var mu sync.Mutex
	var tokenRequests int
	var keyRequests int
	var lastKeyBody map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenRequests++
			if r.Method != "POST" {
				t.Errorf("token request method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("grant_type") != "client_credentials" {
				t.Errorf("grant_type = %q", r.FormValue("grant_type"))
			}
			if r.FormValue("client_id") != "test-id" {
				t.Errorf("client_id = %q", r.FormValue("client_id"))
			}
			if r.FormValue("client_secret") != "test-secret" {
				t.Errorf("client_secret = %q", r.FormValue("client_secret"))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
			return
		}

		if strings.HasSuffix(r.URL.Path, "/activation_keys") && r.Method == "POST" {
			keyRequests++
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-token" {
				t.Errorf("Authorization = %q, want Bearer test-token", auth)
			}
			lastKeyBody = make(map[string]string)
			json.NewDecoder(r.Body).Decode(&lastKeyBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"name": lastKeyBody["name"]})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	c := NewClient("test-id", "test-secret")
	c.tokenURL = ts.URL + "/token"
	c.activationKeyURL = ts.URL + "/activation_keys"

	name, err := c.CreateActivationKey("orchestrator-task-42")
	if err != nil {
		t.Fatalf("CreateActivationKey() error: %v", err)
	}
	if name != "orchestrator-task-42" {
		t.Errorf("name = %q, want %q", name, "orchestrator-task-42")
	}

	mu.Lock()
	defer mu.Unlock()
	if tokenRequests != 1 {
		t.Errorf("tokenRequests = %d, want 1", tokenRequests)
	}
	if keyRequests != 1 {
		t.Errorf("keyRequests = %d, want 1", keyRequests)
	}
	if lastKeyBody["name"] != "orchestrator-task-42" {
		t.Errorf("key body name = %q", lastKeyBody["name"])
	}
}

func TestCreateActivationKey_TokenFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid credentials"))
	}))
	defer ts.Close()

	c := NewClient("bad-id", "bad-secret")
	c.tokenURL = ts.URL + "/token"

	_, err := c.CreateActivationKey("test-key")
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Errorf("error = %q, want mention of 401", err.Error())
	}
}

func TestCreateActivationKey_APIFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer ts.Close()

	c := NewClient("id", "secret")
	c.tokenURL = ts.URL + "/token"
	c.activationKeyURL = ts.URL + "/activation_keys"

	_, err := c.CreateActivationKey("test-key")
	if err == nil {
		t.Fatal("expected error for API failure")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %q, want mention of 500", err.Error())
	}
}

func TestDeleteActivationKey(t *testing.T) {
	var deletedKey string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
			return
		}
		if r.Method == "DELETE" {
			parts := strings.Split(r.URL.Path, "/")
			deletedKey = parts[len(parts)-1]
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	c := NewClient("id", "secret")
	c.tokenURL = ts.URL + "/token"
	c.activationKeyURL = ts.URL + "/activation_keys"

	err := c.DeleteActivationKey("orchestrator-task-42")
	if err != nil {
		t.Fatalf("DeleteActivationKey() error: %v", err)
	}
	if deletedKey != "orchestrator-task-42" {
		t.Errorf("deleted key = %q, want %q", deletedKey, "orchestrator-task-42")
	}
}
