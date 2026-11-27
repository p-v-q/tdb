// Package driver defines the database driver abstraction: connections, query
// results and the driver registry.
//
// tdb is designed around "one editor, many databases". To that end this
// package defines capability interfaces only, without binding to any specific
// database:
//
//	dialect.Dialect   -- SQL dialect (keywords/functions/quoting rules)
//	catalog.Provider  -- metadata (schema/table/column); once a driver implements
//	                     it, intelligent suggestions follow automatically
//	driver.Connection -- connection (query execution will be wired into the
//	                     result grid in a later version)
//	driver.DefinitionProvider -- object DDL on demand (optional; drivers whose
//	                     database stores CREATE statements implement it)
//	driver.TransactionManager -- explicit transaction control (optional;
//	                     drivers whose database has transactions implement it,
//	                     which is what the UI's manual-commit mode builds on)
//
// To add a new database:
//  1. Implement dialect.Dialect (embed dialect.Base to reuse common logic)
//  2. Implement Connection, with Catalog() for introspection and Query() for
//     query execution
//  3. Call driver.Register(YourFactory{}) in init()
package driver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
)

// ErrNotImplemented indicates that the driver is not implemented yet.
var ErrNotImplemented = errors.New("this database driver is not implemented yet (tdb v1 ships only the framework and the demo connection)")

// Config is the configuration needed to establish a connection.
type Config struct {
	// Name is the driver name, e.g. "demo" or "postgres".
	Name string
	// DSN is the connection string, e.g. postgres://user:pass@host:5432/db.
	DSN string
	// Options holds driver-specific parameters.
	Options map[string]string
}

// ResultColumn describes a single column in a result set.
type ResultColumn struct {
	Name string
	Type string
}

// Result is the outcome of a query: a row set for statements that return rows,
// or just an affected-row count for the rest.
type Result struct {
	Columns []ResultColumn
	// Rows holds the result rows. Cells are *string and not string because a
	// nil cell is SQL NULL: a database client must not confuse NULL with the
	// empty string. Values are rendered as text, which is all a grid needs and
	// keeps driver-specific types out of the interface.
	Rows [][]*string
	// Affected is the number of rows changed by an INSERT/UPDATE/DELETE.
	Affected int64
	// Truncated reports that the row cap was hit and more rows were available.
	Truncated bool
	// Duration is how long the database spent on the statement.
	Duration time.Duration
}

// Connection is a database session.
// Connection also satisfies catalog.Provider, so it can be used directly as a
// metadata source for completion.
type Connection interface {
	// Name returns the connection name (shown in the status bar).
	Name() string
	// Dialect returns the SQL dialect for the connection.
	Dialect() dialect.Dialect
	// Catalog returns a metadata snapshot (may trigger introspection).
	Catalog(ctx context.Context) (*catalog.Catalog, error)
	// Query executes one statement and returns its rows or its affected-row
	// count. Implementations must honour ctx so a running statement can be
	// cancelled.
	Query(ctx context.Context, query string, args ...any) (*Result, error)
	// Close closes the connection.
	Close() error
}

// Factory creates connections from configuration.
type Factory interface {
	Name() string
	Open(ctx context.Context, cfg Config) (Connection, error)
}

// DefinitionRef identifies the object whose DDL a caller wants: a relation
// (table, view or materialized view) or a resource (function, procedure,
// trigger, index or sequence), named the way a catalog snapshot names it.
type DefinitionRef struct {
	// Kind is the object kind, using the catalog.ResourceKind vocabulary
	// (catalog.KindTable for tables, catalog.KindTrigger for triggers, …).
	Kind catalog.ResourceKind
	// Schema is the schema (SQLite database) the object lives in.
	Schema string
	// Name is the object's name, unqualified.
	Name string
	// Owner names the table a resource belongs to (a trigger's table, an
	// index's table). It disambiguates objects whose names are only unique
	// within their table; objects that are unique in their schema leave it
	// empty.
	Owner string
}

