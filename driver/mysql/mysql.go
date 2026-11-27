// Package mysql implements driver.Connection for MySQL and MariaDB through
// database/sql and github.com/go-sql-driver/mysql (a pure Go driver, so no
// cgo is required). Both names register the same implementation: the wire
// protocol and information_schema are close enough that one driver serves
// both, while the connection keeps the name the user picked ("mysql" or
// "mariadb") in the UI and in error messages.
//
// The package follows the same shape as driver/postgres and driver/sqlite: a
// factory registered from init(), catalog introspection through
// information_schema, statements through a pooled connection, NULL kept apart
// from the empty string, a row cap with a truncation flag, and cancellation
// through context.
package mysql

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
)

// maxRows caps a result set so that `SELECT *` on a huge table cannot exhaust
// memory; a truncated result says so instead of silently lying.
const maxRows = 1000

// maxCellBytes caps how many bytes of a BLOB are rendered as hex; a longer
// value is cut with its total length noted, so the grid stays readable.
const maxCellBytes = 256

// The names this package registers in the driver registry. "mariadb" is the
// same driver under the name MariaDB users expect; it reuses the MySQL
// dialect because tdb ships no separate MariaDB dialect.
const (
	mysqlName   = "mysql"
	mariadbName = "mariadb"
	dialectName = "mysql"
)

func init() {
	driver.Register(factory{name: mysqlName})
	driver.Register(factory{name: mariadbName})
}

// factory opens MySQL / MariaDB connections.
type factory struct{ name string }

// Name implements driver.Factory.
func (f factory) Name() string { return f.name }

// Open implements driver.Factory. A DSN wins (either the driver's own format
// or a mysql:// URL); otherwise the usual host/port/user/password/database
// options are composed. sensible DSN defaults (parseTime, utf8mb4) are
// applied unless the DSN sets them itself.
func (f factory) Open(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
	mc, err := dataSource(cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.name, err)
	}
	db, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.name, err)
	}
	// A desktop client does not need a big pool, and a small one keeps a
	// server-side connection limit from being hit by several tabs.
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %s: %w", f.name, endpoint(mc), err)
	}
	return &conn{
		db:      db,
		name:    f.name,
		dbName:  displayName(mc, f.name),
		dialect: dialect.MustGet(dialectName),
	}, nil
}

// conn is an open MySQL / MariaDB database.
type conn struct {
	db      *sql.DB
	name    string // "mysql" or "mariadb", as the user connected
	dbName  string // database shown in the status bar
	dialect dialect.Dialect
	// closed is read on the query goroutine and written by Close on the UI
	// goroutine, so it is atomic rather than a plain bool.
	closed atomic.Bool

	// mu guards tx: statements run on a background goroutine while the UI
	// may trigger Commit or Rollback concurrently.
	mu sync.Mutex
	tx *sql.Tx
}

// The connection offers explicit transaction control on top of Connection.
var _ driver.TransactionManager = (*conn)(nil)

// Name implements driver.Connection: the database name, like the mysql client.
func (c *conn) Name() string { return c.dbName }

// Databases implements driver.ServerLister: the databases a user can connect
// to. MySQL and MariaDB keep their own catalog databases, and the connection
// form offers only the user's data: a form entry naming information_schema
// would be a session that only reads the server's metadata.
func (c *conn) Databases(ctx context.Context) ([]string, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT SCHEMA_NAME FROM information_schema.SCHEMATA
		  WHERE SCHEMA_NAME NOT IN ('information_schema','performance_schema','mysql','sys')
		  ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, fmt.Errorf("mysql: list databases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("mysql: list databases: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Dialect implements driver.Connection.
func (c *conn) Dialect() dialect.Dialect { return c.dialect }

// Catalog implements driver.Connection: it reads the user schemas (MySQL
// databases), their tables, views and columns, and the stored routines,
// triggers and indexes from information_schema.
func (c *conn) Catalog(ctx context.Context) (*catalog.Catalog, error) {
	if err := c.check(); err != nil {
		return nil, err
	}

	out := &catalog.Catalog{
		Dialect:  c.name,
		LoadedAt: time.Now(),
		// MySQL stores routines, triggers and indexes, and it has neither
		// materialized views nor sequences. MariaDB adds sequences: the group
		// stays empty (and is hidden) on MySQL, so the capability is claimed
		// for both — the driver speaks to both servers.
		Capabilities: catalog.Capable(
			catalog.KindTable, catalog.KindView,
			catalog.KindFunction, catalog.KindProcedure,
			catalog.KindTrigger, catalog.KindIndex, catalog.KindSequence,
		),
	}
	schemas := make(map[string]*catalog.Schema)

	names, err := c.schemaNames(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		s := &catalog.Schema{Name: name}
		schemas[name] = s
		out.Schemas = append(out.Schemas, s)
	}

	tables, err := c.loadTables(ctx, schemas, out)
	if err != nil {
		return nil, err
	}
	if err := c.loadColumns(ctx, tables); err != nil {
		return nil, err
	}
	if err := c.loadResources(ctx, schemas); err != nil {
		return nil, err
	}
	return out, nil
}

// Query implements driver.Connection: one statement at a time, honouring ctx
// so that a running statement can be cancelled.
func (c *conn) Query(ctx context.Context, query string, args ...any) (*driver.Result, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	// A context that is already cancelled or expired must not execute
	// anything: the pool would otherwise race the deadline and run the
	// statement anyway (a coarse monotonic clock can make a nanosecond
	// deadline look unexpired for a moment).
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("mysql: nothing to execute")
	}

	start := time.Now()
	// Transaction-control statements typed by the user must own the session
	// exactly like the UI's manual-commit mode does (see driver.TxAction):
	// running BEGIN on a pooled connection hands the connection (and the
	// transaction) back between statements, so a later statement autocommits.
	if res, handled, err := c.transactionControl(ctx, query); handled {
		if err != nil {
			return nil, err
		}
		res.Duration = time.Since(start)
		return res, nil
	}

	// The row cap must stop the server-side statement, not drain it:
	// database/sql's Rows.Close reads and discards the rest of the result, so
	// a capped `SELECT * FROM huge_table` would keep the UI waiting for the
	// whole result set. Cancelling the context closes the driver connection
	// under the running query instead.
	queryCtx, cancelQuery := context.WithCancel(ctx)
	defer cancelQuery()

	// A dedicated connection keeps the statement, its context and its
	// affected-row count together on one wire; the open transaction (manual
	// commit mode) takes over that role while one is open, so the whole batch
	// lands in it.
	var db execer
	if tx := c.currentTx(); tx != nil {
		db = tx
	} else {
		conn, err := c.db.Conn(queryCtx)
		if err != nil {
			return nil, fmt.Errorf("mysql: %w", err)
		}
		defer conn.Close()
		db = conn
	}

	// database/sql cannot report RowsAffected on the query path, and MySQL
	// cannot report columns on the exec path; the statement kind decides
	// which one to use (see returnsRows).
	if !returnsRows(query) {
		r, err := db.ExecContext(queryCtx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("mysql: %w", err)
		}
		affected, err := r.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("mysql: read affected rows: %w", err)
		}
		return &driver.Result{Affected: affected, Duration: time.Since(start)}, nil
	}

	rows, err := db.QueryContext(queryCtx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql: %w", err)
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("mysql: %w", err)
	}
	types, _ := rows.ColumnTypes()

	res := &driver.Result{
		Columns:  make([]driver.ResultColumn, len(names)),
		Duration: time.Since(start),
	}
	for i, name := range names {
		col := driver.ResultColumn{Name: name}
		if i < len(types) {
			col.Type = types[i].DatabaseTypeName()
		}
		res.Columns[i] = col
	}

	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			cancelQuery()
			break
		}
		row, err := scanRow(rows, types, len(names))
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil && !res.Truncated {
		// A cancellation caused by the row cap is expected: the rows we kept
		// are the answer. Any other error is a real failure.
		return nil, fmt.Errorf("mysql: %w", err)
	}
	res.Duration = time.Since(start)
	return res, nil
}

