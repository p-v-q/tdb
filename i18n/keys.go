package i18n

import "sort"

// Key identifies a user-visible message. Keys are dotted and grouped by area so
// that they stay greppable and easy to move around.
type Key string

// Every message key. Adding a key requires translating it in all catalogs;
// Validate (asserted from tests) enforces that.
const (
	// Common
	KeyAppLoading Key = "app.loading"
	KeyOn         Key = "common.on"
	KeyOff        Key = "common.off"

	// Language names
	KeyLocaleName Key = "locale.name"

	// Application menu (macOS style: the app name is the first menu)
	KeyAppName  Key = "app.name"
	KeyAppAbout Key = "app.about"
	KeyAppQuit  Key = "app.quit"

	// Connections
	KeyConnTitle    Key = "conn.title"
	KeyConnName     Key = "conn.name"
	KeyConnDriver   Key = "conn.driver"
	KeyConnHost     Key = "conn.host"
	KeyConnPort     Key = "conn.port"
	KeyConnUser     Key = "conn.user"
	KeyConnPassword Key = "conn.password"
	KeyConnDatabase Key = "conn.database"
	KeyConnSchema   Key = "conn.schema"
	KeyConnFile     Key = "conn.file"
	KeyConnDSN      Key = "conn.dsn"
	// KeyConnSSLMode is the transport-encryption field of a server driver; its
	// value is one of the driver's protocol keywords (see connSSLModes).
	KeyConnSSLMode Key = "conn.sslMode"
	// KeyConnSSLDefault is shown for the empty SSL mode: it names no protocol
	// keyword but the UI concept "the driver's own default".
	KeyConnSSLDefault Key = "conn.sslDefault"
	// KeyConnSchemaDefault is the first entry of the schema list: no schema
	// is named, so unqualified names resolve in the server default.
	KeyConnSchemaDefault Key = "conn.schemaDefault"
	// KeyConnListFailed reports a server list the form could not read.
	KeyConnListFailed         Key = "conn.listFailed"
	KeyConnConnect            Key = "conn.connect"
	KeyConnCancel             Key = "conn.cancel"
	KeyConnHint               Key = "conn.hint"
	KeyConnNameRequired       Key = "conn.nameRequired"
	KeyConnFailed             Key = "conn.failed"
	KeyConnConnected          Key = "conn.connected"
	KeyConnDriverPending      Key = "conn.driverPending"
	KeyConnEditTitle          Key = "conn.editTitle"
	KeyConnSave               Key = "conn.save"
	KeyConnSaveHint           Key = "conn.saveHint"
	KeyConnSaved              Key = "conn.saved"
	KeyConnDeleted            Key = "conn.deleted"
	KeyConnTesting            Key = "conn.testing"
	KeyConnTestOK             Key = "conn.testOK"
	KeyConnTestFailed         Key = "conn.testFailed"
	KeyConnTestPending        Key = "conn.testPending"
	KeyConnReconnect          Key = "conn.reconnect"
	KeyConnDisconnect         Key = "conn.disconnect"
	KeyConnReconnected        Key = "conn.reconnected"
	KeyConnDisconnected       Key = "conn.disconnected"
	KeyConnTimeout            Key = "conn.timeout"
	KeyConnLost               Key = "conn.lost"
	KeyConnCloseFailed        Key = "conn.closeFailed"
	KeyConnPasswordEnv        Key = "conn.passwordEnv"
	KeyConnPasswordEnvHint    Key = "conn.passwordEnvHint"
	KeyConnPasswordEnvMissing Key = "conn.passwordEnvMissing"
	// KeyConnSchemaMissing warns that the connection's default schema does	// not exist on the database: the connection stays usable, but it works in
	// the server's default schema.
	KeyConnSchemaMissing Key = "conn.schemaMissing"

	// Section titles of the connection form. They are drawn as quiet header
	// rows above the first field of their group and are never focusable.
	KeyConnSectionServer      Key = "conn.section.server"
	KeyConnSectionCredentials Key = "conn.section.credentials"
	KeyConnSectionDatabase    Key = "conn.section.database"
	KeyConnSectionAdvanced    Key = "conn.section.advanced"

	// Object browser context menu
	KeyObjMenuNewConn    Key = "objects.menu.newConnection"
	KeyObjMenuConnect    Key = "objects.menu.connect"
	KeyObjMenuTest       Key = "objects.menu.test"
	KeyObjMenuEdit       Key = "objects.menu.edit"
	KeyObjMenuDelete     Key = "objects.menu.delete"
	KeyObjMenuRefresh    Key = "objects.menu.refresh"
	KeyObjMenuInsertName Key = "objects.menu.insertName"
	KeyObjMenuCopyName   Key = "objects.menu.copyName"
	KeyObjMenuGenSelect  Key = "objects.menu.generateSelect"
	KeyObjMenuGenInsert  Key = "objects.menu.generateInsert"
	KeyObjMenuGenUpdate  Key = "objects.menu.generateUpdate"
	KeyObjMenuGenDelete  Key = "objects.menu.generateDelete"
	KeyObjMenuViewData   Key = "objects.menu.viewData"
	KeyObjMenuViewDef    Key = "objects.menu.viewDefinition"

	// Object tree resource groups
	KeyObjGroupTables            Key = "objects.group.tables"
	KeyObjGroupViews             Key = "objects.group.views"
	KeyObjGroupMaterializedViews Key = "objects.group.materializedViews"
	KeyObjGroupFunctions         Key = "objects.group.functions"
	KeyObjGroupProcedures        Key = "objects.group.procedures"
	KeyObjGroupTriggers          Key = "objects.group.triggers"
	KeyObjGroupIndexes           Key = "objects.group.indexes"
	KeyObjGroupSequences         Key = "objects.group.sequences"
	KeyObjGroupCount             Key = "objects.group.count"
	KeyObjOn                     Key = "objects.on"
	// KeyObjSchemaDefault marks the schema the connection works in.
	KeyObjSchemaDefault Key = "objects.schemaDefault"

	// Object tree filter
	KeyObjNoMatches Key = "objects.noMatches"

	// Title bar
	KeyHeaderUnnamed Key = "header.unnamed"
	KeyHeaderNoMeta  Key = "header.noMeta"
	KeyHeaderMeta    Key = "header.meta"
	// The metadata size is assembled from these four so a database with one
	// schema or one table reads "1 schema / 1 table" instead of the
	// ungrammatical plural form.
	KeyHeaderSchemaOne Key = "header.schemaOne"
	KeyHeaderSchemas   Key = "header.schemas"
	KeyHeaderTableOne  Key = "header.tableOne"
	KeyHeaderTables    Key = "header.tables"
	KeyHeaderConn      Key = "header.connection"
	KeyHeaderFile      Key = "header.file"
	// KeyHeaderDatabase names the database a connection is attached to: on a
	// server with several databases the connection name alone does not say
	// which one the statements run in.
	KeyHeaderDatabase Key = "header.database"

	// Panes
	KeyPaneObjects       Key = "pane.objects"
	KeyPaneEditor        Key = "pane.editor"
	KeyPaneResults       Key = "pane.results"
	KeyPaneResultsHidden Key = "pane.resultsHidden"
	// KeyPaneResizing is the hint drawn in the panes while a divider drag is
	// moving them: the contents are rendered again on release.
	KeyPaneResizing Key = "pane.resizing"

	// Status bar
	KeyStatusContext      Key = "status.context"
	KeyStatusQualifier    Key = "status.qualifier"
	KeyStatusSelection    Key = "status.selection"
	KeyStatusCandidates   Key = "status.candidates"
	KeyStatusHints        Key = "status.hints"
	KeyStatusUncommitted  Key = "status.uncommitted"
	KeyStatusManualCommit Key = "status.manualCommit"
	// Focus-aware status-bar text: the object browser reports its selection
	// (or its filter input) on the left and shows pane-specific key hints on
	// the right, so the shortcuts of the pane that owns the keyboard are the
	// ones on screen.
	KeyStatusObjectPosition     Key = "status.objectPosition"
	KeyStatusObjectsHints       Key = "status.objectsHints"
	KeyStatusObjectsFilterHints Key = "status.objectsFilterHints"
	KeyStatusResultsHints       Key = "status.resultsHints"
	KeyStatusFiltering          Key = "status.filtering"

	// Find prompt
	KeyFindLabel     Key = "find.label"
	KeyFindEmpty     Key = "find.empty"
	KeyFindMatches   Key = "find.matches"
	KeyFindNoMatches Key = "find.noMatches"
	KeyFindHint      Key = "find.hint"

	// File and directory picker, shared by "open SQL file" and the export
	// destination field
	KeyPickerOpenTitle  Key = "picker.openTitle"
	KeyPickerDirTitle   Key = "picker.dirTitle"
	KeyPickerEmpty      Key = "picker.empty"
	KeyPickerNoMatch    Key = "picker.noMatch"
	KeyPickerUnreadable Key = "picker.unreadable"
	KeyPickerParent     Key = "picker.parent"
	KeyPickerChoose     Key = "picker.choose"
	KeyPickerHint       Key = "picker.hint"
	// KeyPickerOpen labels the picker's primary button in file mode; the
	// directory mode uses KeyPickerChoose.
	KeyPickerOpen Key = "picker.open"
	// KeyPickerSuffix says what the listing is cut down to when the picker
	// was opened with a file-extension restriction ("open SQL file").
	// %s is the extension exactly as the restriction is spelled (".sql").
	KeyPickerSuffix Key = "picker.suffix"

	// Query history
	KeyHistoryTitle      Key = "history.title"
	KeyHistoryEmpty      Key = "history.empty"
	KeyHistoryTime       Key = "history.time"
	KeyHistoryConnection Key = "history.connection"
	KeyHistorySQL        Key = "history.sql"
	KeyHistoryHint       Key = "history.hint"
	KeyHistoryDeleted    Key = "history.deleted"
	KeyHistoryFilterHint Key = "history.filterHint"
	KeyHistoryNoMatch    Key = "history.noMatch"

	// Editor tabs
	KeyTabRename       Key = "tab.rename"
	KeyTabDuplicate    Key = "tab.duplicate"
	KeyTabClose        Key = "tab.close"
	KeyTabCloseOthers  Key = "tab.closeOthers"
	KeyTabCloseLeft    Key = "tab.closeLeft"
	KeyTabCloseRight   Key = "tab.closeRight"
	KeyTabCloseAll     Key = "tab.closeAll"
	KeyTabRenamePrompt Key = "tab.renamePrompt"
	KeyTabRenameHint   Key = "tab.renameHint"
	KeyTabRenameAuto   Key = "tab.renameAuto"
	KeyTabRenamed      Key = "tab.renamed"
	KeyTabDuplicated   Key = "tab.duplicated"
	KeyTabClosed       Key = "tab.closed"
	KeyTabOthersClosed Key = "tab.othersClosed"
	KeyTabLeftClosed   Key = "tab.leftClosed"
	KeyTabRightClosed  Key = "tab.rightClosed"
	KeyTabAllClosed    Key = "tab.allClosed"

	// The kind of a tab that holds a generated statement, shown in brackets
	// after the object's name.
	KeyTabKindSelect Key = "tab.kind.select"
	KeyTabKindInsert Key = "tab.kind.insert"
	KeyTabKindUpdate Key = "tab.kind.update"
	KeyTabKindDelete Key = "tab.kind.delete"
	KeyTabKindData   Key = "tab.kind.data"

	// Welcome tab: the first tab of a session when nothing else is open
	KeyWelcomeTab      Key = "welcome.tab"
	KeyWelcomeTitle    Key = "welcome.title"
	KeyWelcomeLead     Key = "welcome.lead"
	KeyWelcomeNewQuery Key = "welcome.newQuery"
	KeyWelcomeSettings Key = "welcome.settings"
	KeyWelcomeKeys     Key = "welcome.keys"
	KeyWelcomeNoConns  Key = "welcome.noConnections"
	KeyWelcomeHint     Key = "welcome.hint"

	// SQL context kinds shown in the status bar
	KeyCtxNone      Key = "ctx.none"
	KeyCtxDefault   Key = "ctx.default"
	KeyCtxTable     Key = "ctx.table"
	KeyCtxColumn    Key = "ctx.column"
	KeyCtxQualified Key = "ctx.qualified"
	KeyCtxSchema    Key = "ctx.schema"

	// Results grid
	KeyResultsEmpty          Key = "results.empty"
	KeyResultsRows           Key = "results.rows"
	KeyResultsOneRow         Key = "results.oneRow"
	KeyResultsAffected       Key = "results.affected"
	KeyResultsOneRowAffected Key = "results.oneRowAffected"
	KeyResultsTruncated      Key = "results.truncated"
	KeyResultsError          Key = "results.error"
	KeyResultsFilter         Key = "results.filter"
	KeyResultsFiltered       Key = "results.filtered"
	KeyResultsSet            Key = "results.set"
	KeyResultsSetHint        Key = "results.setHint"
	KeyResultsCaretHint      Key = "results.hint"
	KeyResultsResizeHint     Key = "results.resizeHint"
	KeyResultsSelected       Key = "results.selected"
	KeyResultsOneRowSelected Key = "results.oneRowSelected"
	KeyStatusRunning         Key = "status.running"

	// Editing a cell of the result grid
	KeyResultEdit                      Key = "result.edit"
	KeyResultApplyChanges              Key = "result.applyChanges"
	KeyResultDiscardChanges            Key = "result.discardChanges"
	KeyResultEditHint                  Key = "result.editHint"
	KeyResultPendingChanges            Key = "result.pendingChanges"
	KeyResultOnePendingChange          Key = "result.onePendingChange"
	KeyResultChangesApplied            Key = "result.changesApplied"
	KeyResultOneChangeApplied          Key = "result.oneChangeApplied"
	KeyResultChangesDiscarded          Key = "result.changesDiscarded"
	KeyResultEditFailed                Key = "result.editFailed"
	KeyResultNotEditable               Key = "result.notEditable"
	KeyResultNotEditableReasonQuery    Key = "result.notEditableReasonQuery"
	KeyResultNotEditableReasonNoKey    Key = "result.notEditableReasonNoKey"
	KeyResultNotEditableReasonKeyNotIn Key = "result.notEditableReasonKeyNotIn"
	KeyResultNotEditableReasonReadOnly Key = "result.notEditableReasonReadOnly"
	// KeyResultNotEditableReasonAmbiguousTable says that the table name is
	// not qualified and several schemas hold a table of that name: the
	// server may have read a different one than the catalog lists.
	KeyResultNotEditableReasonAmbiguousTable Key = "result.notEditableReasonAmbiguousTable"
	// KeyResultNotEditableReasonAmbiguousColumn says that a result column
	// matches several table columns when identifier case is folded.
	KeyResultNotEditableReasonAmbiguousColumn Key = "result.notEditableReasonAmbiguousColumn"
	// KeyResultEditKeyColumn says that the cell is part of the primary key,
	// which addresses the row and must not be rewritten.
	KeyResultEditKeyColumn Key = "result.editKeyColumn"
	// KeyResultEditNoRows says that an update did not affect any row.
	KeyResultEditNoRows Key = "result.editNoRows"
	// KeyResultEditTooManyRows says that an update affected more than one
	// row, which means the key did not address a single row.
	KeyResultEditTooManyRows Key = "result.editTooManyRows"
	// KeyResultEditRolledBack says that the batch was rolled back, so
	// nothing of it reached the database.
	KeyResultEditRolledBack Key = "result.editRolledBack"
	// KeyResultEditWrongConnection says that the pending changes belong to
	// another database connection.
	KeyResultEditWrongConnection Key = "result.editWrongConnection"

	// Result grid context menu
	KeyResultMenuCopyCell     Key = "resultMenu.copyCell"
	KeyResultMenuCopyRow      Key = "resultMenu.copyRow"
	KeyResultMenuCopyColumn   Key = "resultMenu.copyColumn"
	KeyResultMenuCopyAll      Key = "resultMenu.copyAll"
	KeyResultMenuCopyColName  Key = "resultMenu.copyColumnName"
	KeyResultMenuSortAsc      Key = "resultMenu.sortAsc"
	KeyResultMenuSortDesc     Key = "resultMenu.sortDesc"
	KeyResultMenuSortClear    Key = "resultMenu.sortClear"
	KeyResultMenuFitColumn    Key = "resultMenu.fitColumn"
	KeyResultMenuFilterCell   Key = "resultMenu.filterCell"
	KeyResultMenuClearFilter  Key = "resultMenu.clearFilter"
	KeyResultMenuPrevSet      Key = "resultMenu.prevSet"
	KeyResultMenuNextSet      Key = "resultMenu.nextSet"
	KeyResultMenuExport       Key = "resultMenu.export"
	KeyResultMenuClearResults Key = "resultMenu.clearResults"

	// Result export
	KeyExportTitle    Key = "export.title"
	KeyExportFile     Key = "export.file"
	KeyExportFormat   Key = "export.format"
	KeyExportCSV      Key = "export.csv"
	KeyExportTSV      Key = "export.tsv"
	KeyExportJSON     Key = "export.json"
	KeyExportMarkdown Key = "export.markdown"
	KeyExportAction   Key = "export.action"
	// KeyExportHintShort is the export hint for a narrow screen: the Esc
	// hint must stay visible even when the value column is tiny.
	KeyExportHintShort    Key = "export.hintShort"
	KeyExportHint         Key = "export.hint"
	KeyExportPathRequired Key = "export.pathRequired"
	KeyExportDirectory    Key = "export.directory"
	KeyExportBrowse       Key = "export.browse"

	// Completion popup
	KeyPopupHeader Key = "popup.header"
	KeyPopupHint   Key = "popup.hint"

	// Menu titles
	KeyMenuFile     Key = "menu.file"
	KeyMenuEdit     Key = "menu.edit"
	KeyMenuView     Key = "menu.view"
	KeyMenuQuery    Key = "menu.query"
	KeyMenuSettings Key = "menu.settings"
	KeyMenuHelp     Key = "menu.help"

	// Menu items
	KeyMenuSave           Key = "menu.item.save"
	KeyMenuOpenFile       Key = "menu.item.openFile"
	KeyMenuQuit           Key = "menu.item.quit"
	KeyMenuNewTab         Key = "menu.item.newTab"
	KeyMenuCloseTab       Key = "menu.item.closeTab"
	KeyMenuNextTab        Key = "menu.item.nextTab"
	KeyMenuPrevTab        Key = "menu.item.prevTab"
	KeyMenuUndo           Key = "menu.item.undo"
	KeyMenuRedo           Key = "menu.item.redo"
	KeyMenuSelectAll      Key = "menu.item.selectAll"
	KeyMenuFind           Key = "menu.item.find"
	KeyMenuHistory        Key = "menu.item.history"
	KeyMenuCopy           Key = "menu.item.copy"
	KeyMenuCut            Key = "menu.item.cut"
	KeyMenuToggleComment  Key = "menu.item.toggleComment"
	KeyMenuPaneObjects    Key = "menu.item.paneObjects"
	KeyMenuPaneResults    Key = "menu.item.paneResults"
	KeyMenuFocusObjects   Key = "menu.item.focusObjects"
	KeyMenuFocusEditor    Key = "menu.item.focusEditor"
	KeyMenuFocusResults   Key = "menu.item.focusResults"
	KeyMenuClosePane      Key = "menu.item.closePane"
	KeyMenuRun            Key = "menu.item.run"
	KeyMenuExplain        Key = "menu.item.explain"
	KeyMenuClearResults   Key = "menu.item.clearResults"
	KeyMenuExport         Key = "menu.item.export"
	KeyMenuPreferences    Key = "menu.item.preferences"
	KeyMenuOpenConfigFile Key = "menu.item.openConfigFile"
	KeyMenuKeymap         Key = "menu.item.keymap"
	KeyMenuReadOnly       Key = "menu.item.readOnly"
	KeyMenuManualCommit   Key = "menu.item.manualCommit"
	KeyMenuCommit         Key = "menu.item.commit"
	KeyMenuRollback       Key = "menu.item.rollback"

	// Key bindings overlay
	KeyKeysTitle      Key = "keys.title"
	KeyKeysHint       Key = "keys.hint"
	KeyKeysObjects    Key = "keys.objects"
	KeyKeysResults    Key = "keys.results"
	KeyKeysEditor     Key = "keys.editor"
	KeyKeysMove       Key = "keys.move"
	KeyKeysFold       Key = "keys.fold"
	KeyKeysOpen       Key = "keys.open"
	KeyKeysFilter     Key = "keys.filter"
	KeyKeysMenu       Key = "keys.menu"
	KeyKeysSort       Key = "keys.sort"
	KeyKeysCopyCell   Key = "keys.copyCell"
	KeyKeysCopyRow    Key = "keys.copyRow"
	KeyKeysResultSets Key = "keys.resultSets"
	KeyKeysBack       Key = "keys.back"
	KeyKeysComplete   Key = "keys.complete"
	KeyKeysAccept     Key = "keys.accept"

	// Toolbar
	KeyToolFormat Key = "toolbar.format"

	// Settings dialog
	KeySettingsTitle Key = "settings.title"
	// KeySettingsHintShort is the hint of the settings dialog for a screen
	// too narrow for the full one: the Esc hint must stay visible.
	KeySettingsHintShort    Key = "settings.hintShort"
	KeySettingsHint         Key = "settings.hint"
	KeySettingsLanguage     Key = "settings.language"
	KeySettingsTheme        Key = "settings.theme"
	KeySettingsIcons        Key = "settings.icons"
	KeySettingsMouse        Key = "settings.mouse"
	KeySettingsGutter       Key = "settings.gutter"
	KeySettingsResetLayout  Key = "settings.resetLayout"
	KeySettingsFollowSystem Key = "settings.followSystem"
	KeySettingsThemeDark    Key = "settings.theme.dark"
	KeySettingsThemeLight   Key = "settings.theme.light"
	KeySettingsDescLanguage Key = "settings.desc.language"
	KeySettingsDescTheme    Key = "settings.desc.theme"
	KeySettingsDescIcons    Key = "settings.desc.icons"
	KeySettingsDescMouse    Key = "settings.desc.mouse"
	KeySettingsDescGutter   Key = "settings.desc.gutter"
	KeySettingsDescLayout   Key = "settings.desc.layout"
	KeySettingsReadOnly     Key = "settings.readOnly"
	KeySettingsDescReadOnly Key = "settings.desc.readOnly"
	KeySettingsBackupKept   Key = "settings.backupKept"

	// Vault (the passphrase that seals the saved connections). Titles, field
	// labels and outcome messages; nothing here ever carries the passphrase
	// itself — a wrong-password message is a fixed string, and the hint is the
	// only value echoed back (it is public by design).
	KeyVaultTitle            Key = "vault.title"
	KeyVaultSetupTitle       Key = "vault.setupTitle"
	KeyVaultChangeTitle      Key = "vault.changeTitle"
	KeyVaultRemoveTitle      Key = "vault.removeTitle"
	KeyVaultManageTitle      Key = "vault.manageTitle"
	KeyVaultHintTitle        Key = "vault.hintTitle"
	KeyVaultPassphrase       Key = "vault.passphrase"
	KeyVaultConfirm          Key = "vault.confirm"
	KeyVaultHint             Key = "vault.hint"
	KeyVaultCurrent          Key = "vault.current"
	KeyVaultSet              Key = "vault.set"
	KeyVaultUnlock           Key = "vault.unlock"
	KeyVaultChange           Key = "vault.change"
	KeyVaultRemove           Key = "vault.remove"
	KeyVaultFormHint         Key = "vault.formHint"
	KeyVaultManageHint       Key = "vault.manageHint"
	KeyVaultHintLine         Key = "vault.hintLine"
	KeyVaultMinNote          Key = "vault.minNote"
	KeyVaultHintWarning      Key = "vault.hintWarning"
	KeyVaultNoRecovery       Key = "vault.noRecovery"
	KeyVaultWeak             Key = "vault.weak"
	KeyVaultTooShort         Key = "vault.tooShort"
	KeyVaultMismatch         Key = "vault.mismatch"
	KeyVaultWrong            Key = "vault.wrong"
	KeyVaultHintIsPassphrase Key = "vault.hintIsPassphrase"
	KeyVaultUnlocked         Key = "vault.unlocked"
	KeyVaultLocked           Key = "vault.locked"
	KeyVaultLockedConnLive   Key = "vault.lockedConnLive"
	KeyVaultSetOK            Key = "vault.setOK"
	KeyVaultChanged          Key = "vault.changed"
	KeyVaultRemoved          Key = "vault.removed"
	KeyVaultRemoveWarning    Key = "vault.removeWarning"
	KeyVaultLockedTree       Key = "vault.lockedTree"
	KeyVaultUnlockFirst      Key = "vault.unlockFirst"
	KeyVaultPlaintextWarning Key = "vault.plaintextWarning"
	KeyVaultMenu             Key = "vault.menu"
	KeyVaultLockNow          Key = "vault.lockNow"
	KeyVaultManageChange     Key = "vault.manageChange"
	KeyVaultManageRemove     Key = "vault.manageRemove"
	KeyVaultManageChangeHint Key = "vault.manageChangeHint"
	KeyVaultNewHint          Key = "vault.newHint"
	KeyVaultHintSet          Key = "vault.hintSet"
	KeyVaultUpgraded         Key = "vault.upgraded"

	// Execution confirmation and risk labels
	KeyConfirmTitle       Key = "confirm.title"
	KeyConfirmSummary     Key = "confirm.summary"
	KeyConfirmOneSummary  Key = "confirm.oneSummary"
	KeyConfirmWarning     Key = "confirm.warning"
	KeyConfirmTooSmall    Key = "confirm.tooSmall"
	KeyConfirmMore        Key = "confirm.more"
	KeyConfirmRun         Key = "confirm.run"
	KeyConfirmHint        Key = "confirm.hint"
	KeyRiskDDL            Key = "risk.ddl"
	KeyRiskDangerousWrite Key = "risk.dangerousWrite"
	KeyRiskWrite          Key = "risk.write"

	// Read-only mode
	KeyMsgReadOnlyEnabled    Key = "msg.readOnlyEnabled"
	KeyMsgReadOnlyDisabled   Key = "msg.readOnlyDisabled"
	KeyMsgReadOnlyBlocked    Key = "msg.readOnlyBlocked"
	KeyMsgOneReadOnlyBlocked Key = "msg.oneReadOnlyBlocked"

	// Transaction control (manual commit mode)
	KeyMsgManualCommitOn       Key = "msg.manualCommitOn"
	KeyMsgManualCommitOff      Key = "msg.manualCommitOff"
	KeyMsgNoTransaction        Key = "msg.noTransaction"
	KeyMsgTxCommitted          Key = "msg.txCommitted"
	KeyMsgTxCommitFailed       Key = "msg.txCommitFailed"
	KeyMsgTxRolledBack         Key = "msg.txRolledBack"
	KeyMsgTxRollbackFailed     Key = "msg.txRollbackFailed"
	KeyMsgTxSwitchBlocked      Key = "msg.txSwitchBlocked"
	KeyMsgTxBusy               Key = "msg.txBusy"
	KeyMsgTxRolledBackOnSwitch Key = "msg.txRolledBackOnSwitch"

	// Status messages
	KeyMsgNoConnection      Key = "msg.noConnection"
	KeyMsgNothingToRun      Key = "msg.nothingToRun"
	KeyMsgNothingToFormat   Key = "msg.nothingToFormat"
	KeyMsgQueryCancelled    Key = "msg.queryCancelled"
	KeyMsgCatalogFailed     Key = "msg.catalogFailed"
	KeyMsgCatalogLoaded     Key = "msg.catalogLoaded"
	KeyMsgNotImplemented    Key = "msg.notImplemented"
	KeyMsgFormatPending     Key = "msg.formatPending"
	KeyMsgFormatted         Key = "msg.formatted"
	KeyMsgExported          Key = "msg.exported"
	KeyMsgOneRowExported    Key = "msg.oneRowExported"
	KeyMsgExportFailed      Key = "msg.exportFailed"
	KeyMsgNoResultsToExport Key = "msg.noResultsToExport"
	KeyMsgNoFileToSave      Key = "msg.noFileToSave"
	KeyMsgSaveFailed        Key = "msg.saveFailed"
	KeyMsgSaved             Key = "msg.saved"
	KeyMsgCopyFailed        Key = "msg.copyFailed"
	KeyMsgCopied            Key = "msg.copied"
	KeyMsgNoSelectionCopy   Key = "msg.noSelectionCopy"
	KeyMsgNoSelectionCut    Key = "msg.noSelectionCut"
	KeyMsgKeymap            Key = "msg.keymap"
	KeyMsgAbout             Key = "msg.about"
	KeyMsgSettingsSaved     Key = "msg.settingsSaved"
	KeyMsgSettingsPath      Key = "msg.settingsPath"
	KeyMsgSettingsFailed    Key = "msg.settingsFailed"
	KeyMsgLayoutReset       Key = "msg.layoutReset"
	KeyMsgLanguageChanged   Key = "msg.languageChanged"
	KeyMsgMouseEnabled      Key = "msg.mouseEnabled"
	KeyMsgMouseDisabled     Key = "msg.mouseDisabled"
	KeyMsgGutterEnabled     Key = "msg.gutterEnabled"
	KeyMsgGutterDisabled    Key = "msg.gutterDisabled"
	KeyMsgOpenConfigFailed  Key = "msg.openConfigFailed"
	KeyMsgNameCopied        Key = "msg.nameCopied"
	KeyMsgDefinitionLoading Key = "msg.definitionLoading"
	KeyMsgDefinitionOpened  Key = "msg.definitionOpened"
	KeyMsgDefinitionFailed  Key = "msg.definitionFailed"
	KeyMsgGenerateFailed    Key = "msg.generateFailed"
	KeyMsgFileOpenFailed    Key = "msg.fileOpenFailed"
	KeyMsgFileTooLarge      Key = "msg.fileTooLarge"
	KeyMsgFileNotUTF8       Key = "msg.fileNotUTF8"
	KeyMsgCopiedRows        Key = "msg.copiedRows"
	KeyMsgOneRowCopied      Key = "msg.oneRowCopied"

	// Command line interface
	KeyCLIDriver         Key = "cli.flag.driver"
	KeyCLIDSN            Key = "cli.flag.dsn"
	KeyCLIVersion        Key = "cli.flag.version"
	KeyCLIDrivers        Key = "cli.flag.drivers"
	KeyCLISettings       Key = "cli.flag.settings"
	KeyCLILang           Key = "cli.flag.lang"
	KeyCLIPrintSettings  Key = "cli.flag.printSettings"
	KeyCLIUsage          Key = "cli.usage"
	KeyCLINotImpl        Key = "cli.notImplemented"
	KeyCLIConnFailed     Key = "cli.connFailed"
	KeyCLIDriversHead    Key = "cli.driversHeader"
	KeyCLIDriverOK       Key = "cli.driverAvailable"
	KeyCLIDriverPending  Key = "cli.driverPending"
	KeyCLIDialectsHead   Key = "cli.dialectsHeader"
	KeyCLIDialectLine    Key = "cli.dialectLine"
	KeyCLIReadFailed     Key = "cli.readFailed"
	KeyCLIWarningMeta    Key = "cli.warnMeta"
	KeyCLISettingsFailed Key = "cli.settingsFailed"
	KeyCLIRunFailed      Key = "cli.runFailed"
)

