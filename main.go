package main

import (
	"database/sql"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"log/slog"
	"os"
	"runtime/debug"
)

var CLI struct {
	Version  kong.VersionFlag `short:"v" help:"Show version information"`
	Env      string           `short:"e" required:"" help:"Set env file"`
	Output   string           `short:"o" required:"" help:"Set output file"`
	Duration string           `short:"d" optional:"" default:"1m" help:"Set duration (1m, 1y)"`
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
	envs, err := godotenv.Read(CLI.Env)
	if err != nil {
		slog.Error(fmt.Sprintf("Error reading file: %s\n", err.Error()))
		os.Exit(1)
	}

	// get access_token
	credential := FromEnvs(envs)
	err = RefreshCredentials(&credential, CLI.Debug)
	if err != nil {
		slog.Error(fmt.Sprintf("Error RefreshCredentials: %s\n", err.Error()))
		os.Exit(1)
	}

	// save new credential
	envs = credential.ToEnvs()
	err = godotenv.Write(envs, CLI.Env)
	if err != nil {
		slog.Error(fmt.Sprintf("Error writing file: %s\n", err.Error()))
		os.Exit(1)
	}

	// get steps
	out, err := GetSteps(&credential, CLI.Duration, CLI.Debug)
	if err != nil {
		slog.Error(fmt.Sprintf("Error GetSteps: %s\n", err.Error()))
		os.Exit(1)
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
		slog.Error(fmt.Sprintf("Error Open: %s\n", err.Error()))
		os.Exit(1)
	}
	defer func(conn *sql.DB) {
		_ = conn.Close()
	}(db)
	err = InsertSteps(db, out)
	if err != nil {
		slog.Error(fmt.Sprintf("Error InsertSteps: %s\n", err.Error()))
		os.Exit(1)
	}
}
