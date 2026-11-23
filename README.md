[![License](https://img.shields.io/badge/license-Apache%202-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go](https://github.com/sekiguchi-nagisa/fitbit-dump/actions/workflows/go.yml/badge.svg)](https://github.com/sekiguchi-nagisa/fitbit-dump/actions/workflows/go.yml)

# fitbit-dump

dump fitbit data from [Google Health API](https://developers.google.com/health) into a sqlite3 database

## Migration from the Fitbit Web API

The Fitbit Web API is deprecated. This tool now uses the Google Health API, which is
the successor of the Fitbit Web API. Steps are fetched with the `dailyRollUp`
endpoint and saved into the `steps` table in the same format as before
(`day` TEXT, `steps` INTEGER).

## Setup

1. Create a Google Cloud project and enable the **Google Health API**.
2. Create an OAuth 2.0 Client ID for a **Web application** and register a redirect
   URI. See the [setup guide](https://developers.google.com/health/setup) for
   details. Add the scopes you need on the **Data Access** page, and add the Google
   Accounts you sign in with to the **Test users** list on the **Audience** page
   while the OAuth consent screen stays in the "Testing" publishing status.
3. Obtain an OAuth 2.0 access token and save it as an access token JSON file.

   This tool only reads the token; generating it is out of scope. Use the
   [OAuth 2.0 Playground](https://developers.google.com/oauthplayground) (gear icon >
   *Use your own OAuth credentials*) or any OAuth 2.0 client library, then write the
   result in the format below.

### Access token JSON

The `-e`/`--env` flag reads a JSON file in this format.

```json
{
  "client_id": "xxx.apps.googleusercontent.com",
  "client_secret": "xxx",
  "access_token": "ya29.xxx",
  "token_type": "Bearer",
  "refresh_token": "1//xxx",
  "expiry": "2026-11-23T20:00:00Z",
  "scope": "https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly"
}
```

Only `access_token` is required. To enable automatic refresh, also set
`refresh_token`, `client_id` and `client_secret`; `expiry` (RFC 3339) tells the tool
when to refresh and is rewritten after each refresh. Without `expiry` the access
token is used as is, and if the API rejects it (401) while `refresh_token` is
present, it is refreshed once and the request is retried.

The scope required for reading steps is
`https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly`.

A refresh token expires after 7 days while the OAuth consent screen is in the
"Testing" publishing status; publish the app for long-running use.

## Usage

```sh
fitbit-dump -e token.json -o db.sqlite3 -d 1m
```

| Flag | Description |
|---|---|
| `-e`, `--env` | access token JSON file |
| `-o`, `--output` | output sqlite3 file |
| `-d`, `--duration` | duration to fetch (`1d`, `1w`, `1m`, `1y`). Default: `1m` |
| `--debug` | print requests and responses |
