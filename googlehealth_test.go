package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// testScope is the OAuth 2.0 scope required to read steps.
const testScope = "https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly"

func TestDurationRange(t *testing.T) {
	now := time.Date(2026, 11, 23, 18, 54, 0, 0, time.UTC)
	tests := []struct {
		duration string
		wantFrom string
		wantTo   string
	}{
		{"1d", "2026-11-22", "2026-11-24"},
		{"1w", "2026-11-16", "2026-11-24"},
		{"1m", "2026-10-23", "2026-11-24"},
		{"1y", "2025-11-23", "2026-11-24"},
		{" 7d ", "2026-11-16", "2026-11-24"},
	}
	for _, tt := range tests {
		from, to, err := durationRange(tt.duration, now)
		if err != nil {
			t.Fatalf("durationRange(%q) returned an error: %s", tt.duration, err)
		}
		if got := from.Format("2006-01-02"); got != tt.wantFrom {
			t.Errorf("durationRange(%q) from = %s, want %s", tt.duration, got, tt.wantFrom)
		}
		if got := to.Format("2006-01-02"); got != tt.wantTo {
			t.Errorf("durationRange(%q) to = %s, want %s", tt.duration, got, tt.wantTo)
		}
	}
}

func TestDurationRangeInvalid(t *testing.T) {
	for _, duration := range []string{"", "1", "m", "0d", "-1d", "1s", "1M", "1y2m"} {
		if _, _, err := durationRange(duration, time.Now()); err == nil {
			t.Errorf("durationRange(%q) did not return an error", duration)
		}
	}
}

func TestGetSteps(t *testing.T) {
	var gotRanges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if got, want := r.URL.Path, "/v4/users/me/dataTypes/steps/dataPoints:dailyRollUp"; got != want {
			t.Errorf("unexpected path: %s, want %s", got, want)
		}
		if got, want := r.Header.Get("authorization"), "Bearer access-token"; got != want {
			t.Errorf("unexpected authorization header: %q, want %q", got, want)
		}
		var req dailyRollUpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode the request body: %s", err)
		}
		if req.WindowSizeDays != 1 {
			t.Errorf("windowSizeDays = %d, want 1", req.WindowSizeDays)
		}
		gotRanges = append(gotRanges, req.Range.Start.Date.String()+".."+req.Range.End.Date.String())
		res := dailyRollUpResponse{
			RollupDataPoints: []dailyRollUpDataPoint{
				{CivilStartTime: req.Range.Start, Steps: &stepsRollupValue{CountSum: "123"}},
				// a window without any data point omits "steps"
				{CivilStartTime: newCivilDateTime(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))},
			},
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	steps, err := GetSteps(&Token{AccessToken: "access-token"}, "1w", true)
	if err != nil {
		t.Fatalf("GetSteps returned an error: %s", err)
	}
	if len(steps) != 1 {
		t.Fatalf("GetSteps returned %d steps, want 1: %+v", len(steps), steps)
	}
	if steps[0].Steps != "123" {
		t.Errorf("steps = %q, want %q", steps[0].Steps, "123")
	}
	if len(gotRanges) != 1 {
		t.Fatalf("the server received %d requests, want 1: %v", len(gotRanges), gotRanges)
	}
}

func TestGetStepsChunksLongRange(t *testing.T) {
	var ranges []civilTimeInterval
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req dailyRollUpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode the request body: %s", err)
		}
		ranges = append(ranges, req.Range)
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(dailyRollUpResponse{})
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	if _, err := GetSteps(&Token{AccessToken: "access-token"}, "1y", false); err != nil {
		t.Fatalf("GetSteps returned an error: %s", err)
	}
	if len(ranges) < 5 {
		t.Fatalf("the server received %d requests, want at least 5: %v", len(ranges), ranges)
	}
	// chunks must be contiguous and no longer than the dailyRollUp limit
	for i, r := range ranges {
		from, err := time.Parse("2006-01-02", r.Start.Date.String())
		if err != nil {
			t.Fatalf("invalid start date %q: %s", r.Start.Date, err)
		}
		to, err := time.Parse("2006-01-02", r.End.Date.String())
		if err != nil {
			t.Fatalf("invalid end date %q: %s", r.End.Date, err)
		}
		if days := int(to.Sub(from).Hours() / 24); days > maxRollupDays {
			t.Errorf("chunk %d spans %d days, want at most %d", i, days, maxRollupDays)
		}
		if i > 0 && ranges[i-1].End.Date != r.Start.Date {
			t.Errorf("chunk %d starts at %s, want %s", i, r.Start.Date, ranges[i-1].End.Date)
		}
	}
}

