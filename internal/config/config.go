// Package config reads the environment the plugin honours, so the rest
// of the code takes its settings as arguments.
package config

import (
	"encoding/json"
	"os"
)

// The picker popup size: content fits the widest readout plus padding,
// grown by the border.
const (
	contentCols = 12
	contentRows = 3
	// PopupWidth and PopupHeight are the fixed outer popup size in cells.
	PopupWidth  = "14"
	PopupHeight = "5"
)

// Config is the environment, read once.
type Config struct {
	// Debug is empty, or a file path, or any value meaning stderr.
	Debug    string
	StateDir string
	// TermProgram keys the theme cache.
	TermProgram string
	// HandoffPath is set by the action process on the picker's environment.
	HandoffPath string

	panes []PaneSource
}

// PaneSource is one candidate for the focused pane, named for the log.
type PaneSource struct {
	Name  string
	Value string
}

func Load() Config {
	var pluginContext struct {
		FocusedPaneID string `json:"focused_pane_id"`
	}
	if raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &pluginContext)
	}
	return Config{
		Debug:       os.Getenv("HINTS_DEBUG"),
		StateDir:    os.Getenv("HERDR_PLUGIN_STATE_DIR"),
		TermProgram: os.Getenv("TERM_PROGRAM"),
		HandoffPath: os.Getenv("HINTS_HANDOFF"),
		panes: []PaneSource{
			{"HERDR_ACTIVE_PANE_ID", os.Getenv("HERDR_ACTIVE_PANE_ID")},
			{"HERDR_PANE_ID", os.Getenv("HERDR_PANE_ID")},
			{"HERDR_PLUGIN_CONTEXT_JSON.focused_pane_id", pluginContext.FocusedPaneID},
		},
	}
}

// PaneSources are the focused-pane candidates, most trusted first.
func (c Config) PaneSources() []PaneSource { return c.panes }

// FocusedPane is the first candidate that named a pane.
func (c Config) FocusedPane() (pane, source string) {
	for _, s := range c.panes {
		if s.Value != "" {
			return s.Value, s.Name
		}
	}
	return "", "none"
}
