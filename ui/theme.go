package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
)

// palette is the raw color set a theme is built from.
//
// Splitting "palette → styles" is what makes adding a theme cheap: a new theme
// only needs one color table instead of 40-odd lipgloss.Style constructors.
type palette struct {
	name string

	accent     lipgloss.Color // primary accent (selection, focus, highlighted borders)
	accentFg   lipgloss.Color // text drawn on top of the accent color
	muted      lipgloss.Color // secondary text
	dim        lipgloss.Color // faint decoration (dividers, borders)
	text       lipgloss.Color // body text
	textBright lipgloss.Color // emphasized body text (titles)
	danger     lipgloss.Color // destructive actions / close buttons
	warn       lipgloss.Color // status message foreground

	statusBg   lipgloss.Color
	panelBg    lipgloss.Color // dropdowns and dialogs
	panelBgAlt lipgloss.Color // secondary background (headers)

	// selBg is the background of the rows inside a multi-row selection of the
	// result grid. It must read as "selected" next to the much fainter
	// panelBgAlt the caret row and the hovered row sit on, so both palettes
	// give it a colour of their own.
	selBg lipgloss.Color

	schema lipgloss.Color // schema rows in the object tree

	keyword     lipgloss.Color
	ident       lipgloss.Color
	quotedIdent lipgloss.Color
	stringLit   lipgloss.Color
	number      lipgloss.Color
	comment     lipgloss.Color
	operator    lipgloss.Color
	punct       lipgloss.Color
	param       lipgloss.Color
}

// darkPalette suits a dark terminal background.
func darkPalette() palette {
	return palette{
		name: "dark",

		accent:     lipgloss.Color("39"),
		accentFg:   lipgloss.Color("231"),
		muted:      lipgloss.Color("245"),
		dim:        lipgloss.Color("238"),
		text:       lipgloss.Color("252"),
		textBright: lipgloss.Color("250"),
		danger:     lipgloss.Color("203"),
		warn:       lipgloss.Color("229"),

		statusBg:   lipgloss.Color("236"),
		panelBg:    lipgloss.Color("235"),
		panelBgAlt: lipgloss.Color("236"),
		selBg:      lipgloss.Color("24"), // a dark slate blue: selected, not loud

		schema: lipgloss.Color("110"),

		keyword:     lipgloss.Color("75"), // SQL keywords are blue, as in DBeaver/DataGrip
		ident:       lipgloss.Color("252"),
		quotedIdent: lipgloss.Color("114"),
		stringLit:   lipgloss.Color("215"),
		number:      lipgloss.Color("179"),
		comment:     lipgloss.Color("242"),
		operator:    lipgloss.Color("245"),
		punct:       lipgloss.Color("247"),
		param:       lipgloss.Color("208"),
	}
}

// lightPalette suits a light terminal background.
func lightPalette() palette {
	return palette{
		name: "light",

		accent:     lipgloss.Color("26"),
		accentFg:   lipgloss.Color("231"),
		muted:      lipgloss.Color("243"),
		dim:        lipgloss.Color("250"),
		text:       lipgloss.Color("236"),
		textBright: lipgloss.Color("235"),
		danger:     lipgloss.Color("160"),
		warn:       lipgloss.Color("232"),

		statusBg:   lipgloss.Color("253"),
		panelBg:    lipgloss.Color("254"),
		panelBgAlt: lipgloss.Color("252"),
		selBg:      lipgloss.Color("153"), // a light blue: selected on a bright background

		schema: lipgloss.Color("30"),

		keyword:     lipgloss.Color("25"),
		ident:       lipgloss.Color("236"),
		quotedIdent: lipgloss.Color("28"),
		stringLit:   lipgloss.Color("130"),
		number:      lipgloss.Color("94"),
		comment:     lipgloss.Color("243"),
		operator:    lipgloss.Color("240"),
		punct:       lipgloss.Color("245"),
		param:       lipgloss.Color("166"),
	}
}

// paletteFor returns the palette named by a setting, falling back to dark for
// unknown names.
func paletteFor(name string) palette {
	if name == settings.ThemeLight {
		return lightPalette()
	}
	return darkPalette()
}