// transactionControl routes a transaction-control statement the user typed to
// the connection's transaction manager, so the transaction owns a session for
// as long as it is open. Begin is idempotent: a raw BEGIN inside a managed
// transaction must not reach MySQL/MariaDB, where it would implicitly commit the
// user's work. SAVEPOINT and RELEASE are not intercepted: inside a transaction
// they run on it, and outside one the server reports its normal error.
func (c *conn) transactionControl(ctx context.Context, query string) (*driver.Result, bool, error) {
	action := driver.TransactionControl(query)
	switch action {
	case driver.TxBegin:
		if err := c.Begin(ctx); err != nil {
			return nil, true, err
		}
		return &driver.Result{}, true, nil
	case driver.TxCommit, driver.TxRollback:
		if c.currentTx() == nil {
			// Outside a transaction MySQL accepts COMMIT and ROLLBACK and
			// returns a command tag without an error; there is nothing to
			// end, and the user's statement is not a failure.
			return &driver.Result{}, true, nil
		}
		var err error
		if action == driver.TxCommit {
			err = c.Commit(ctx)
		} else {
			err = c.Rollback(ctx)
		}
		if err != nil {
			return nil, true, err
		}
		return &driver.Result{}, true, nil
	}
	return nil, false, nil
}

// Close implements driver.Connection.
func (c *conn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.db.Close()
}

// check guards against using a closed connection.
func (c *conn) check() error {
	if c.closed.Load() {
		return errors.New("mysql: connection is closed")
	}
	return nil
}

// ---- Transactions ----

// execer is what statements run through: a dedicated pooled connection
// normally, the open transaction while one exists.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// currentTx returns the open transaction, if any.
func (c *conn) currentTx() *sql.Tx {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tx
}

// Begin implements driver.TransactionManager: it opens a transaction that
// every following Query joins, and is a no-op when one is already open so a
// batch never has to special-case.
func (c *conn) Begin(ctx context.Context) error {
	if err := c.check(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tx != nil {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mysql: begin transaction: %w", err)
	}
	c.tx = tx
	return nil
}

// Commit implements driver.TransactionManager. database/sql's Commit takes no
// context; ctx stays in the interface so every driver presents one shape.
func (c *conn) Commit(context.Context) error {
	c.mu.Lock()
	tx := c.tx
	c.tx = nil // the transaction is over even when the server refuses to commit
	c.mu.Unlock()
	if tx == nil {
		return errors.New("mysql: no transaction to commit")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mysql: commit: %w", err)
	}
	return nil
}

// Rollback implements driver.TransactionManager (no context, as in Commit).
func (c *conn) Rollback(context.Context) error {
	c.mu.Lock()
	tx := c.tx
	c.tx = nil
	c.mu.Unlock()
	if tx == nil {
		return errors.New("mysql: no transaction to roll back")
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("mysql: rollback: %w", err)
	}
	return nil
}

// InTx implements driver.TransactionManager.
func (c *conn) InTx() bool { return c.currentTx() != nil }

// ---- Introspection ----

// hiddenSchemas lists the schemas MySQL ships: they are implementation
// details of the server, not user data.
const hiddenSchemas = "'mysql', 'information_schema', 'performance_schema', 'sys'"

// schemaNames lists the user-visible schemas (a MySQL "database").
func (c *conn) schemaNames(ctx context.Context) ([]string, error) {
	const q = `
SELECT SCHEMA_NAME
FROM information_schema.SCHEMATA
WHERE SCHEMA_NAME NOT IN (` + hiddenSchemas + `)
ORDER BY SCHEMA_NAME`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("mysql: list schemas: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("mysql: list schemas: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// tableID identifies a table while the catalog is being assembled.
type tableID struct {
	schema string
	table  string
}

// loadTables reads tables and views into their schemas, and returns them by id
// for the column pass. MySQL has no materialized views: TABLE_TYPE is either
// BASE TABLE or VIEW.
func (c *conn) loadTables(ctx context.Context, schemas map[string]*catalog.Schema, out *catalog.Catalog) (map[tableID]*catalog.Table, error) {
	const q = `
SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE, COALESCE(TABLE_COMMENT, '')
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN (` + hiddenSchemas + `)
  AND TABLE_TYPE IN ('BASE TABLE', 'VIEW', 'SYSTEM VERSIONED')
ORDER BY TABLE_SCHEMA, TABLE_NAME`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("mysql: list tables: %w", err)
	}
	defer rows.Close()

	tables := make(map[tableID]*catalog.Table)
	for rows.Next() {
		var (
			id      tableID
			typ     string
			comment string
		)
		if err := rows.Scan(&id.schema, &id.table, &typ, &comment); err != nil {
			return nil, fmt.Errorf("mysql: list tables: %w", err)
		}
		s := schemas[id.schema]
		if s == nil {
			// Defensive: a schema created between the two queries.
			s = &catalog.Schema{Name: id.schema}
			schemas[id.schema] = s
			out.Schemas = append(out.Schemas, s)
		}
		t := &catalog.Table{
			Schema:  id.schema,
			Name:    id.table,
			Type:    catalog.TypeTable,
			Comment: comment,
		}
		if typ == "VIEW" {
			t.Type = catalog.TypeView
		}
		s.Tables = append(s.Tables, t)
		tables[id] = t
	}
	return tables, rows.Err()
}

// loadColumns fills in the column list of every table: type, nullability,
// primary key, uniqueness, default, comment and ordinal position. COLUMN_KEY
// condenses the index information: PRI for a primary-key column, UNI for a
// column covered by a single-column unique index.
func (c *conn) loadColumns(ctx context.Context, tables map[tableID]*catalog.Table) error {
	const q = `
SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE,
       COLUMN_KEY, COLUMN_DEFAULT, COALESCE(COLUMN_COMMENT, ''), ORDINAL_POSITION
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA NOT IN (` + hiddenSchemas + `)
ORDER BY TABLE_SCHEMA, TABLE_NAME, ORDINAL_POSITION`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("mysql: list columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id       tableID
			name     string
			typ      string
			nullable string
			key      string
			def      sql.NullString
			comment  string
			ordinal  int
		)
		if err := rows.Scan(&id.schema, &id.table, &name, &typ, &nullable,
			&key, &def, &comment, &ordinal); err != nil {
			return fmt.Errorf("mysql: list columns: %w", err)
		}
		t := tables[id]
		if t == nil {
			continue
		}
		t.Columns = append(t.Columns, &catalog.Column{
			Name:     name,
			Type:     typ,
			Nullable: nullable == "YES",
			PK:       key == "PRI",
			Unique:   key == "UNI",
			Default:  def.String,
			Comment:  comment,
			Ordinal:  ordinal,
		})
	}
	return rows.Err()
}

// loadResources reads the non-relation objects MySQL exposes in
// information_schema. They are split into small queries instead of one UNION
// so each resource shape stays readable and a future resource only adds one
// function.
func (c *conn) loadResources(ctx context.Context, schemas map[string]*catalog.Schema) error {
	if err := c.loadRoutines(ctx, schemas); err != nil {
		return err
	}
	if err := c.loadTriggers(ctx, schemas); err != nil {
		return err
	}
	if err := c.loadIndexes(ctx, schemas); err != nil {
		return err
	}
	return c.loadSequences(ctx, schemas)
}

// loadSequences lists MariaDB SEQUENCE objects. MySQL has none; on MariaDB a
// sequence is a table-shaped object in information_schema.TABLES with
// TABLE_TYPE 'SEQUENCE', and the tree shows it in its own Resources group
// instead of pretending the user can select rows from it.
func (c *conn) loadSequences(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT TABLE_SCHEMA, TABLE_NAME, COALESCE(TABLE_COMMENT, '')
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN (` + hiddenSchemas + `)
  AND TABLE_TYPE = 'SEQUENCE'
ORDER BY TABLE_SCHEMA, TABLE_NAME`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("mysql: list sequences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, name, comment string
		if err := rows.Scan(&schema, &name, &comment); err != nil {
			return fmt.Errorf("mysql: list sequences: %w", err)
		}
		s := schemas[schema]
		if s == nil {
			continue
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:    catalog.KindSequence,
			Schema:  schema,
			Name:    name,
			Comment: comment,
		})
	}
	return rows.Err()
}

// loadRoutines reads stored functions and procedures.
//
// DTD_IDENTIFIER is NULL for every routine that does not return a table
// structure — every procedure, and every function with a scalar result — so it
// is scanned into a nullable type. Scanning it into a plain string made the
// whole catalog fail ("converting NULL to string is unsupported") and left the
// object browser empty; the same goes for the language and the comment, which
// the server may answer with NULL on other versions.
func (c *conn) loadRoutines(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT ROUTINE_SCHEMA, ROUTINE_NAME, ROUTINE_TYPE,
       DTD_IDENTIFIER, COALESCE(EXTERNAL_LANGUAGE, ROUTINE_BODY, ''),
       COALESCE(ROUTINE_COMMENT, '')
FROM information_schema.ROUTINES
WHERE ROUTINE_SCHEMA NOT IN (` + hiddenSchemas + `)
ORDER BY ROUTINE_SCHEMA, ROUTINE_NAME`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("mysql: list routines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			schema, name, typ string
			returnType        sql.NullString
			language          sql.NullString
			comment           sql.NullString
		)
		if err := rows.Scan(&schema, &name, &typ, &returnType, &language, &comment); err != nil {
			return fmt.Errorf("mysql: list routines: %w", err)
		}
		s := schemas[schema]
		if s == nil {
			continue
		}
		resourceKind := catalog.KindFunction
		if typ == "PROCEDURE" {
			resourceKind = catalog.KindProcedure
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:    resourceKind,
			Schema:  schema,
			Name:    name,
			Detail:  routineDetail(returnType.String, language.String),
			Comment: comment.String,
		})
	}
	return rows.Err()
}