// DefinitionProvider is an optional interface for connections that can
// reproduce an object's CREATE statement on demand. It stays out of
// Connection so introspection snapshots remain cheap: a driver whose database
// stores DDL (SQLite, PostgreSQL, MySQL, …) implements it, and one that cannot
// simply does not. The UI discovers the capability with a type assertion and
// hides the action instead of failing per database.
type DefinitionProvider interface {
	// Definition returns the object's DDL as a re-executable statement. It
	// must honour ctx so a slow server cannot pin the caller.
	Definition(ctx context.Context, ref DefinitionRef) (string, error)
}

// ServerLister is an optional interface for connections whose server holds more
// namespaces than the connected one: the connection form lists the names the
// server holds instead of asking the user to type them from memory. It is
// implemented by the servers with a database catalog (PostgreSQL, MySQL and
// MariaDB); SQLite has a single file and does not implement it.
type ServerLister interface {
	// Databases lists the databases the server offers for connections. The
	// caller opens the connection against a maintenance database to ask, so
	// a server that refuses the named database can still list its catalog.
	// It must honour ctx so a slow server cannot pin the form.
	Databases(ctx context.Context) ([]string, error)
}

// SchemaLister is an optional interface for connections whose database has
// schemas a connection can name as its default (PostgreSQL). The list leaves
// out the server's own schemas: the form shows places a user's statements can
// work in.
type SchemaLister interface {
	// Schemas lists the schemas of the connected database. It must honour ctx
	// so a slow server cannot pin the form.
	Schemas(ctx context.Context) ([]string, error)
}

// TxAction classifies a statement that controls transactions, so a driver can
// route it to its TransactionManager instead of running it as an ordinary
// statement. A statement typed by the user is a transaction boundary whether or
// not the driver opened the transaction: running BEGIN through a connection
// pool releases the connection between statements, which rolls the transaction
// back and silently commits every later statement (the pre-release review's
// blocker). Recognising the keyword once, in the shared layer, keeps the three
// SQL drivers from each growing their own parser.
type TxAction int

// The transaction-control actions a user-typed statement can request.
const (
	// TxNone is any statement that is not transaction control.
	TxNone TxAction = iota
	// TxBegin starts a transaction (BEGIN, BEGIN WORK/TRANSACTION,
	// START TRANSACTION).
	TxBegin
	// TxCommit commits the open transaction (COMMIT, END).
	TxCommit
	// TxRollback discards the open transaction (ROLLBACK, ABORT). A rollback
	// to a savepoint is not a transaction rollback and is reported as TxNone.
	TxRollback
	// TxSavepoint creates a savepoint inside the open transaction.
	TxSavepoint
	// TxRelease releases a savepoint (RELEASE SAVEPOINT …).
	TxRelease
)

// String returns the action's name, for test failures and error messages.
func (a TxAction) String() string {
	switch a {
	case TxBegin:
		return "begin"
	case TxCommit:
		return "commit"
	case TxRollback:
		return "rollback"
	case TxSavepoint:
		return "savepoint"
	case TxRelease:
		return "release"
	default:
		return "none"
	}
}

// TransactionControl reports the transaction-control action of a statement.
//
// It is deliberately tolerant: leading whitespace and SQL comments (--, # and
// /* */, nested like PostgreSQL's) are skipped, case does not matter, and a
// trailing semicolon is allowed. Only the first keyword decides; a statement
// that merely starts with a keyword is not confused with it ("BEGINNING" is
// not BEGIN), and ROLLBACK TO [SAVEPOINT] is not reported as TxRollback
// because it keeps the transaction open.
//
// Options after the keyword (BEGIN ISOLATION LEVEL SERIALIZABLE, START
// TRANSACTION READ ONLY) are not interpreted here: the drivers start their
// transaction with the server's defaults, which is the same transaction shape
// the statements themselves ask for except for isolation-level overrides. The
// keyword prefix is still recognised, because letting those statements run
// unmanaged would silently commit the script.
func TransactionControl(statement string) TxAction {
	word, rest := firstSQLWord(statement)
	switch word {
	case "BEGIN":
		return TxBegin
	case "START":
		if next, _ := firstSQLWord(rest); next == "TRANSACTION" {
			return TxBegin
		}
	case "COMMIT", "END":
		return TxCommit
	case "ROLLBACK":
		// ROLLBACK TO [SAVEPOINT] name rolls the transaction back to a
		// savepoint and leaves it open: it must run on the transaction, not
		// end it.
		if next, _ := firstSQLWord(rest); next == "TO" {
			return TxNone
		}
		return TxRollback
	case "ABORT":
		return TxRollback
	case "SAVEPOINT":
		return TxSavepoint
	case "RELEASE":
		return TxRelease
	}
	return TxNone
}