// theme collects every style the UI draws with. All of them derive from a
// palette so the whole interface can be re-skinned at once.
type theme struct {
	palette palette

	appName lipgloss.Style
	header  lipgloss.Style

	lineNum    lipgloss.Style
	lineNumCur lipgloss.Style
	cursor     lipgloss.Style
	selection  lipgloss.Style // reverse video, readable on any palette

	status      lipgloss.Style
	statusKey   lipgloss.Style
	danger      lipgloss.Style // error text
	statusMsg   lipgloss.Style
	popupBorder lipgloss.Style
	popupSel    lipgloss.Style
	popupLabel  lipgloss.Style
	popupKind   lipgloss.Style
	popupDetail lipgloss.Style
	popupHeader lipgloss.Style

	// Pane chrome
	paneTitle       lipgloss.Style // unfocused pane title
	paneTitleActive lipgloss.Style // focused pane title
	paneClose       lipgloss.Style // close button, unfocused
	paneCloseActive lipgloss.Style // close button, focused
	paneHint        lipgloss.Style // in-pane hint text
	divider         lipgloss.Style // pane divider
	dividerActive   lipgloss.Style // divider being dragged
	expandBtn       lipgloss.Style // expand button
	expandStrip     lipgloss.Style // expand strip background
	objSchema       lipgloss.Style // object tree: schema row
	objTable        lipgloss.Style // object tree: table/view row
	objComment      lipgloss.Style // object tree: comment after the name

	// Result grid
	resultRowSel      lipgloss.Style // selected row background (also the caret row and the hovered row)
	resultRowSelNum   lipgloss.Style // row number inside the selected row
	resultRowSelCell  lipgloss.Style // active cell inside the selected row
	resultRowRangeSel lipgloss.Style // rows inside a multi-row selection (round 7 §3)

	// Menu bar
	menuBarFill     lipgloss.Style // menu bar background
	menuTitle       lipgloss.Style // menu title
	menuTitleActive lipgloss.Style // menu title of the open menu
	menuHover       lipgloss.Style // menu title under the pointer
	menuApp         lipgloss.Style // the application name, bold as on macOS
	menuAppActive   lipgloss.Style // the application name of the open menu
	menuInfo        lipgloss.Style // session info on the right of the menu bar
	menuBox         lipgloss.Style // dropdown background
	menuItemSel     lipgloss.Style // selected dropdown item
	menuBorder      lipgloss.Style // dropdown border

	// Toolbar
	toolBtn      lipgloss.Style // toolbar button
	toolBtnHover lipgloss.Style // toolbar button under the pointer
	toolSep      lipgloss.Style // separator between button groups

	// Scrollbars
	scrollTrack lipgloss.Style // the empty part of a scrollbar
	scrollThumb lipgloss.Style // the thumb

	// Editor tabs
	tabBar    lipgloss.Style // tab bar background
	tab       lipgloss.Style // inactive tab
	tabActive lipgloss.Style // active tab
	tabNew    lipgloss.Style // "new tab" slot

	// Settings dialog
	dlgBorder   lipgloss.Style
	dlgBox      lipgloss.Style
	dlgTitle    lipgloss.Style
	dlgLabel    lipgloss.Style
	dlgValue    lipgloss.Style
	dlgRowSel   lipgloss.Style // selected row background
	dlgValueSel lipgloss.Style // value inside the selected row
	dlgDesc     lipgloss.Style // row description

	keyword     lipgloss.Style
	ident       lipgloss.Style
	quotedIdent lipgloss.Style
	stringLit   lipgloss.Style
	number      lipgloss.Style
	comment     lipgloss.Style
	operator    lipgloss.Style
	punct       lipgloss.Style
	param       lipgloss.Style
	plain       lipgloss.Style
}