// routineDetail builds the compact "→ result · language" description.
// information_schema.PARAMETERS is unreliable across MySQL and MariaDB
// versions, so the argument list is deliberately left out.
func routineDetail(returnType, language string) string {
	var parts []string
	if returnType != "" {
		parts = append(parts, "→ "+returnType)
	}
	if language != "" {
		parts = append(parts, language)
	}
	return strings.Join(parts, " · ")
}

// triggerRow is one row of information_schema.TRIGGERS: a trigger fires one
// timing for one event, and MariaDB allows the same trigger name for several
// events on one table, so the rows are merged below.
type triggerRow struct {
	schema, name, table string
	timing, event       string
}

// loadTriggers reads user-defined triggers and merges the per-event rows into
// one resource per trigger name.
func (c *conn) loadTriggers(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT TRIGGER_SCHEMA, TRIGGER_NAME, EVENT_OBJECT_TABLE, ACTION_TIMING, EVENT_MANIPULATION
FROM information_schema.TRIGGERS
WHERE TRIGGER_SCHEMA NOT IN (` + hiddenSchemas + `)
ORDER BY TRIGGER_SCHEMA, TRIGGER_NAME, EVENT_MANIPULATION`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("mysql: list triggers: %w", err)
	}
	defer rows.Close()

	var seen []triggerRow
	for rows.Next() {
		var r triggerRow
		if err := rows.Scan(&r.schema, &r.name, &r.table, &r.timing, &r.event); err != nil {
			return fmt.Errorf("mysql: list triggers: %w", err)
		}
		seen = append(seen, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range mergeTriggers(seen) {
		s := schemas[r.schema]
		if s == nil {
			continue
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:   catalog.KindTrigger,
			Schema: r.schema,
			Name:   r.name,
			Owner:  r.table,
			Detail: r.detail(),
		})
	}
	return nil
}

// triggerEntry is a trigger after its per-event rows were merged: one timing
// (the first row wins; it is the same for every event) and the events in the
// order they arrived.
type triggerEntry struct {
	schema, name, table string
	timing              string
	events              []string
}

// detail renders the compact "BEFORE INSERT OR UPDATE" label.
func (e triggerEntry) detail() string {
	parts := make([]string, 0, 2)
	if e.timing != "" {
		parts = append(parts, e.timing)
	}
	if len(e.events) > 0 {
		parts = append(parts, strings.Join(e.events, " OR "))
	}
	return strings.Join(parts, " ")
}

// mergeTriggers turns the per-event rows into one entry per trigger.
func mergeTriggers(rows []triggerRow) []triggerEntry {
	var out []triggerEntry
	index := make(map[string]int)
	for _, r := range rows {
		key := r.schema + "\x00" + r.name + "\x00" + r.table
		if i, ok := index[key]; ok {
			out[i].addEvent(r.event)
			continue
		}
		index[key] = len(out)
		e := triggerEntry{schema: r.schema, name: r.name, table: r.table, timing: r.timing}
		e.addEvent(r.event)
		out = append(out, e)
	}
	return out
}

// addEvent appends an event unless the trigger already has it (the same event
// must not appear twice in the label).
func (e *triggerEntry) addEvent(event string) {
	if event == "" {
		return
	}
	for _, seen := range e.events {
		if seen == event {
			return
		}
	}
	e.events = append(e.events, event)
}

// indexRow is one row of information_schema.STATISTICS: an index spans one or
// more columns, one row each.
type indexRow struct {
	schema, table, name string
	nonUnique           bool
	column              string
	indexType           string
}

// functionalIndexColumn is what an index part that indexes an expression is
// called in the tree. A functional (or multi-valued) index has no column name:
// MySQL reports COLUMN_NAME as NULL and keeps the expression in EXPRESSION, and
// MySQL 5.x/MariaDB do not have the EXPRESSION column at all. The expression is
// not needed to list the index, but the row must never fail the whole catalog.
const functionalIndexColumn = "expression"

// loadIndexes reads indexes on user tables and merges the per-column rows into
// one resource per index.
func (c *conn) loadIndexes(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT TABLE_SCHEMA, TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME, INDEX_TYPE
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA NOT IN (` + hiddenSchemas + `)
ORDER BY TABLE_SCHEMA, TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("mysql: list indexes: %w", err)
	}
	defer rows.Close()

	var seen []indexRow
	for rows.Next() {
		var (
			r         indexRow
			nonUnique int
			column    sql.NullString
		)
		if err := rows.Scan(&r.schema, &r.table, &r.name, &nonUnique, &column, &r.indexType); err != nil {
			return fmt.Errorf("mysql: list indexes: %w", err)
		}
		r.nonUnique = nonUnique == 0
		// A functional or multi-valued index part has no column name; the
		// index still belongs in the tree, so the part gets a readable name
		// instead of failing the scan and, with it, the whole catalog.
		if column.Valid && column.String != "" {
			r.column = column.String
		} else {
			r.column = functionalIndexColumn
		}
		seen = append(seen, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range mergeIndexes(seen) {
		s := schemas[r.schema]
		if s == nil {
			continue
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:   catalog.KindIndex,
			Schema: r.schema,
			Name:   r.name,
			Owner:  r.table,
			Detail: r.detail,
		})
	}
	return nil
}

// indexEntry is an index after its per-column rows were merged.
type indexEntry struct {
	schema, table, name string
	detail              string
}

// mergeIndexes turns the per-column rows into one entry per index, with the
// uniqueness and the indexed columns as detail.
func mergeIndexes(rows []indexRow) []indexEntry {
	var out []indexEntry
	index := make(map[string]int)
	for _, r := range rows {
		key := r.schema + "\x00" + r.table + "\x00" + r.name
		if i, ok := index[key]; ok {
			out[i].addColumn(r.column)
			continue
		}
		index[key] = len(out)
		e := indexEntry{schema: r.schema, table: r.table, name: r.name}
		e.detail = indexDetail(r.name, r.nonUnique, r.indexType)
		e.addColumn(r.column)
		out = append(out, e)
	}
	return out
}

// indexDetail builds the compact "primary key · BTREE" style description.
func indexDetail(name string, nonUnique bool, indexType string) string {
	var head string
	switch {
	case name == "PRIMARY":
		head = "primary key"
	case !nonUnique:
		head = "unique"
	}
	if indexType != "" && indexType != "BTREE" {
		if head != "" {
			head += " · " + indexType
		} else {
			head = indexType
		}
	}
	return head
}

// addColumn appends an indexed column to the entry's detail. Rows arrive in
// SEQ_IN_INDEX order, so appending keeps the index's own column order.
func (e *indexEntry) addColumn(col string) {
	if col == "" {
		return
	}
	switch {
	case e.detail == "":
		e.detail = "(" + col + ")"
	case strings.HasSuffix(e.detail, ")"):
		e.detail = e.detail[:len(e.detail)-1] + ", " + col + ")"
	default:
		e.detail += " · (" + col + ")"
	}
}

// ---- Object definitions ----

// Definition implements driver.DefinitionProvider: MySQL and MariaDB print
// the CREATE statement of tables, views, routines and triggers with SHOW
// CREATE …. Indexes are the exception — both servers only show them inside
// SHOW CREATE TABLE — so index DDL is assembled from information_schema.
// Identifiers cannot be parameterized; they are quoted as backtick
// identifiers (quoteIdent) and never interpolated raw.
func (c *conn) Definition(ctx context.Context, ref driver.DefinitionRef) (string, error) {
	if err := c.check(); err != nil {
		return "", err
	}
	schema := ref.Schema
	if schema == "" {
		schema = c.dbName // the connection's own database, like the client default
	}
	qual := quoteIdent(schema) + "." + quoteIdent(ref.Name)
	kindWord := ref.Kind.String()

	var query string
	switch ref.Kind {
	case catalog.KindTable:
		query = "SHOW CREATE TABLE " + qual
	case catalog.KindView:
		query = "SHOW CREATE VIEW " + qual
	case catalog.KindFunction:
		query = "SHOW CREATE FUNCTION " + qual
	case catalog.KindProcedure:
		query = "SHOW CREATE PROCEDURE " + qual
	case catalog.KindTrigger:
		query = "SHOW CREATE TRIGGER " + qual
	case catalog.KindSequence:
		// MariaDB keeps sequences as first-class objects (MySQL has none);
		// SHOW CREATE SEQUENCE prints the full CREATE SEQUENCE statement.
		query = "SHOW CREATE SEQUENCE " + qual
	case catalog.KindIndex:
		return c.indexDefinition(ctx, schema, ref.Name, ref.Owner)
	default:
		return "", fmt.Errorf("%s: no stored definitions for %s", c.name, kindWord)
	}

	ddl, err := c.showCreate(ctx, query)
	if errors.Is(err, sql.ErrNoRows) || isMissingObject(err) {
		// SHOW CREATE reports a missing object as a server error (1146 for
		// tables and views, 1305 for routines and triggers), never as an
		// empty result; the sql.ErrNoRows branch stays as a defensive
		// fallback. The message is the same "not found" the PostgreSQL and
		// SQLite drivers produce.
		return "", fmt.Errorf("%s: %s %s.%s not found", c.name, kindWord, schema, ref.Name)
	}
	if err != nil {
		return "", fmt.Errorf("%s: definition of %s %s.%s: %w", c.name, kindWord, schema, ref.Name, err)
	}
	// SHOW CREATE prints the environment the object was created in — the
	// account that owns it, the algorithm of a view — next to the object
	// itself. Those clauses belong to the server, not to the object, and keep
	// the statement from running somewhere else.
	return stripEnvironmentClauses(ddl), nil
}

// stripEnvironmentClauses removes the clauses MySQL and MariaDB print in front
// of a stored object's name: ALGORITHM=…, DEFINER=… and SQL SECURITY DEFINER.
//
// SHOW CREATE reports the environment the object lives in, not only the object
// itself: a DEFINER names the account that owns the object, and a statement
// that carries someone else's DEFINER fails on the next server ("Access denied
// to user … for routine …"). The stripped statement is the one a user can copy
// into a database of their own.
//
// The clauses stand between CREATE and the object keyword, so they are cut
// from that prefix; the rest of the statement — the body of a view, the body of
// a routine — is left untouched. Each clause goes away together with the
// whitespace behind it, so exactly one space is left between CREATE and the
// object keyword.
//
// SQL SECURITY INVOKER is kept: it is not environment-specific (it names no
// account) and it is not the default, so dropping it would silently turn an
// invoker routine into a definer one.
func stripEnvironmentClauses(ddl string) string {
	const create = "CREATE "
	lead := ddl[:len(ddl)-len(strings.TrimLeft(ddl, " \t\r\n"))]
	body := strings.TrimLeft(ddl, " \t\r\n")
	if !hasPrefixFold(body, create) {
		return ddl
	}
	rest := body[len(create):]
	stripped := false
	for {
		_, tail, ok := cutEnvironmentClause(rest)
		if !ok {
			break
		}
		rest = strings.TrimLeft(tail, " \t\r\n")
		stripped = true
	}
	if !stripped {
		return ddl
	}
	return lead + create + rest
}

// cutEnvironmentClause cuts one environment-specific clause from the head of
// s: ALGORITHM=…, DEFINER=… or SQL SECURITY DEFINER. It returns the clause,
// the text behind it (the whitespace that separated it from the next word
// included) and whether such a clause was found at all.
func cutEnvironmentClause(s string) (clause, rest string, ok bool) {
	switch {
	case hasPrefixFold(s, "ALGORITHM="):
		value, tail, ok := cutWord(s[len("ALGORITHM="):])
		if !ok {
			return "", s, false
		}
		return s[:len("ALGORITHM=")+len(value)], tail, true
	case hasPrefixFold(s, "DEFINER="):
		return cutDefiner(s[len("DEFINER="):])
	case hasPrefixFold(s, "SQL SECURITY DEFINER"):
		return s[:len("SQL SECURITY DEFINER")], s[len("SQL SECURITY DEFINER"):], true
	}
	return "", s, false
}

// cutDefiner cuts the account of a DEFINER clause: the user and the host it
// was created for, written as `user`@`host` by MySQL and as user@host by
// MariaDB. s starts behind the "DEFINER=" of the clause.
func cutDefiner(s string) (clause, rest string, ok bool) {
	user, tail, ok := cutAccountPart(s, '@')
	if !ok || !strings.HasPrefix(tail, "@") {
		return "", s, false
	}
	host, tail, ok := cutAccountPart(tail[1:], 0)
	if !ok {
		return "", s, false
	}
	return s[:len(user)+1+len(host)], tail, true
}

// cutAccountPart cuts one part of an account name — the user or the host of a
// DEFINER clause. The part is quoted (with backticks, single or double
// quotes) or bare; a quoted part may contain its quote character, doubled. A
// bare part ends at the stop character (unless it is zero) or at the next
// whitespace.
func cutAccountPart(s string, stop byte) (part, rest string, ok bool) {
	if s == "" {
		return "", s, false
	}
	switch s[0] {
	case '`', '\'', '"':
		quote := s[0]
		for i := 1; i < len(s); i++ {
			if s[i] != quote {
				continue
			}
			if quote != '`' && i+1 < len(s) && s[i+1] == quote {
				i++ // a doubled quote escapes itself
				continue
			}
			return s[:i+1], s[i+1:], true
		}
		return "", s, false // unterminated quote
	}
	for i := 0; i < len(s); i++ {
		if stop != 0 && s[i] == stop {
			return s[:i], s[i:], true
		}
		if isSpace(s[i]) {
			return s[:i], s[i:], true
		}
	}
	return s, "", true
}