// firstSQLWord skips leading whitespace and comments, then returns the first
// word of s uppercased, together with the text behind it. An empty word means
// the statement has no code (only whitespace/comments).
func firstSQLWord(s string) (word, rest string) {
	s = skipSQLSpaceAndComments(s)
	if s == "" {
		return "", ""
	}
	i := 0
	for i < len(s) && isSQLWordByte(s[i]) {
		i++
	}
	return strings.ToUpper(s[:i]), s[i:]
}

// isSQLWordByte reports whether c can be part of a SQL keyword/identifier word.
func isSQLWordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_', c == '$':
		return true
	}
	return false
}

// skipSQLSpaceAndComments removes leading whitespace and comments from s.
// Line comments (-- and #) run to the end of the line; block comments nest, as
// PostgreSQL allows. An unterminated comment hides the rest of the statement,
// so an empty string is returned rather than half a keyword.
func skipSQLSpaceAndComments(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n\f\v")
		switch {
		case s == "":
			return ""
		case strings.HasPrefix(s, "--"), strings.HasPrefix(s, "#"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			end, ok := skipBlockComment(s)
			if !ok {
				return ""
			}
			s = s[end:]
			continue
		}
		return s
	}
}

// skipBlockComment returns the index just behind the block comment at the head
// of s (which starts with "/*"). Nested comments are supported, matching
// PostgreSQL; an unterminated comment reports ok=false.
func skipBlockComment(s string) (end int, ok bool) {
	depth := 0
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(s[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i, true
			}
		default:
			i++
		}
	}
	return 0, false
}

// TransactionManager is an optional interface for connections that can run
// statements inside an explicit transaction — the safety net behind the UI's
// manual-commit mode (Commit / Rollback instead of per-statement commits).
//
// The contract:
//
//   - Begin starts a transaction if none is open (it is a no-op when one
//     already is, so a batch never has to special-case), and from then on
//     Query runs inside it until Commit or Rollback ends it.
//   - Commit persists the open transaction's changes, Rollback discards them;
//     both report an error when no transaction is open, so a mis-sequenced
//     UI action surfaces instead of pretending to have done something.
//   - InTx reports whether uncommitted work exists; the UI uses it to decide
//     when it must warn before dropping or closing the transaction.
//   - Implementations must be safe for concurrent use: statements run on a
//     background goroutine while the UI may trigger Commit/Rollback.
//
// ctx is carried by every method so drivers whose client library can bound a
// round trip (a lock wait, a network flush) can do so; drivers whose commit
// API takes no context (database/sql) may ignore it on Commit/Rollback.
//
// It stays out of Connection so a database without transactions (or a driver
// that does not model them, like the demo) needs no error paths: the UI
// discovers the capability with a type assertion, exactly as it does for
// DefinitionProvider, and hides the transaction controls when it is missing.
type TransactionManager interface {
	// Begin opens a transaction (or leaves the open one alone). It must
	// honour ctx so a lock wait cannot pin the caller.
	Begin(ctx context.Context) error
	// Commit ends the open transaction, persisting its changes.
	Commit(ctx context.Context) error
	// Rollback ends the open transaction, discarding its changes.
	Rollback(ctx context.Context) error
	// InTx reports whether a transaction is currently open.
	InTx() bool
}