// newTheme derives every style from one palette.
func newTheme(p palette) theme {
	fg := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	fbg := func(f, bg lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(f).Background(bg)
	}
	plain := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

	return theme{
		palette: p,

		appName: lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		header:  fg(p.muted),

		lineNum:    fg(lipgloss.Color("240")),
		lineNumCur: lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		cursor:     lipgloss.NewStyle().Reverse(true),
		selection:  lipgloss.NewStyle().Reverse(true),

		status:      fbg(p.text, p.statusBg),
		statusKey:   fbg(p.muted, p.statusBg),
		danger:      lipgloss.NewStyle().Foreground(p.danger).Bold(true),
		statusMsg:   lipgloss.NewStyle().Foreground(p.warn).Background(p.accent).Bold(true),
		popupBorder: fg(p.dim),
		popupSel:    lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		popupLabel:  fg(p.text),
		popupKind:   fg(p.accent),
		popupDetail: fg(lipgloss.Color("244")),
		popupHeader: fbg(lipgloss.Color("240"), p.panelBg),

		paneTitle:       fg(p.muted),
		paneTitleActive: lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		paneClose:       fg(lipgloss.Color("240")),
		paneCloseActive: lipgloss.NewStyle().Foreground(p.danger).Bold(true),
		paneHint:        lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Italic(true),
		divider:         fg(p.dim),
		dividerActive:   lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		expandBtn:       lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		expandStrip:     fg(p.statusBg),
		objSchema:       lipgloss.NewStyle().Foreground(p.schema).Bold(true),
		objTable:        fg(p.text),
		objComment:      lipgloss.NewStyle().Foreground(p.muted).Italic(true),

		resultRowSel:      lipgloss.NewStyle().Foreground(p.text).Background(p.panelBgAlt),
		resultRowSelNum:   lipgloss.NewStyle().Foreground(p.accent).Background(p.panelBgAlt).Bold(true),
		resultRowSelCell:  lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		resultRowRangeSel: lipgloss.NewStyle().Foreground(p.text).Background(p.selBg),

		menuBarFill:     fbg(p.textBright, p.statusBg),
		menuTitle:       fbg(p.textBright, p.statusBg),
		menuTitleActive: lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		menuHover:       lipgloss.NewStyle().Foreground(p.accent).Background(p.statusBg).Bold(true),
		menuApp:         lipgloss.NewStyle().Foreground(p.textBright).Background(p.statusBg).Bold(true),
		menuAppActive:   lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		menuInfo:        fbg(p.muted, p.statusBg),
		menuBox:         fbg(p.text, p.panelBg),
		menuItemSel:     lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		menuBorder:      fbg(lipgloss.Color("240"), p.panelBg),

		// Toolbar buttons are filled pills: terminals cannot draw real icons,
		// so a label on its own background is what makes a button look like one.
		toolBtn:      fbg(p.text, p.dim),
		toolBtnHover: lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		toolSep:      fbg(p.dim, p.statusBg),

		scrollTrack: fg(p.dim),
		scrollThumb: fg(p.muted),

		tabBar:    fbg(p.muted, p.statusBg),
		tab:       fbg(p.textBright, p.panelBgAlt),
		tabActive: lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		tabNew:    fbg(p.accent, p.statusBg),

		dlgBorder:   fbg(lipgloss.Color("240"), p.panelBg),
		dlgBox:      fbg(p.text, p.panelBg),
		dlgTitle:    lipgloss.NewStyle().Foreground(p.accentFg).Background(p.accent).Bold(true),
		dlgLabel:    fg(p.text),
		dlgValue:    plain(p.accent),
		dlgRowSel:   lipgloss.NewStyle().Background(lipgloss.Color("237")),
		dlgValueSel: lipgloss.NewStyle().Foreground(p.accent).Bold(true).Background(lipgloss.Color("237")),
		dlgDesc:     lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true),

		keyword:     lipgloss.NewStyle().Foreground(p.keyword).Bold(true),
		ident:       fg(p.ident),
		quotedIdent: fg(p.quotedIdent),
		stringLit:   fg(p.stringLit),
		number:      fg(p.number),
		comment:     lipgloss.NewStyle().Foreground(p.comment).Italic(true),
		operator:    fg(p.operator),
		punct:       fg(p.punct),
		param:       fg(p.param),
		plain:       lipgloss.NewStyle(),
	}
}

// withPalette re-skins the theme with the palette named by a setting.
func (t theme) withPalette(name string) theme { return newTheme(paletteFor(name)) }

// styleFor returns the style used for a token kind.
func (t theme) styleFor(kind sql.TokenKind) lipgloss.Style {
	switch kind {
	case sql.KindKeyword:
		return t.keyword
	case sql.KindIdent:
		return t.ident
	case sql.KindQuotedIdent:
		return t.quotedIdent
	case sql.KindString:
		return t.stringLit
	case sql.KindNumber:
		return t.number
	case sql.KindComment:
		return t.comment
	case sql.KindOperator:
		return t.operator
	case sql.KindPunct:
		return t.punct
	case sql.KindParam:
		return t.param
	default:
		return t.plain
	}
}