// isSpace reports whether c separates two words.
func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n':
		return true
	}
	return false
}

// cutWord cuts the whitespace-delimited word at the head of s. The rest keeps
// the whitespace that followed the word.
func cutWord(s string) (word, rest string, ok bool) {
	if s == "" {
		return "", s, false
	}
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		return s[:i], s[i:], true
	}
	return s, "", true
}

// hasPrefixFold reports whether s starts with prefix, ignoring case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// isMissingObject reports whether err is the server's "object does not exist"
// answer to SHOW CREATE …: 1146 (table doesn't exist) and 1051 (unknown
// table) for tables and views, 1305 (routine does not exist) for functions,
// procedures and triggers. It is pure so every server error code can be
// pinned in tests without a live server.
func isMissingObject(err error) bool {
	var me *gomysql.MySQLError
	if !errors.As(err, &me) {
		return false
	}
	switch me.Number {
	case 1051, 1146, 1305:
		return true
	}
	return false
}

// showCreate runs a SHOW CREATE … statement and returns the CREATE statement
// from its result row. Only tables and views put the statement in the second
// column; routines insert a sql_mode column before it (Function, sql_mode,
// Create Function, …) and triggers do too (Trigger, sql_mode, SQL Original
// Statement, … — MariaDB adds a normalized Create Trigger), so the column is
// located by name — findCreateColumn — instead of by position.
func (c *conn) showCreate(ctx context.Context, query string) (string, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", sql.ErrNoRows
	}
	names, err := rows.Columns()
	if err != nil {
		return "", err
	}
	vals := make([]sql.NullString, len(names))
	targets := make([]any, len(names))
	for i := range vals {
		targets[i] = &vals[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return "", err
	}
	idx, ok := findCreateColumn(names, vals)
	if !ok {
		return "", errors.New("the server returned no CREATE statement")
	}
	return vals[idx].String, nil
}

