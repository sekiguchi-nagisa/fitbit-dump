package main

import (
	"fmt"
	"github.com/alecthomas/kong"
	"os"
	"runtime/debug"
)

var CLI struct {
	Version bool   `short:"v"  help:"Print version information and exit"`
	Env     string `short:"e" help:"Set environment variables (access token)"`
	Output  string `short:"o" help:"Set output file (default ./output.sqlite3)"`
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
	kong.Parse(&CLI, kong.UsageOnError())
	if CLI.Version {
		fmt.Println(getVersion())
		os.Exit(0)
	}
}
