package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// maxRollupDays is the maximum range of a single dailyRollUp request.
	maxRollupDays = 90

	// dailyRollupPageSize is the maximum number of data points of a single dailyRollUp response.
	dailyRollupPageSize = maxRollupDays

	// expiryMargin is how long before the actual expiry an access token is refreshed.
	expiryMargin = time.Minute
)

// endpoints are variables so that tests can point them to a local server.
var (
	tokenEndpoint  = "https://oauth2.googleapis.com/token"
	healthEndpoint = "https://health.googleapis.com"
)

var durationPattern = regexp.MustCompile(`^([0-9]+)([dwmy])$`)

// Token is the access token JSON file read by -e.
type Token struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry"`
	Scope        string    `json:"scope,omitempty"`
}

// LoadToken reads the access token JSON file.
func LoadToken(path string) (*Token, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ret Token
	if err = json.Unmarshal(body, &ret); err != nil {
		return nil, fmt.Errorf("%s: invalid access token JSON: %w", path, err)
	}
	if ret.AccessToken == "" && ret.RefreshToken == "" {
		return nil, fmt.Errorf("%s: neither access_token nor refresh_token is set", path)
	}
	return &ret, nil
}

// SaveToken writes the access token JSON file.
func SaveToken(path string, token *Token) error {
	body, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

// expired reports whether the access token is known to be expired. When no
// expiry is recorded the token is assumed to be usable as is, so that a hand
// written access token JSON works without a refresh.
func (t *Token) expired() bool {
	if t.Expiry.IsZero() {
		return false
	}
	return time.Now().Add(expiryMargin).After(t.Expiry)
}

// Refresh obtains a new access token using the refresh token if the current
// access token has expired. It reports whether the token was updated.
func (t *Token) Refresh(verbose bool) (bool, error) {
	return t.refresh(false, verbose)
}

// refresh obtains a new access token. When force is false it does nothing if
// the access token has not expired.
func (t *Token) refresh(force bool, verbose bool) (bool, error) {
	if !force && !t.expired() {
		return false, nil
	}
	if t.RefreshToken == "" {
		return false, errors.New("cannot refresh the access token: refresh_token is not set in the access token JSON")
	}
	if t.ClientID == "" || t.ClientSecret == "" {
		return false, errors.New("cannot refresh the access token: client_id and client_secret are required in the access token JSON")
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", t.RefreshToken)
	values.Set("client_id", t.ClientID)
	values.Set("client_secret", t.ClientSecret)

	if verbose {
		fmt.Println("Update credential ...")
	}
	body, err := postForm(tokenEndpoint, values)
	if err != nil {
		return false, err
	}
	var res tokenResponse
	if err = json.Unmarshal(body, &res); err != nil {
		return false, err
	}
	if res.AccessToken == "" {
		return false, fmt.Errorf("token endpoint returned no access_token: %s", strings.TrimSpace(string(body)))
	}
	t.AccessToken = res.AccessToken
	if res.RefreshToken != "" {
		t.RefreshToken = res.RefreshToken
	}
	if res.TokenType != "" {
		t.TokenType = res.TokenType
	}
	if res.Scope != "" {
		t.Scope = res.Scope
	}
	t.Expiry = time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
	if verbose {
		fmt.Println("new access_token: " + t.AccessToken)
		fmt.Println("new expiry: " + t.Expiry.Format(time.RFC3339))
	}
	return true, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// Steps is a daily step count saved to the SQLite database.
type Steps struct {
	Day   string
	Steps string
}

// GetSteps fetches daily step counts for the past duration (e.g. 1d, 1w, 1m, 1y)
// from the Google Health API.
func GetSteps(token *Token, duration string, verbose bool) ([]Steps, error) {
	start, end, err := durationRange(duration, time.Now())
	if err != nil {
		return nil, err
	}
	var out []Steps
	for from := start; from.Before(end); {
		to := from.AddDate(0, 0, maxRollupDays)
		if to.After(end) {
			to = end
		}
		steps, err := getDailySteps(token, from, to, verbose)
		if err != nil {
			return nil, err
		}
		out = append(out, steps...)
		from = to
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// durationRange converts a duration such as "1m" into the civil time range
// [start, end) ending tomorrow, so that today is included.
func durationRange(duration string, now time.Time) (time.Time, time.Time, error) {
	m := durationPattern.FindStringSubmatch(strings.TrimSpace(duration))
	if m == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid duration %q: use a number followed by d, w, m or y (e.g. 1d, 1w, 1m, 1y)", duration)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid duration %q: must be greater than zero", duration)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var start time.Time
	switch m[2] {
	case "d":
		start = today.AddDate(0, 0, -n)
	case "w":
		start = today.AddDate(0, 0, -7*n)
	case "m":
		start = today.AddDate(0, -n, 0)
	default: // "y"
		start = today.AddDate(-n, 0, 0)
	}
	return start, today.AddDate(0, 0, 1), nil
}

type civilDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

func (d civilDate) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

type civilDateTime struct {
	Date civilDate `json:"date"`
}

func newCivilDateTime(t time.Time) civilDateTime {
	return civilDateTime{Date: civilDate{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}}
}

type civilTimeInterval struct {
	Start civilDateTime `json:"start"`
	End   civilDateTime `json:"end"`
}

type dailyRollUpRequest struct {
	Range          civilTimeInterval `json:"range"`
	WindowSizeDays int               `json:"windowSizeDays"`
	PageSize       int               `json:"pageSize"`
	PageToken      string            `json:"pageToken,omitempty"`
}

type dailyRollUpResponse struct {
	RollupDataPoints []dailyRollUpDataPoint `json:"rollupDataPoints"`
	NextPageToken    string                 `json:"nextPageToken"`
}

type dailyRollUpDataPoint struct {
	CivilStartTime civilDateTime     `json:"civilStartTime"`
	Steps          *stepsRollupValue `json:"steps"`
}

type stepsRollupValue struct {
	CountSum string `json:"countSum"`
}

func getDailySteps(token *Token, from, to time.Time, verbose bool) ([]Steps, error) {
	uri := healthEndpoint + "/v4/users/me/dataTypes/steps/dataPoints:dailyRollUp"
	var out []Steps
	pageToken := ""
	for {
		payload := dailyRollUpRequest{
			Range: civilTimeInterval{
				Start: newCivilDateTime(from),
				End:   newCivilDateTime(to),
			},
			WindowSizeDays: 1,
			PageSize:       dailyRollupPageSize,
			PageToken:      pageToken,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, uri, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("authorization", "Bearer "+token.AccessToken)
		req.Header.Set("accept", "application/json")
		req.Header.Set("content-type", "application/json")
		if verbose {
			fmt.Printf("%s %s\n%s\n", req.Method, uri, body)
		}
		resBody, err := doRequest(req)
		if err != nil {
			return nil, err
		}
		if verbose {
			fmt.Println(string(resBody))
		}
		var res dailyRollUpResponse
		if err = json.Unmarshal(resBody, &res); err != nil {
			return nil, err
		}
		for _, point := range res.RollupDataPoints {
			if point.Steps == nil {
				// no data was recorded in this window
				continue
			}
			count := point.Steps.CountSum
			if count == "" {
				// the device was worn but recorded no steps
				count = "0"
			}
			out = append(out, Steps{Day: point.CivilStartTime.Date.String(), Steps: count})
		}
		if res.NextPageToken == "" {
			return out, nil
		}
		pageToken = res.NextPageToken
	}
}

func postForm(uri string, values url.Values) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, uri, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	return doRequest(req)
}

func doRequest(req *http.Request) ([]byte, error) {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(res.Body)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &httpStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Status:     res.Status,
			Body:       strings.TrimSpace(string(body)),
		}
	}
	return body, nil
}

// httpStatusError is a non-2xx response from the API.
type httpStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s %s: %s: %s", e.Method, e.URL, e.Status, e.Body)
}

// isUnauthorized reports whether err is a 401 response, which usually means the
// access token has expired.
func isUnauthorized(err error) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized
}