// showCreateDDLColumns lists, in priority order, the column names MySQL and
// MariaDB use for the CREATE statement of a SHOW CREATE … row. The order
// also decides which column wins when a server offers several: MariaDB's
// SHOW CREATE TRIGGER prints both the statement as written ("SQL Original
// Statement") and the normalized "Create Trigger", and the normalized one is
// the better definition.
var showCreateDDLColumns = []string{
	"Create Table",
	"Create View",
	"Create Function",
	"Create Procedure",
	"Create Trigger",
	"Create Sequence",
	"SQL Original Statement",
}

// findCreateColumn locates the DDL column of one SHOW CREATE … row: the
// first known CREATE column (matched case-insensitively) that carries a
// value, or — when a server names its columns differently — the first column
// whose value looks like a CREATE statement. names and vals are the parallel
// column names and scanned values of one row.
func findCreateColumn(names []string, vals []sql.NullString) (int, bool) {
	for _, want := range showCreateDDLColumns {
		for i, name := range names {
			if i >= len(vals) {
				break
			}
			if strings.EqualFold(name, want) &&
				vals[i].Valid && strings.TrimSpace(vals[i].String) != "" {
				return i, true
			}
		}
	}
	for i, v := range vals {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(v.String)), "CREATE ") {
			return i, true
		}
	}
	return 0, false
}

// statsColumns records which optional information_schema.STATISTICS columns the
// connected server has. MySQL 8 has EXPRESSION, COLLATION and IS_VISIBLE;
// MariaDB has COLLATION and IGNORED but no EXPRESSION; older servers have
// neither. Asking information_schema.COLUMNS once keeps one query portable
// across the servers the driver supports.
type statsColumns struct {
	expression     bool
	collation      bool
	visibilityName string // "IS_VISIBLE" or "IGNORED"; empty when neither exists
}

// statisticsColumns reads the column list of information_schema.STATISTICS.
func (c *conn) statisticsColumns(ctx context.Context) (statsColumns, error) {
	const q = `SELECT COLUMN_NAME FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'STATISTICS'`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return statsColumns{}, fmt.Errorf("%s: inspect information_schema.STATISTICS: %w", c.name, err)
	}
	defer rows.Close()
	have := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return statsColumns{}, fmt.Errorf("%s: inspect information_schema.STATISTICS: %w", c.name, err)
		}
		have[strings.ToUpper(name)] = true
	}
	if err := rows.Err(); err != nil {
		return statsColumns{}, fmt.Errorf("%s: inspect information_schema.STATISTICS: %w", c.name, err)
	}
	out := statsColumns{
		expression: have["EXPRESSION"],
		collation:  have["COLLATION"],
	}
	switch {
	case have["IS_VISIBLE"]:
		out.visibilityName = "IS_VISIBLE"
	case have["IGNORED"]:
		out.visibilityName = "IGNORED"
	}
	return out, nil
}

