// aztui is a k9s-style terminal UI for Azure.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/app"
	"github.com/GoosieZA/aztui/internal/auth"
	"github.com/GoosieZA/aztui/internal/config"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
	"github.com/GoosieZA/aztui/internal/version"

	// Resource modules self-register on import.
	_ "github.com/GoosieZA/aztui/internal/modules/appconfig"
	_ "github.com/GoosieZA/aztui/internal/modules/keyvault"
	_ "github.com/GoosieZA/aztui/internal/modules/servicebus"
	_ "github.com/GoosieZA/aztui/internal/modules/sql"
	_ "github.com/GoosieZA/aztui/internal/modules/storage"
	_ "github.com/GoosieZA/aztui/internal/modules/vm"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	readOnly := flag.Bool("read-only", false, "disable every mutating action (toggle at runtime with :ro)")
	flag.Parse()
	ui.SetReadOnly(*readOnly)
	if *showVersion {
		fmt.Printf("aztui %s (%s)\n", version.Version, version.Commit)
		return
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "aztui:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	cred, authDesc, err := auth.NewCredential()
	if err != nil {
		return err
	}

	mctx := modules.Context{Cred: cred, Config: cfg}
	program := tea.NewProgram(app.New(mctx, authDesc), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = program.Run()
	return err
}
