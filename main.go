package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/alecthomas/kong"
	_ "github.com/mattn/go-sqlite3"
)

var CLI struct {
	Version  kong.VersionFlag `short:"v" help:"Show version information"`
	Env      string           `short:"e" required:"" help:"Set access token JSON file"`
	Output   string           `short:"o" required:"" help:"Set output file"`
	Duration string           `short:"d" optional:"" default:"1m" help:"Set duration (1d, 1w, 1m, 1y)"`
	Debug    bool             `optional:"" default:"false" help:"Set debug mode"`
}

var version = "" // for version embedding (specified like "-X main.version=v0.1.0")

func getVersion() string {
	info, ok := debug.ReadBuildInfo()
	if ok {
		rev := "unknown"
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				rev = setting.Value
				break
			}
		}
		var v = info.Main.Version
		if version != "" { // set by "-X main.version=v0.1.0"
			v = version
		}
		return fmt.Sprintf("%s (%s)", v, rev)
	} else {
		return "(unknown)"
	}
}

func main() {
	kong.Parse(&CLI, kong.UsageOnError(), kong.Vars{"version": getVersion()})
	if CLI.Version {
		fmt.Println(getVersion())
		os.Exit(0)
	}
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	// get access_token
	credential, err := LoadToken(CLI.Env)
	if err != nil {
		return fmt.Errorf("error LoadToken: %w", err)
	}
	// the access token is always refreshed, because the expiry recorded in the
	// access token JSON is not reliable
	updated, err := credential.Refresh(CLI.Debug)
	if err != nil {
		return fmt.Errorf("error Refresh: %w", err)
	}
	if updated {
		// save new credential
		if err = SaveToken(CLI.Env, credential); err != nil {
			return fmt.Errorf("error SaveToken: %w", err)
		}
	}

	// get steps
	out, err := GetSteps(credential, CLI.Duration, CLI.Debug)
	if err != nil && isUnauthorized(err) && credential.RefreshToken != "" {
		// the refreshed access token was rejected; refresh it once more and retry.
		if CLI.Debug {
			fmt.Println("The access token was rejected, refreshing it ...")
		}
		if _, refreshErr := credential.Refresh(CLI.Debug); refreshErr != nil {
			return fmt.Errorf("error Refresh: %w", refreshErr)
		}
		if err = SaveToken(CLI.Env, credential); err != nil {
			return fmt.Errorf("error SaveToken: %w", err)
		}
		out, err = GetSteps(credential, CLI.Duration, CLI.Debug)
	}
	if err != nil {
		return fmt.Errorf("error GetSteps: %w", err)
	}
	if CLI.Debug {
		fmt.Println("Successfully fetched steps:")
		for _, step := range out {
			fmt.Printf("%s %s\n", step.Day, step.Steps)
		}
	}

	// save to DB
	db, err := sql.Open("sqlite3", CLI.Output)
	if err != nil {
		return fmt.Errorf("error Open: %w", err)
	}
	defer func(conn *sql.DB) {
		_ = conn.Close()
	}(db)
	if err = InsertSteps(db, out); err != nil {
		return fmt.Errorf("error InsertSteps: %w", err)
	}
	return nil
}