// indexDefinition assembles the CREATE INDEX statement for one index from
// information_schema.STATISTICS: an index spans one row per column, and the
// rows are merged here the way the catalog pass merges them into one resource.
//
// Functional and multi-valued index parts have no column name (COLUMN_NAME is
// NULL and the expression lives in EXPRESSION on MySQL 8), so both are scanned
// as nullable and rendered as "(expression)" instead of failing the query.
// Descending parts (COLLATION = 'D') and an invisible index (IS_VISIBLE = 'NO'
// on MySQL, IGNORED = 'YES' on MariaDB) are carried into the statement so a
// re-created index behaves like the original.
func (c *conn) indexDefinition(ctx context.Context, schema, name, table string) (string, error) {
	extra, err := c.statisticsColumns(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: definition of index %s.%s: %w", c.name, schema, name, err)
	}
	selectCols := []string{"TABLE_NAME", "NON_UNIQUE", "COLUMN_NAME", "SUB_PART", "INDEX_TYPE"}
	if extra.expression {
		selectCols = append(selectCols, "EXPRESSION")
	}
	if extra.collation {
		selectCols = append(selectCols, "COLLATION")
	}
	if extra.visibilityName != "" {
		selectCols = append(selectCols, extra.visibilityName)
	}
	q := `SELECT ` + strings.Join(selectCols, ", ") + `
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND INDEX_NAME = ?
  AND (? = '' OR TABLE_NAME = ?)
ORDER BY TABLE_NAME, SEQ_IN_INDEX`
	rows, err := c.db.QueryContext(ctx, q, schema, name, table, table)
	if err != nil {
		return "", fmt.Errorf("%s: definition of index %s.%s: %w", c.name, schema, name, err)
	}
	defer rows.Close()

	var (
		owner      string
		unique     bool
		typ        string
		visibility string
		cols       []indexColumn
		seenCols   = map[string]bool{}
	)
	for rows.Next() {
		var (
			tbl                                string
			nonUnique                          int
			column, expression, collation, vis sql.NullString
			subPart                            sql.NullInt64
			indexType                          string
		)
		targets := []any{&tbl, &nonUnique, &column, &subPart, &indexType}
		if extra.expression {
			targets = append(targets, &expression)
		}
		if extra.collation {
			targets = append(targets, &collation)
		}
		if extra.visibilityName != "" {
			targets = append(targets, &vis)
		}
		if err := rows.Scan(targets...); err != nil {
			return "", fmt.Errorf("%s: definition of index %s.%s: %w", c.name, schema, name, err)
		}
		if owner == "" {
			owner, unique, typ = tbl, nonUnique == 0, indexType
			visibility = indexVisibility(extra.visibilityName, vis)
		}
		part := indexColumn{subPart: subPart}
		switch {
		case column.Valid && column.String != "":
			part.name = column.String
		case expression.Valid && expression.String != "":
			part.expression = expression.String
		default:
			// A server that reports neither leaves only the placeholder.
			part.expression = functionalIndexColumn
		}
		part.descending = collation.Valid && strings.EqualFold(strings.TrimSpace(collation.String), "D")
		// A composite index lists each part once, even when STATISTICS
		// repeats rows for partial index statistics.
		key := "col:" + part.name
		if part.expression != "" {
			key = "expr:" + part.expression
		}
		if !seenCols[key] {
			seenCols[key] = true
			cols = append(cols, part)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("%s: definition of index %s.%s: %w", c.name, schema, name, err)
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("%s: index %s.%s not found", c.name, schema, name)
	}
	return indexDDL(schema, owner, name, unique, typ, visibility, cols), nil
}

// indexVisibility turns the server's visibility column into the keyword to
// print: MySQL spells an invisible index IS_VISIBLE='NO' and prints INVISIBLE,
// MariaDB spells it IGNORED='YES' and prints IGNORED.
func indexVisibility(column string, value sql.NullString) string {
	if column == "" || !value.Valid {
		return ""
	}
	switch column {
	case "IS_VISIBLE":
		if strings.EqualFold(strings.TrimSpace(value.String), "NO") {
			return "INVISIBLE"
		}
	case "IGNORED":
		if strings.EqualFold(strings.TrimSpace(value.String), "YES") {
			return "IGNORED"
		}
	}
	return ""
}

// indexColumn is one part of an assembled index definition. Exactly one of name
// and expression is set: a functional index part has an expression, a column
// part has a name and possibly a prefix length. descending is MySQL 8's
// per-part DESC.
type indexColumn struct {
	name       string
	expression string
	subPart    sql.NullInt64
	descending bool
}

// indexDDL renders the statement that recreates one index. FULLTEXT and SPATIAL
// belong in the CREATE keyword position (MySQL rejects "USING FULLTEXT"); the
// PRIMARY index cannot be created with CREATE INDEX and becomes the equivalent
// ALTER TABLE … ADD PRIMARY KEY.
func indexDDL(schema, table, name string, unique bool, indexType, visibility string, cols []indexColumn) string {
	parts := make([]string, len(cols))
	upperType := strings.ToUpper(strings.TrimSpace(indexType))
	// FULLTEXT and SPATIAL index parts take no prefix length: the server
	// reports a geometry prefix for a spatial index, but printing it would
	// produce a statement MySQL rejects.
	withSubPart := upperType != "FULLTEXT" && upperType != "SPATIAL"
	for i, col := range cols {
		var part string
		switch {
		case col.expression != "":
			part = expressionPart(col.expression)
		case col.name != "":
			part = quoteIdent(col.name)
			if withSubPart && col.subPart.Valid {
				part += fmt.Sprintf("(%d)", col.subPart.Int64)
			}
		default:
			part = quoteIdent(functionalIndexColumn)
		}
		if col.descending {
			part += " DESC"
		}
		parts[i] = part
	}
	colList := "(" + strings.Join(parts, ", ") + ")"
	if strings.EqualFold(name, "PRIMARY") {
		return fmt.Sprintf("ALTER TABLE %s.%s ADD PRIMARY KEY %s",
			quoteIdent(schema), quoteIdent(table), colList)
	}
	kind := ""
	switch upperType {
	case "FULLTEXT", "SPATIAL":
		kind = upperType + " "
	default:
		if unique {
			kind = "UNIQUE "
		}
	}
	ddl := fmt.Sprintf("CREATE %sINDEX %s ON %s.%s %s",
		kind, quoteIdent(name), quoteIdent(schema), quoteIdent(table), colList)
	switch upperType {
	case "", "BTREE", "FULLTEXT", "SPATIAL":
		// FULLTEXT/SPATIAL are part of the CREATE keyword; BTREE is the
		// default and adds nothing.
	default:
		ddl += " USING " + upperType
	}
	if visibility != "" {
		ddl += " " + visibility
	}
	return ddl
}

// expressionPart renders one functional index part. MySQL's EXPRESSION column
// already carries the parentheses around the expression ("(`a` + 1)"); one pair
// is added only when the server printed the bare expression.
func expressionPart(expression string) string {
	e := strings.TrimSpace(expression)
	if strings.HasPrefix(e, "(") && strings.HasSuffix(e, ")") {
		return e
	}
	return "(" + e + ")"
}

// ---- Values and configuration ----

// scanRow reads one row, keeping NULLs apart from empty strings and rendering
// every value as text.
func scanRow(rows *sql.Rows, types []*sql.ColumnType, n int) ([]*string, error) {
	raw := make([]any, n)
	targets := make([]any, n)
	for i := range raw {
		targets[i] = &raw[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, fmt.Errorf("mysql: read row: %w", err)
	}
	row := make([]*string, n)
	for i, v := range raw {
		if v == nil {
			continue // SQL NULL
		}
		typ := ""
		if i < len(types) {
			typ = types[i].DatabaseTypeName()
		}
		s := formatValue(typ, v)
		row[i] = &s
	}
	return row, nil
}

// formatValue renders a driver value the way the server would print it in text
// form, which is what a result grid wants. Binary column types become hex so
// that raw bytes never land in the grid.
func formatValue(dbType string, v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		if isBinaryType(dbType) {
			return binaryText(t)
		}
		return string(t)
	case time.Time:
		return formatTime(dbType, t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

// isBinaryType reports whether a column type carries raw bytes (as opposed to
// text that merely arrives as []byte on the wire).
func isBinaryType(dbType string) bool {
	switch strings.ToUpper(dbType) {
	case "BINARY", "VARBINARY", "TINYBLOB", "BLOB", "MEDIUMBLOB", "LONGBLOB", "BIT":
		return true
	}
	return false
}

// binaryText renders bytes as hex, capped so a big BLOB cannot flood the grid.
func binaryText(b []byte) string {
	shown := b
	suffix := ""
	if len(shown) > maxCellBytes {
		shown = shown[:maxCellBytes]
		suffix = fmt.Sprintf("… (%d bytes)", len(b))
	}
	return "0x" + hex.EncodeToString(shown) + suffix
}

// formatTime renders a parsed date/time value per its column type. MySQL's
// zero date ('0000-00-00' / '0000-00-00 00:00:00') comes off the wire as Go's
// zero time.Time; printing it as 0001-01-01 would invent a value the server
// never held, so the server's spelling is restored.
func formatTime(dbType string, t time.Time) string {
	upperType := strings.ToUpper(dbType)
	if t.IsZero() {
		switch upperType {
		case "DATE":
			return "0000-00-00"
		case "TIME":
			return "00:00:00"
		default: // DATETIME, TIMESTAMP
			return "0000-00-00 00:00:00"
		}
	}
	switch upperType {
	case "DATE":
		return t.Format("2006-01-02")
	case "TIME":
		return t.Format("15:04:05.999999")
	default: // DATETIME, TIMESTAMP
		return t.Format("2006-01-02 15:04:05.999999")
	}
}

// returnsRows reports whether a statement produces a result set. database/sql
// cannot tell before running the statement, and MySQL's exec path would lose
// the result rows while its query path loses the affected-row count, so a
// classifier decides which path to take.
//
// The classifier errs towards the query path for every statement that can
// return rows: CALL (stored procedures return result sets), ANALYZE/OPTIMIZE/
// REPAIR/CHECKSUM TABLE, a parenthesised query expression such as
// "(SELECT 1) UNION (SELECT 2)", and MariaDB's INSERT/UPDATE/DELETE/REPLACE …
// RETURNING. A WITH statement is routed by its main verb: a data-modifying CTE
// without RETURNING goes through Exec so the affected count is right.
func returnsRows(query string) bool {
	if containsReturning(query) {
		return true
	}
	switch firstKeyword(query) {
	case "SELECT", "VALUES", "TABLE", "SHOW", "DESCRIBE", "DESC",
		"EXPLAIN", "HELP", "CHECK", "CHECKSUM", "CALL",
		"ANALYZE", "OPTIMIZE", "REPAIR", "(":
		return true
	case "WITH":
		switch withMainVerb(query) {
		case "SELECT", "VALUES", "TABLE":
			return true
		}
	}
	return false
}

// firstKeyword returns the first word of a statement, skipping leading
// whitespace and comments. A statement that starts with "(" (a parenthesised
// query expression) is reported as "(".
func firstKeyword(query string) string {
	s := query
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "#"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		}
		break
	}
	if s == "" {
		return ""
	}
	if s[0] == '(' {
		return "("
	}
	i := 0
	for i < len(s) && isSQLWordByte(s[i]) {
		i++
	}
	return strings.ToUpper(s[:i])
}

// containsReturning reports whether a statement carries a RETURNING clause
// (MariaDB prints the rows of INSERT/UPDATE/DELETE/REPLACE … RETURNING, and the
// exec path would discard them). Words inside strings, identifiers and
// comments do not count.
func containsReturning(query string) bool {
	found := false
	scanSQLTokens(query, func(word string, _ int) bool {
		if word == "RETURNING" {
			found = true
			return false
		}
		return true
	})
	return found
}

// withMainVerb returns the verb of a WITH statement's main statement: the CTE
// definitions are inside parentheses, so the first top-level verb among
// SELECT/VALUES/TABLE/INSERT/UPDATE/DELETE/REPLACE is the statement the WITH
// belongs to.
func withMainVerb(query string) string {
	verb := ""
	scanSQLTokens(query, func(word string, depth int) bool {
		if depth != 0 {
			return true
		}
		switch word {
		case "SELECT", "VALUES", "TABLE", "INSERT", "UPDATE", "DELETE", "REPLACE":
			verb = word
			return false
		}
		return true
	})
	return verb
}

// scanSQLTokens calls fn for every keyword-like word of a statement, with the
// parenthesis depth the word sits at. String literals, quoted identifiers and
// comments are skipped; fn returning false stops the scan.
func scanSQLTokens(query string, fn func(word string, depth int) bool) {
	depth := 0
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipSQLQuoted(query, i)
		case strings.HasPrefix(query[i:], "--"), c == '#':
			if j := strings.IndexByte(query[i:], '\n'); j >= 0 {
				i += j + 1
			} else {
				return
			}
		case strings.HasPrefix(query[i:], "/*"):
			end, ok := skipSQLBlockComment(query, i)
			if !ok {
				return
			}
			i = end
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case isSQLWordByte(c):
			j := i
			for j < len(query) && isSQLWordByte(query[j]) {
				j++
			}
			if !fn(strings.ToUpper(query[i:j]), depth) {
				return
			}
			i = j
		default:
			i++
		}
	}
}

