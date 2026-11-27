package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// setCLI replaces the global CLI flags for the duration of a test.
func setCLI(t *testing.T, env string, output string, duration string) {
	t.Helper()
	old := CLI
	t.Cleanup(func() { CLI = old })
	CLI.Env = env
	CLI.Output = output
	CLI.Duration = duration
	CLI.Debug = false
}

func TestRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req dailyRollUpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode the request body: %s", err)
			return
		}
		res := dailyRollUpResponse{
			RollupDataPoints: []dailyRollUpDataPoint{
				{
					CivilStartTime: req.Range.Start,
					Steps:          &stepsRollupValue{CountSum: "4321"},
				},
			},
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	dbPath := filepath.Join(dir, "out.sqlite3")
	// no refresh_token: the access token is used as is
	if err := SaveToken(tokenPath, &Token{
		AccessToken: "access",
		Expiry:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveToken returned an error: %s", err)
	}

	setCLI(t, tokenPath, dbPath, "1d")
	if err := run(); err != nil {
		t.Fatalf("run returned an error: %s", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open the database: %s", err)
	}
	defer func() { _ = db.Close() }()

	var day string
	var steps int
	if err := db.QueryRow(`select day, steps from steps`).Scan(&day, &steps); err != nil {
		t.Fatalf("failed to select the inserted row: %s", err)
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if day != yesterday {
		t.Errorf("day = %q, want %q", day, yesterday)
	}
	if steps != 4321 {
		t.Errorf("steps = %d, want 4321", steps)
	}

	// running twice must be idempotent
	if err = run(); err != nil {
		t.Fatalf("the second run returned an error: %s", err)
	}
	var count int
	if err = db.QueryRow(`select count(*) from steps`).Scan(&count); err != nil {
		t.Fatalf("failed to count the rows: %s", err)
	}
	if count != 1 {
		t.Errorf("the database has %d rows, want 1", count)
	}
}

func TestRunAlwaysRefreshesToken(t *testing.T) {
	refreshed := false
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshed = true
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3599,"token_type":"Bearer"}`))
	}))
	defer tokenSrv.Close()

	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("authorization"), "Bearer new-access"; got != want {
			t.Errorf("authorization = %q, want %q", got, want)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"rollupDataPoints":[]}`))
	}))
	defer healthSrv.Close()

	oldToken, oldHealth := tokenEndpoint, healthEndpoint
	tokenEndpoint, healthEndpoint = tokenSrv.URL, healthSrv.URL
	defer func() { tokenEndpoint, healthEndpoint = oldToken, oldHealth }()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := SaveToken(tokenPath, &Token{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccessToken:  "old-access",
		RefreshToken: "refresh",
		// the recorded expiry has not passed, but the token is refreshed anyway
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveToken returned an error: %s", err)
	}

	setCLI(t, tokenPath, filepath.Join(dir, "out.sqlite3"), "1d")
	if err := run(); err != nil {
		t.Fatalf("run returned an error: %s", err)
	}
	if !refreshed {
		t.Error("the token was not refreshed")
	}
	token, err := LoadToken(tokenPath)
	if err != nil {
		t.Fatalf("LoadToken returned an error: %s", err)
	}
	if token.AccessToken != "new-access" || token.RefreshToken != "refresh" {
		t.Errorf("the refreshed token was not saved: %+v", token)
	}
}

func TestRunRetriesAfter401(t *testing.T) {
	// the first refresh returns a token the API rejects; the dump command must
	// refresh once more and retry.
	var healthCalls int
	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthCalls++
		if got, want := r.Header.Get("authorization"), "Bearer fresh-access"; got != want {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"status":"UNAUTHENTICATED"}}`))
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"rollupDataPoints":[]}`))
	}))
	defer healthSrv.Close()

	tokenCalls := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse the form: %s", err)
		}
		for key, want := range map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "refresh",
			"client_id":     "client-id",
			"client_secret": "client-secret",
		} {
			if got := r.PostForm.Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		tokenCalls++
		accessToken := "stale-access"
		if tokenCalls > 1 {
			accessToken = "fresh-access"
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","expires_in":3599,"token_type":"Bearer"}`))
	}))
	defer tokenSrv.Close()

	oldToken, oldHealth := tokenEndpoint, healthEndpoint
	tokenEndpoint, healthEndpoint = tokenSrv.URL, healthSrv.URL
	defer func() { tokenEndpoint, healthEndpoint = oldToken, oldHealth }()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := SaveToken(tokenPath, &Token{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccessToken:  "stale-access",
		RefreshToken: "refresh",
	}); err != nil {
		t.Fatalf("SaveToken returned an error: %s", err)
	}

	setCLI(t, tokenPath, filepath.Join(dir, "out.sqlite3"), "1d")
	if err := run(); err != nil {
		t.Fatalf("run returned an error: %s", err)
	}
	if healthCalls != 2 {
		t.Errorf("the health API was called %d times, want 2 (initial + retry)", healthCalls)
	}
	saved, err := LoadToken(tokenPath)
	if err != nil {
		t.Fatalf("LoadToken returned an error: %s", err)
	}
	if saved.AccessToken != "fresh-access" {
		t.Errorf("access_token = %q, want %q", saved.AccessToken, "fresh-access")
	}
}