func TestGetStepsPaginates(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req dailyRollUpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode the request body: %s", err)
		}
		requests++
		res := dailyRollUpResponse{
			RollupDataPoints: []dailyRollUpDataPoint{
				{CivilStartTime: newCivilDateTime(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)), Steps: &stepsRollupValue{CountSum: "10"}},
			},
		}
		if req.PageToken == "" {
			res.NextPageToken = "next"
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	steps, err := GetSteps(&Token{AccessToken: "access-token"}, "1d", false)
	if err != nil {
		t.Fatalf("GetSteps returned an error: %s", err)
	}
	if requests != 2 {
		t.Errorf("the server received %d requests, want 2", requests)
	}
	if len(steps) != 2 {
		t.Errorf("GetSteps returned %d steps, want 2: %+v", len(steps), steps)
	}
}

func TestGetStepsReportsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid authentication credentials"}`))
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	if _, err := GetSteps(&Token{AccessToken: "expired"}, "1d", false); err == nil {
		t.Fatal("GetSteps did not return an error")
	}
}

func TestGetStepsNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprint(w, `{"rollupDataPoints":[]}`)
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	steps, err := GetSteps(&Token{AccessToken: "access-token"}, "1d", false)
	if err != nil {
		t.Fatalf("GetSteps returned an error: %s", err)
	}
	if len(steps) != 0 {
		t.Errorf("GetSteps returned %d steps, want 0: %+v", len(steps), steps)
	}
}