// skipSQLBlockComment returns the index behind the block comment starting at
// s[start]; nested comments are skipped like a single one.
func skipSQLBlockComment(s string, start int) (int, bool) {
	depth := 0
	for i := start; i < len(s); {
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

// skipSQLQuoted returns the index behind the quoted string or identifier that
// starts at s[start]; a doubled quote escapes itself.
func skipSQLQuoted(s string, start int) int {
	quote := s[start]
	for i := start + 1; i < len(s); i++ {
		if s[i] != quote {
			continue
		}
		if i+1 < len(s) && s[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}
	return len(s)
}

// isSQLWordByte reports whether c can be part of a keyword.
func isSQLWordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_', c == '$':
		return true
	}
	return false
}

// dataSource turns the driver configuration into a parsed MySQL config. A DSN
// wins (a mysql:// or mariadb:// URL is converted; any other DSN is the
// driver's own format); otherwise the form fields are composed. The defaults a
// SQL client wants (parseTime so dates are real dates, utf8mb4 for full
// Unicode) apply unless the DSN sets the parameter itself.
func dataSource(cfg driver.Config) (*gomysql.Config, error) {
	raw := strings.TrimSpace(cfg.DSN)
	if raw == "" {
		raw = composeFromOptions(cfg.Options)
	} else if isURLDSN(raw) {
		return urlConfig(raw)
	}

	mc, err := gomysql.ParseDSN(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid connection string: %w", err)
	}
	if !mc.ParseTime && !dsnSets(raw, "parseTime") {
		mc.ParseTime = true
	}
	if !dsnSets(raw, "charset") && !dsnSets(raw, "collation") {
		if err := gomysql.Charset("utf8mb4", "")(mc); err != nil {
			return nil, fmt.Errorf("invalid connection string: %w", err)
		}
	}
	return mc, nil
}

// composeFromOptions builds a driver-native DSN from the form fields.
func composeFromOptions(opts map[string]string) string {
	get := func(key string) string { return strings.TrimSpace(opts[key]) }
	host, port := get("host"), get("port")
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "3306"
	}
	mc := gomysql.NewConfig()
	mc.User = get("user")
	if mc.User == "" {
		mc.User = "root" // the conventional local account, like the mysql client
	}
	mc.Passwd = opts["password"]
	mc.Net = "tcp"
	mc.Addr = hostPort(host, port)
	mc.DBName = get("database")
	return mc.FormatDSN()
}

// isURLDSN reports whether a DSN is URL-shaped (as composed by the connection
// form); anything else is left for the driver's own parser.
func isURLDSN(raw string) bool {
	return strings.HasPrefix(raw, mysqlName+"://") || strings.HasPrefix(raw, mariadbName+"://")
}

// urlConfig converts a URL-style DSN (mysql://user:pass@host:port/db?params,
// as composed by the connection form) into a driver config.
//
// URL parameters keep the meaning the driver's own DSN parser gives them:
// every Config-level parameter (tls, parseTime, the timeouts, …) is mapped
// onto its Config field by applyURLParam. Only genuinely unknown parameters
// become connection attributes — mc.Params is sent to the server verbatim in
// the handshake as key/value pairs the server merely records (e.g. in
// performance_schema.session_connect_attrs); it configures nothing, so a
// "tls=require" parked there would silently leave TLS off.
//
// The same client defaults as for a native DSN (parseTime, utf8mb4) apply
// when the URL does not set them itself.
func urlConfig(raw string) (*gomysql.Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// *url.Error echoes the whole URL — password included — so nothing of
		// the original text is carried into the message: it would surface in
		// the UI and the logs verbatim.
		return nil, errors.New("invalid connection URL: malformed DSN")
	}

	mc := gomysql.NewConfig()
	if u.User != nil {
		mc.User = u.User.Username()
		mc.Passwd, _ = u.User.Password()
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "3306"
	}
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(host, port)
	mc.DBName = strings.TrimPrefix(u.Path, "/")

	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		// The query parser quotes the offending parameter text, which the
		// user may have filled with credentials; only the failure is reported.
		return nil, errors.New("invalid connection URL: malformed query parameters")
	}

	// charset and collation are applied together after the loop, so the pair
	// works in either URL order: gomysql.Charset sets both fields, and an
	// inline charset application would drop a collation applied before it.
	var urlCharset, urlCollation string
	var charsetSet, collationSet bool
	for key, values := range params {
		switch key {
		case "charset":
			urlCharset, charsetSet = values[0], true
			continue // never a connection attribute
		case "collation":
			urlCollation, collationSet = values[0], true
			continue // never a connection attribute
		}
		mapped, err := applyURLParam(mc, key, values[0])
		if err != nil {
			return nil, err
		}
		// Only parameters the Config does not know stay connection attributes.
		if !mapped {
			if mc.Params == nil {
				mc.Params = make(map[string]string, len(params))
			}
			mc.Params[key] = values[0]
		}
	}

	switch {
	case charsetSet && collationSet:
		if err := gomysql.Charset(urlCharset, urlCollation)(mc); err != nil {
			return nil, fmt.Errorf("invalid connection URL: %w", err)
		}
	case charsetSet:
		if err := gomysql.Charset(urlCharset, "")(mc); err != nil {
			return nil, fmt.Errorf("invalid connection URL: %w", err)
		}
	case collationSet:
		// Only the collation: the charset stays up to the server default.
		mc.Collation = urlCollation
	default:
		if err := gomysql.Charset("utf8mb4", "")(mc); err != nil {
			return nil, fmt.Errorf("invalid connection URL: %w", err)
		}
	}

	// Same parseTime default as for a native DSN: dates arrive as dates unless
	// the URL asked for raw byte values.
	if !mc.ParseTime && !params.Has("parseTime") {
		mc.ParseTime = true
	}
	return mc, nil
}

