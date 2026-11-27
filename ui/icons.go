package ui

// icons.go holds the glyphs drawn across the interface.
//
// Two sets are available and the "Icons" setting picks one:
//
//   - plainIcons uses single-column characters that every terminal renders the
//     same way. It is the safe choice, and the toolbar buttons then show their
//     text labels.
//   - nerdIcons uses Font Awesome code points from the private use area, the
//     way desktop IDEs look. It needs a terminal font patched with Nerd Fonts
//     (see the README); without one those glyphs render as boxes. None of them
//     is an emoji, so they stay exactly one column wide.
//
// Both sets are measured as one column wide by uniseg, so the layout math stays
// identical whichever one is active.

import (
	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/complete"
)

// iconSet is one collection of interface glyphs.
type iconSet struct {
	// Toolbar buttons. The plain set leaves these empty, in which case the
	// button shows its text label instead.
	newTab, save, run, format, settings, help string

	// Editor tabs.
	tab, tabNew, tabClose string

	// Object browser and completion popup.
	connActive, connIdle, schema, table, view, column string
	keyword, function, snippet                        string
	trigger, index, sequence                          string

	// The file picker marks directories with it. The plain set leaves it
	// empty, and the picker then falls back to a trailing "/" — it is a
	// different mark from schema, which only happens to be a folder glyph
	// in the nerd set because the object browser draws schemas as folders.
	folder string

	// Expansion state and pane chrome.
	expandOpen, expandClosed, expandUp, paneClose string
}

// toolbarIconKey identifies a toolbar icon inside the set.
type toolbarIconKey int

// The toolbar icons, in toolButtons order.
const (
	iconNewTab toolbarIconKey = iota
	iconSave
	iconRun
	iconFormat
	iconSettings
	iconHelp
)

// icon returns the icon of a toolbar button ("" when the set has none).
func (s iconSet) icon(k toolbarIconKey) string {
	switch k {
	case iconNewTab:
		return s.newTab
	case iconSave:
		return s.save
	case iconRun:
		return s.run
	case iconFormat:
		return s.format
	case iconSettings:
		return s.settings
	case iconHelp:
		return s.help
	}
	return ""
}

// kind returns the icon of a completion candidate kind, so the popup and the
// object browser agree on what a table, a column and the rest look like.
func (s iconSet) kind(k complete.Kind) string {
	switch k {
	case complete.KindTable:
		return s.table
	case complete.KindView:
		return s.view
	case complete.KindColumn:
		return s.column
	case complete.KindSchema:
		return s.schema
	case complete.KindFunction:
		return s.function
	case complete.KindKeyword:
		return s.keyword
	case complete.KindSnippet:
		return s.snippet
	case complete.KindProcedure:
		// The object browser draws procedures with the function glyph, so the
		// popup agrees with the tree.
		return s.function
	case complete.KindTrigger:
		return s.trigger
	case complete.KindIndex:
		return s.index
	case complete.KindSequence:
		return s.sequence
	}
	return s.column
}

// resource returns the icon for a catalog resource kind. Group headers and
// leaf rows use the same mapping, so a "Views" folder and its members agree.
func (s iconSet) resource(k catalog.ResourceKind) string {
	switch k {
	case catalog.KindTable:
		return s.table
	case catalog.KindView, catalog.KindMaterializedView:
		return s.view
	case catalog.KindFunction, catalog.KindProcedure:
		return s.function
	case catalog.KindTrigger:
		return s.trigger
	case catalog.KindIndex:
		return s.index
	case catalog.KindSequence:
		return s.sequence
	}
	return s.column
}

// plainIcons needs no particular font.
func plainIcons() iconSet {
	return iconSet{
		tabNew:       "+",
		tabClose:     "×",
		connActive:   "◉",
		connIdle:     "○",
		schema:       "◈",
		table:        "⊞",
		view:         "◫",
		column:       "◧",
		keyword:      "⌘",
		function:     "ƒ",
		snippet:      "»",
		trigger:      "↯",
		index:        "⌗",
		sequence:     "≋",
		expandOpen:   "▾",
		expandClosed: "▸",
		expandUp:     "▴",
		paneClose:    "✕",
	}
}

// nerdIcons uses Font Awesome glyphs from the private use area. The code
// points are written as escapes so the source stays readable in any editor.
func nerdIcons() iconSet {
	return iconSet{
		newTab:   "\uf0fe", // plus-square
		save:     "\uf0c7", // floppy-o
		run:      "\uf04b", // play
		format:   "\uf036", // align-left
		settings: "\uf013", // cog
		help:     "\uf059", // question-circle

		tab:      "\uf15b", // file
		tabNew:   "\uf067", // plus
		tabClose: "\uf00d", // times

		connActive: "\uf1c0", // database
		connIdle:   "\uf1e6", // plug
		schema:     "\uf07b", // folder
		folder:     "\uf07b", // folder, drawn by the file picker
		table:      "\uf0ce", // table
		view:       "\uf06e", // eye
		column:     "\uf0db", // columns
		keyword:    "\uf02d", // book
		function:   "\uf121", // code
		snippet:    "\uf0c4", // scissors
		trigger:    "\uf0e7", // bolt
		index:      "\uf0ca", // list-ul
		sequence:   "\uf0cb", // list-ol

		expandOpen:   "\uf0d7", // caret-down
		expandClosed: "\uf0da", // caret-right
		expandUp:     "\uf0d8", // caret-up
		paneClose:    "\uf00d", // times
	}
}

// icons returns the active icon set.
func (m Model) icons() iconSet {
	if m.settings.Icons {
		return nerdIcons()
	}
	return plainIcons()
}