func TestSaveAndLoadToken(t *testing.T) {
	path := t.TempDir() + "/token.json"
	want := &Token{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccessToken:  "access",
		TokenType:    "Bearer",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour).UTC().Round(time.Second),
		Scope:        testScope,
	}
	if err := SaveToken(path, want); err != nil {
		t.Fatalf("SaveToken returned an error: %s", err)
	}
	got, err := LoadToken(path)
	if err != nil {
		t.Fatalf("LoadToken returned an error: %s", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken ||
		got.ClientID != want.ClientID || got.ClientSecret != want.ClientSecret ||
		got.TokenType != want.TokenType || got.Scope != want.Scope {
		t.Errorf("LoadToken = %+v, want %+v", got, want)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Errorf("Expiry = %s, want %s", got.Expiry, want.Expiry)
	}
}

func TestLoadTokenInvalid(t *testing.T) {
	path := t.TempDir() + "/token.json"
	if err := SaveToken(path, &Token{ClientID: "client-id"}); err != nil {
		t.Fatalf("SaveToken returned an error: %s", err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Error("LoadToken did not return an error for a token without access_token and refresh_token")
	}

	envPath := t.TempDir() + "/token.env"
	// an env file written by the previous version must be rejected
	if err := os.WriteFile(envPath, []byte("FITBIT_ACCESS_TOKEN=a\n"), 0o600); err != nil {
		t.Fatalf("failed to write a file: %s", err)
	}
	if _, err := LoadToken(envPath); err == nil {
		t.Error("LoadToken did not return an error for a non JSON file")
	}
}

func TestTokenRefresh(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("content-type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected content-type: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse the form: %s", err)
		}
		got = r.PostForm
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3599,"token_type":"Bearer","scope":"` + testScope + `"}`))
	}))
	defer srv.Close()

	old := tokenEndpoint
	tokenEndpoint = srv.URL
	defer func() { tokenEndpoint = old }()

	token := &Token{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccessToken:  "old-access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}
	updated, err := token.Refresh(true)
	if err != nil {
		t.Fatalf("Refresh returned an error: %s", err)
	}
	if !updated {
		t.Error("Refresh did not update an expired token")
	}
	if token.AccessToken != "new-access" {
		t.Errorf("access_token = %q, want %q", token.AccessToken, "new-access")
	}
	if token.RefreshToken != "refresh" {
		t.Errorf("refresh_token = %q, want %q", token.RefreshToken, "refresh")
	}
	if !token.Expiry.After(time.Now()) {
		t.Errorf("expiry = %s, want a future time", token.Expiry)
	}
	for key, want := range map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "refresh",
		"client_id":     "client-id",
		"client_secret": "client-secret",
	} {
		if v := got.Get(key); v != want {
			t.Errorf("%s = %q, want %q", key, v, want)
		}
	}
}

func TestTokenRefreshAlwaysRefreshes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3599,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	old := tokenEndpoint
	tokenEndpoint = srv.URL
	defer func() { tokenEndpoint = old }()

	// a token that looks perfectly valid is still refreshed, because the expiry
	// recorded in the access token JSON is not reliable
	token := &Token{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour),
	}
	updated, err := token.Refresh(false)
	if err != nil {
		t.Fatalf("Refresh returned an error: %s", err)
	}
	if !updated {
		t.Error("Refresh did not update a token that is not expired")
	}
	if token.AccessToken != "new-access" {
		t.Errorf("access_token = %q, want %q", token.AccessToken, "new-access")
	}
}

func TestTokenRefreshWithoutRefreshToken(t *testing.T) {
	old := tokenEndpoint
	tokenEndpoint = "http://127.0.0.1:1" // must not be called
	defer func() { tokenEndpoint = old }()

	// an access token JSON without a refresh token has to be used as is
	token := &Token{AccessToken: "access", Expiry: time.Now().Add(-time.Hour)}
	updated, err := token.Refresh(false)
	if err != nil {
		t.Fatalf("Refresh returned an error: %s", err)
	}
	if updated {
		t.Error("Refresh updated a token without a refresh token")
	}
	if token.AccessToken != "access" {
		t.Errorf("access_token = %q, want %q", token.AccessToken, "access")
	}
}

func TestGetStepsZeroStepsIsStored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		// a worn device with no steps returns "countSum": "0"
		_, _ = fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":11,"day":21}},"steps":{"countSum":"0"}}]}`)
	}))
	defer srv.Close()

	old := healthEndpoint
	healthEndpoint = srv.URL
	defer func() { healthEndpoint = old }()

	steps, err := GetSteps(&Token{AccessToken: "access-token"}, "1d", false)
	if err != nil {
		t.Fatalf("GetSteps returned an error: %s", err)
	}
	if len(steps) != 1 {
		t.Fatalf("GetSteps returned %d steps, want 1: %+v", len(steps), steps)
	}
	if steps[0].Day != "2026-11-21" || steps[0].Steps != "0" {
		t.Errorf("unexpected steps: %+v", steps[0])
	}
}

func TestRefreshNeedsClientCredentials(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new"}`))
	}))
	defer srv.Close()

	old := tokenEndpoint
	tokenEndpoint = srv.URL
	defer func() { tokenEndpoint = old }()

	// an expired token with a refresh_token but no client credentials used to
	// send a request that failed with the cryptic
	// "Could not determine client ID from request."
	token := &Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}
	_, err := token.Refresh(false)
	if err == nil {
		t.Fatal("Refresh did not return an error without client credentials")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error = %q, want it to mention client_id", err)
	}
	if called {
		t.Error("Refresh sent a request even though client credentials are missing")
	}
}

func TestIsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"expired"}`))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("failed to create a request: %s", err)
	}
	_, err = doRequest(req)
	if err == nil {
		t.Fatal("doRequest did not return an error for a 401 response")
	}
	if !isUnauthorized(err) {
		t.Errorf("isUnauthorized(%v) = false, want true", err)
	}
	if isUnauthorized(errors.New("boom")) {
		t.Error("isUnauthorized reported true for an unrelated error")
	}
}