// applyURLParam maps one URL query parameter onto the Config field the
// driver's own DSN parser (parseDSNParams in go-sql-driver) would set it to;
// the boolean return reports whether the key is a Config-level parameter at
// all. Semantics and value vocabulary mirror the driver exactly — booleans
// accept only what its readBool accepts, durations go through
// time.ParseDuration — and an invalid value is an error, not a silent
// connection attribute. Messages quote only the offending parameter and its
// value, never the URL, so credentials still cannot leak into the UI or logs.
func applyURLParam(mc *gomysql.Config, key, value string) (mapped bool, err error) {
	switch key {
	// Boolean parameters, sharing the driver's readBool vocabulary.
	case "allowAllFiles", "allowCleartextPasswords", "allowFallbackToPlaintext",
		"allowNativePasswords", "allowOldPasswords", "checkConnLiveness",
		"clientFoundRows", "columnsWithAlias", "compress", "interpolateParams",
		"multiStatements", "parseTime", "rejectReadOnly":
		b, ok := boolValue(value)
		if !ok {
			return true, fmt.Errorf("invalid bool value for the %s parameter: %q", key, value)
		}
		switch key {
		case "allowAllFiles":
			mc.AllowAllFiles = b
		case "allowCleartextPasswords":
			mc.AllowCleartextPasswords = b
		case "allowFallbackToPlaintext":
			mc.AllowFallbackToPlaintext = b
		case "allowNativePasswords":
			mc.AllowNativePasswords = b
		case "allowOldPasswords":
			mc.AllowOldPasswords = b
		case "checkConnLiveness":
			mc.CheckConnLiveness = b
		case "clientFoundRows":
			mc.ClientFoundRows = b
		case "columnsWithAlias":
			mc.ColumnsWithAlias = b
		case "compress":
			if err := gomysql.EnableCompression(b)(mc); err != nil {
				return true, err
			}
		case "interpolateParams":
			mc.InterpolateParams = b
		case "multiStatements":
			mc.MultiStatements = b
		case "parseTime":
			mc.ParseTime = b
		case "rejectReadOnly":
			mc.RejectReadOnly = b
		}
		return true, nil

	// Dial and I/O timeouts are Go durations ("5s", "500ms"), like a native DSN.
	case "timeout", "readTimeout", "writeTimeout":
		d, err := time.ParseDuration(value)
		if err != nil {
			return true, fmt.Errorf("invalid duration for the %s parameter: %w", key, err)
		}
		switch key {
		case "timeout":
			mc.Timeout = d
		case "readTimeout":
			mc.ReadTimeout = d
		case "writeTimeout":
			mc.WriteTimeout = d
		}
		return true, nil

	// timeTruncate is unexported on Config, so the driver's own Option applies it.
	case "timeTruncate":
		d, err := time.ParseDuration(value)
		if err != nil {
			return true, fmt.Errorf("invalid duration for the timeTruncate parameter: %w", err)
		}
		return true, gomysql.TimeTruncate(d)(mc)

	case "maxAllowedPacket":
		n, err := strconv.Atoi(value)
		if err != nil {
			return true, fmt.Errorf("invalid value for the maxAllowedPacket parameter: %w", err)
		}
		mc.MaxAllowedPacket = n
		return true, nil

	case "loc":
		// url.ParseQuery has already percent-decoded the value, matching the
		// driver's QueryUnescape before LoadLocation ("Local" included).
		loc, err := time.LoadLocation(value)
		if err != nil {
			return true, fmt.Errorf("invalid value for the loc parameter: %w", err)
		}
		mc.Loc = loc
		return true, nil

	// TLS uses the driver's own values: a boolean, the "skip-verify" or
	// "preferred" presets, or the name of a custom TLS configuration
	// registered with gomysql.RegisterTLSConfig. The registry lives in the
	// driver package, so a custom name travels as-is and is validated there
	// at connect time; here it only gets a syntax check, because the DSN
	// round trip (FormatDSN escapes, ParseDSN unescapes it back) could not
	// carry a name with ?/&/whitespace faithfully anyway.
	case "tls":
		if b, ok := boolValue(value); ok {
			mc.TLSConfig = "false"
			if b {
				mc.TLSConfig = "true"
			}
			return true, nil
		}
		if vl := strings.ToLower(value); vl == "skip-verify" || vl == "preferred" {
			mc.TLSConfig = vl
			return true, nil
		}
		if value == "" || strings.ContainsAny(value, "?& \t\r\n") {
			return true, fmt.Errorf("invalid value for the tls parameter: %q", value)
		}
		mc.TLSConfig = value
		return true, nil

	// Name-only values resolved from registries in the driver package
	// (gomysql.RegisterServerPubKey); unknown names error at connect time.
	case "serverPubKey":
		mc.ServerPubKey = value
		return true, nil

	case "connectionAttributes":
		mc.ConnectionAttributes = value
		return true, nil
	}
	return false, nil
}

// boolValue mirrors the driver's readBool: only its exact set of spellings
// counts as a boolean, so "yes", "on" or "maybe" are errors rather than
// silently truthy values.
func boolValue(value string) (b, ok bool) {
	switch value {
	case "1", "true", "TRUE", "True":
		return true, true
	case "0", "false", "FALSE", "False":
		return false, true
	}
	return false, false
}

// dsnSets reports whether the query part of a DSN sets the given parameter
// (used to tell "the user chose this" from "apply the default").
func dsnSets(dsn, key string) bool {
	i := strings.IndexByte(dsn, '?')
	if i < 0 {
		return false
	}
	for _, part := range strings.Split(dsn[i+1:], "&") {
		if k, _, ok := strings.Cut(part, "="); ok && k == key {
			return true
		}
	}
	return false
}

// hostPort joins a host and a port, accepting a host that already carries its
// own port (the form normally keeps them apart).
func hostPort(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}

// quoteIdent quotes a MySQL identifier with backticks.
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// displayName returns the name shown for the connection: the database name.
func displayName(mc *gomysql.Config, fallback string) string {
	if db := strings.TrimSpace(mc.DBName); db != "" {
		return db
	}
	return fallback
}

// endpoint describes the target for error messages without leaking a password.
func endpoint(mc *gomysql.Config) string {
	addr := mc.Addr
	if addr == "" {
		addr = "127.0.0.1:3306"
	}
	return addr + "/" + mc.DBName
}
