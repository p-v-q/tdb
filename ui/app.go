// Package ui implements TDB's terminal interface with Bubble Tea: the menu
// bar, toolbar, tabs, object browser, SQL editor, result grid, dialogs and
// status bar.
//
// The package is split by responsibility:
//
//	model.go, update.go      top-level state, message loop, status messages
//	query.go, connection.go  running statements and switching databases
//	tabs.go                  per-tab sessions and tab lifecycle
//	editor*.go               editing, key handling, files and SQL commands
//	results*.go              result state, rendering, input and copying
//	objects.go, context_menu.go  object-browser tree and its menu
//	menu*.go                 menu definitions, dispatch, shortcuts, rendering
//	layout.go, panes.go      pure geometry shared by rendering and hit tests
//	render.go, theme.go      composition and visual styles
//	*_dialog.go              modal forms and pickers
//
// Architectural rules:
//   - Input, state and rendering stay separate where practical. Layout
//     geometry is a pure function, so drawing and hit testing cannot drift.
//   - menuDefs is the single source of truth for shortcuts and actions; the
//     keyboard, menus and toolbar all dispatch through runMenuAction.
//   - A tab owns its buffer and result grid. Background commands carry the
//     session that started them, so switching tabs cannot misfile results.
//   - The UI depends on driver interfaces, never on a concrete driver. The
//     binary is the composition root that links implementations.
//   - Clipboard access goes through OSC 52 in clipboard.go, which keeps both
//     the editor and the result grid testable without a terminal.
package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/settings"
)

// Run starts the terminal interface in full-screen mode, with mouse support
// unless the settings disable it. All-motion events are enabled so menus,
// toolbar buttons and tree rows can react to hover, not only to drags.
func Run(cfg Config) error {
	if cfg.Settings.Version == 0 {
		cfg.Settings = settings.Default()
	}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if cfg.Settings.Mouse {
		opts = append(opts, tea.WithMouseAllMotion())
	}
	p := tea.NewProgram(New(cfg), opts...)
	_, err := p.Run()
	return err
}
