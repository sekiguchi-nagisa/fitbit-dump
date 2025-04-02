package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type FitbitCredential struct {
	AccessToken  string
	BasicToken   string
	ClientID     string
	RefreshToken string
}

func FromEnvs(envs map[string]string) FitbitCredential {
	return FitbitCredential{
		AccessToken:  envs["FITBIT_ACCESS_TOKEN"],
		BasicToken:   envs["FITBIT_BASIC_TOKEN"],
		ClientID:     envs["FITBIT_CLIENT_ID"],
		RefreshToken: envs["FITBIT_REFRESH_TOKEN"],
	}
}

func (f *FitbitCredential) ToEnvs() map[string]string {
	ret := map[string]string{}
	ret["FITBIT_ACCESS_TOKEN"] = f.AccessToken
	ret["FITBIT_BASIC_TOKEN"] = f.BasicToken
	ret["FITBIT_CLIENT_ID"] = f.ClientID
	ret["FITBIT_REFRESH_TOKEN"] = f.RefreshToken
	return ret
}

type Steps struct {
	Day   string `json:"dateTime"`
	Steps string `json:"value"`
}

type ActivitiesSteps struct {
	Values []Steps `json:"activities-steps"`
}

func GetSteps(credential *FitbitCredential, duration string) ([]Steps, error) {
	url := fmt.Sprintf("https://api.fitbit.com/1/user/-/activities/steps/date/today/%s.json", duration)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("authorization", "Bearer "+credential.AccessToken)
	req.Header.Add("accept", "application/json")
	client := &http.Client{}
	res, err := client.Do(req)
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
	fmt.Println(string(body))
	var ret ActivitiesSteps
	err = json.Unmarshal(body, &ret)
	return ret.Values, err
}

func RefreshCredentials(credential *FitbitCredential) error {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", credential.RefreshToken)

	req, err := http.NewRequest(http.MethodPost, "https://api.fitbit.com/oauth2/token", strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Basic "+credential.BasicToken)
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(res.Body)
	var ret map[string]interface{}
	err = json.NewDecoder(res.Body).Decode(&ret)
	fmt.Println("update refresh token")
	credential.AccessToken = ret["access_token"].(string)
	credential.RefreshToken = ret["refresh_token"].(string)
	return err
}