// allKeys lists every declared key. It is the registry the catalogs are checked
// against (see Validate), so a new key has to be added here and translated in
// every catalog.
var allKeys = []Key{
	KeyAppLoading, KeyOn, KeyOff,
	KeyLocaleName,
	KeyAppName, KeyAppAbout, KeyAppQuit,
	KeyConnTitle, KeyConnName, KeyConnDriver, KeyConnHost, KeyConnPort,
	KeyConnUser, KeyConnPassword, KeyConnDatabase, KeyConnSchema, KeyConnFile, KeyConnDSN,
	KeyConnSSLMode, KeyConnSSLDefault, KeyConnSchemaDefault, KeyConnListFailed,
	KeyConnConnect, KeyConnCancel, KeyConnHint, KeyConnNameRequired,
	KeyConnFailed, KeyConnConnected, KeyConnDriverPending,
	KeyConnEditTitle, KeyConnSave, KeyConnSaveHint, KeyConnSaved, KeyConnDeleted,
	KeyConnTesting, KeyConnTestOK, KeyConnTestFailed, KeyConnTestPending,
	KeyConnReconnect, KeyConnDisconnect, KeyConnReconnected, KeyConnDisconnected,
	KeyConnTimeout, KeyConnLost, KeyConnCloseFailed, KeyConnPasswordEnv, KeyConnPasswordEnvHint,
	KeyConnPasswordEnvMissing, KeyConnSchemaMissing,
	KeyConnSectionServer, KeyConnSectionCredentials, KeyConnSectionDatabase, KeyConnSectionAdvanced,
	KeyObjMenuNewConn, KeyObjMenuConnect, KeyObjMenuTest, KeyObjMenuEdit,
	KeyObjMenuDelete, KeyObjMenuRefresh, KeyObjMenuInsertName, KeyObjMenuCopyName,
	KeyObjMenuGenSelect, KeyObjMenuGenInsert, KeyObjMenuGenUpdate, KeyObjMenuGenDelete,
	KeyObjMenuViewData, KeyObjMenuViewDef,
	KeyObjGroupTables, KeyObjGroupViews, KeyObjGroupMaterializedViews,
	KeyObjGroupFunctions, KeyObjGroupProcedures, KeyObjGroupTriggers,
	KeyObjGroupIndexes, KeyObjGroupSequences, KeyObjGroupCount, KeyObjOn,
	KeyObjSchemaDefault,
	KeyObjNoMatches,
	KeyHeaderUnnamed, KeyHeaderNoMeta, KeyHeaderMeta, KeyHeaderConn, KeyHeaderFile, KeyHeaderDatabase,
	KeyHeaderSchemaOne, KeyHeaderSchemas, KeyHeaderTableOne, KeyHeaderTables,
	KeyPaneObjects, KeyPaneEditor, KeyPaneResults, KeyPaneResultsHidden, KeyPaneResizing,
	KeyStatusContext, KeyStatusQualifier, KeyStatusSelection, KeyStatusCandidates, KeyStatusHints,
	KeyStatusUncommitted, KeyStatusManualCommit,
	KeyStatusObjectPosition, KeyStatusObjectsHints, KeyStatusObjectsFilterHints,
	KeyStatusResultsHints, KeyStatusFiltering,
	KeyFindLabel, KeyFindEmpty, KeyFindMatches, KeyFindNoMatches, KeyFindHint,
	KeyPickerOpenTitle, KeyPickerDirTitle, KeyPickerEmpty, KeyPickerNoMatch,
	KeyPickerUnreadable, KeyPickerParent, KeyPickerChoose, KeyPickerHint,
	KeyPickerOpen, KeyPickerSuffix,
	KeyHistoryTitle, KeyHistoryEmpty, KeyHistoryTime, KeyHistoryConnection, KeyHistorySQL, KeyHistoryHint, KeyHistoryDeleted,
	KeyHistoryFilterHint, KeyHistoryNoMatch,
	KeyTabRename, KeyTabDuplicate, KeyTabClose, KeyTabCloseOthers, KeyTabCloseLeft,
	KeyTabCloseRight, KeyTabCloseAll, KeyTabRenamePrompt,
	KeyTabRenameHint, KeyTabRenameAuto, KeyTabRenamed, KeyTabDuplicated,
	KeyTabClosed, KeyTabOthersClosed, KeyTabLeftClosed, KeyTabRightClosed, KeyTabAllClosed,
	KeyTabKindSelect, KeyTabKindInsert, KeyTabKindUpdate, KeyTabKindDelete,
	KeyTabKindData,
	KeyWelcomeTab, KeyWelcomeTitle, KeyWelcomeLead, KeyWelcomeNewQuery,
	KeyWelcomeSettings, KeyWelcomeKeys, KeyWelcomeNoConns, KeyWelcomeHint,
	KeyCtxNone, KeyCtxDefault, KeyCtxTable, KeyCtxColumn, KeyCtxQualified, KeyCtxSchema,
	KeyResultsEmpty, KeyResultsRows, KeyResultsOneRow, KeyResultsAffected, KeyResultsOneRowAffected, KeyResultsTruncated,
	KeyResultsOneRowSelected,
	KeyResultsError, KeyResultsFilter, KeyResultsFiltered, KeyResultsSet, KeyResultsSetHint,
	KeyResultsCaretHint, KeyResultsResizeHint, KeyResultsSelected, KeyStatusRunning,
	KeyResultEdit, KeyResultApplyChanges, KeyResultDiscardChanges, KeyResultEditHint,
	KeyResultPendingChanges, KeyResultOnePendingChange, KeyResultChangesApplied, KeyResultOneChangeApplied, KeyResultChangesDiscarded,
	KeyResultEditFailed, KeyResultNotEditable, KeyResultNotEditableReasonQuery,
	KeyResultNotEditableReasonNoKey, KeyResultNotEditableReasonKeyNotIn,
	KeyResultNotEditableReasonReadOnly, KeyResultNotEditableReasonAmbiguousTable,
	KeyResultNotEditableReasonAmbiguousColumn,
	KeyResultEditKeyColumn, KeyResultEditNoRows, KeyResultEditTooManyRows,
	KeyResultEditRolledBack, KeyResultEditWrongConnection,
	KeyResultMenuCopyCell, KeyResultMenuCopyRow, KeyResultMenuCopyColumn, KeyResultMenuCopyAll,
	KeyResultMenuCopyColName, KeyResultMenuSortAsc, KeyResultMenuSortDesc, KeyResultMenuSortClear,
	KeyResultMenuFitColumn, KeyResultMenuFilterCell, KeyResultMenuClearFilter,
	KeyResultMenuPrevSet, KeyResultMenuNextSet,
	KeyResultMenuExport, KeyResultMenuClearResults,
	KeyExportTitle, KeyExportFile, KeyExportFormat, KeyExportCSV, KeyExportTSV,
	KeyExportJSON, KeyExportMarkdown, KeyExportAction, KeyExportHintShort, KeyExportHint, KeyExportPathRequired,
	KeyExportDirectory, KeyExportBrowse,
	KeyPopupHeader, KeyPopupHint,
	KeyMenuFile, KeyMenuEdit, KeyMenuView, KeyMenuQuery, KeyMenuSettings, KeyMenuHelp,
	KeyMenuSave, KeyMenuOpenFile, KeyMenuQuit, KeyMenuNewTab, KeyMenuCloseTab, KeyMenuNextTab, KeyMenuPrevTab,
	KeyMenuUndo, KeyMenuRedo, KeyMenuSelectAll, KeyMenuFind, KeyMenuHistory, KeyMenuCopy, KeyMenuCut, KeyMenuToggleComment,
	KeyMenuPaneObjects, KeyMenuPaneResults, KeyMenuClosePane, KeyMenuRun,
	KeyMenuFocusObjects, KeyMenuFocusEditor, KeyMenuFocusResults,
	KeyMenuExplain, KeyMenuClearResults, KeyMenuExport,
	KeyMenuPreferences, KeyMenuOpenConfigFile, KeyMenuKeymap, KeyMenuReadOnly,
	KeyMenuManualCommit, KeyMenuCommit, KeyMenuRollback,
	KeyKeysTitle, KeyKeysHint, KeyKeysObjects, KeyKeysResults, KeyKeysEditor,
	KeyKeysMove, KeyKeysFold, KeyKeysOpen, KeyKeysFilter, KeyKeysMenu,
	KeyKeysSort, KeyKeysCopyCell, KeyKeysCopyRow, KeyKeysResultSets, KeyKeysBack,
	KeyKeysComplete, KeyKeysAccept,
	KeyToolFormat,
	KeySettingsTitle, KeySettingsHintShort, KeySettingsHint, KeySettingsLanguage, KeySettingsTheme, KeySettingsMouse,
	KeySettingsGutter, KeySettingsIcons, KeySettingsResetLayout, KeySettingsFollowSystem,
	KeySettingsThemeDark, KeySettingsThemeLight,
	KeySettingsDescLanguage, KeySettingsDescTheme, KeySettingsDescIcons, KeySettingsDescMouse,
	KeySettingsDescGutter, KeySettingsDescLayout, KeySettingsReadOnly, KeySettingsDescReadOnly,
	KeySettingsBackupKept,
	KeyVaultTitle, KeyVaultSetupTitle, KeyVaultChangeTitle, KeyVaultRemoveTitle,
	KeyVaultManageTitle, KeyVaultHintTitle,
	KeyVaultPassphrase, KeyVaultConfirm, KeyVaultHint, KeyVaultCurrent,
	KeyVaultSet, KeyVaultUnlock, KeyVaultChange, KeyVaultRemove,
	KeyVaultFormHint, KeyVaultManageHint, KeyVaultHintLine, KeyVaultMinNote,
	KeyVaultHintWarning, KeyVaultNoRecovery, KeyVaultWeak, KeyVaultTooShort, KeyVaultMismatch, KeyVaultWrong,
	KeyVaultHintIsPassphrase, KeyVaultUnlocked, KeyVaultLocked, KeyVaultLockedConnLive,
	KeyVaultSetOK, KeyVaultChanged, KeyVaultRemoved, KeyVaultRemoveWarning,
	KeyVaultLockedTree, KeyVaultUnlockFirst, KeyVaultPlaintextWarning,
	KeyVaultMenu, KeyVaultLockNow, KeyVaultManageChange, KeyVaultManageRemove,
	KeyVaultManageChangeHint, KeyVaultNewHint, KeyVaultHintSet, KeyVaultUpgraded,
	KeyConfirmTitle, KeyConfirmSummary, KeyConfirmOneSummary, KeyConfirmWarning, KeyConfirmTooSmall, KeyConfirmMore, KeyConfirmRun,
	KeyConfirmHint, KeyRiskDDL, KeyRiskDangerousWrite, KeyRiskWrite,
	KeyMsgReadOnlyEnabled, KeyMsgReadOnlyDisabled, KeyMsgReadOnlyBlocked, KeyMsgOneReadOnlyBlocked,
	KeyMsgManualCommitOn, KeyMsgManualCommitOff, KeyMsgNoTransaction,
	KeyMsgTxCommitted, KeyMsgTxCommitFailed, KeyMsgTxRolledBack, KeyMsgTxRollbackFailed,
	KeyMsgTxSwitchBlocked, KeyMsgTxBusy, KeyMsgTxRolledBackOnSwitch,
	KeyMsgNoConnection, KeyMsgNothingToRun, KeyMsgNothingToFormat, KeyMsgQueryCancelled,
	KeyMsgCatalogFailed, KeyMsgCatalogLoaded, KeyMsgNotImplemented, KeyMsgFormatted,
	KeyMsgExported, KeyMsgOneRowExported, KeyMsgExportFailed, KeyMsgNoResultsToExport, KeyMsgNoFileToSave,
	KeyMsgSaveFailed, KeyMsgSaved, KeyMsgCopyFailed, KeyMsgCopied, KeyMsgNoSelectionCopy,
	KeyMsgNoSelectionCut, KeyMsgKeymap, KeyMsgAbout, KeyMsgSettingsSaved, KeyMsgSettingsPath, KeyMsgSettingsFailed,
	KeyMsgLayoutReset, KeyMsgLanguageChanged, KeyMsgMouseEnabled, KeyMsgMouseDisabled,
	KeyMsgGutterEnabled, KeyMsgGutterDisabled, KeyMsgOpenConfigFailed, KeyMsgNameCopied,
	KeyMsgDefinitionLoading, KeyMsgDefinitionOpened, KeyMsgDefinitionFailed, KeyMsgGenerateFailed,
	KeyMsgFileOpenFailed, KeyMsgFileTooLarge, KeyMsgFileNotUTF8, KeyMsgCopiedRows, KeyMsgOneRowCopied,
	KeyCLIDriver, KeyCLIDSN, KeyCLIVersion, KeyCLIDrivers, KeyCLISettings, KeyCLILang,
	KeyCLIPrintSettings, KeyCLIUsage, KeyCLINotImpl, KeyCLIConnFailed, KeyCLIDriversHead,
	KeyCLIDriverOK, KeyCLIDriverPending, KeyCLIDialectsHead, KeyCLIDialectLine, KeyCLIReadFailed,
	KeyCLIWarningMeta, KeyCLISettingsFailed, KeyCLIRunFailed,
}

// knownKeys is the set form of allKeys, used by Validate to reject stray keys.
var knownKeys = make(map[Key]bool, len(allKeys))

func init() {
	for _, k := range allKeys {
		knownKeys[k] = true
	}
}

// Keys returns every declared key, sorted by value.
func Keys() []Key {
	out := make([]Key, len(allKeys))
	copy(out, allKeys)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
