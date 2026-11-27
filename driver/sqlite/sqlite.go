// Package sqlite implements driver.Connection for SQLite databases through
// database/sql and modernc.org/sqlite (a pure Go port, so no cgo and no
// platform surprises).
//
// The driver lives in its own package on purpose: the core driver interfaces
// stay free of database-specific code and dependencies. A new database is a new
// package that registers a factory, and nothing else in tdb changes.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
)

// maxRows caps a result set so that `SELECT *` on a huge table cannot exhaust
// memory; a truncated result says so instead of silently lying.
const maxRows = 1000

// driverName is the name this package registers in the driver registry.
const driverName = "sqlite"

func init() { driver.Register(factory{}) }

// factory opens SQLite connections.
type factory struct{}

// Name implements driver.Factory.
func (factory) Name() string { return driverName }

// Open implements driver.Factory. The database location comes from the DSN
// (a path or a file: URL) or from the "file" option; ":memory:" opens a private
// in-memory database.
func (factory) Open(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
	loc := strings.TrimSpace(cfg.DSN)
	if loc == "" {
		loc = strings.TrimSpace(cfg.Options["file"])
	}
	if loc == "" {
		return nil, errors.New(`sqlite: no database file given (use the File field or a DSN such as file:mydb.sqlite)`)
	}

	db, err := sql.Open("sqlite", loc)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %s: %w", loc, err)
	}
	// SQLite has a single writer anyway; a small pool keeps concurrent readers
	// working and avoids "database is locked" surprises.
	db.SetMaxOpenConns(4)
	if isInMemory(loc) {
		// An in-memory database is private to its connection: the pool must
		// stay at one connection or every statement sees a different database.
		db.SetMaxOpenConns(1)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: %s: %w", loc, err)
	}
	return &conn{
		db:      db,
		name:    displayName(loc),
		dialect: dialect.MustGet(driverName),
	}, nil
}

// conn is an open SQLite database.
type conn struct {
	db      *sql.DB
	name    string
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

// Name implements driver.Connection: the database is shown by its file name.
func (c *conn) Name() string { return c.name }

// Dialect implements driver.Connection.
func (c *conn) Dialect() dialect.Dialect { return c.dialect }

// Catalog implements driver.Connection: it introspects every attached database
// (main, plus anything the user attached) and its tables, views and columns.
func (c *conn) Catalog(ctx context.Context) (*catalog.Catalog, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	dbs, err := c.databases(ctx)
	if err != nil {
		return nil, err
	}
	// WITHOUT ROWID and STRICT decide whether a PRIMARY KEY column can hold
	// NULL (see columns); one PRAGMA answers for every attached database.
	tableFlags := c.tableFlags(ctx)
	out := &catalog.Catalog{
		Dialect:  driverName,
		LoadedAt: time.Now(),
		// SQLite stores views, triggers and indexes. Functions are registered
		// in the connection at runtime and are not part of the schema, and
		// SQLite has no sequences or stored procedures.
		Capabilities: catalog.Capable(
			catalog.KindTable, catalog.KindView,
			catalog.KindTrigger, catalog.KindIndex,
		),
	}
	for _, dbName := range dbs {
		schema := &catalog.Schema{Name: dbName}
		objects, err := c.objects(ctx, dbName)
		if err != nil {
			return nil, err
		}
		for _, o := range objects {
			table := &catalog.Table{Schema: dbName, Name: o.name, Type: catalog.TypeTable}
			if o.typ == "view" {
				table.Type = catalog.TypeView
			}
			cols, err := c.columns(ctx, dbName, o.name, tableFlags[tableKey(dbName, o.name)])
			if err != nil {
				return nil, err
			}
			table.Columns = cols
			schema.Tables = append(schema.Tables, table)
		}
		resources, err := c.resources(ctx, dbName)
		if err != nil {
			return nil, err
		}
		schema.Resources = resources
		out.Schemas = append(out.Schemas, schema)
	}
	return out, nil
}

// Query implements driver.Connection: one statement at a time, honouring ctx so
// that a running statement can be cancelled.
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
		return nil, errors.New("sqlite: nothing to execute")
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

	// Statements run through the open transaction while one exists, so the
	// whole batch lands in it; otherwise a dedicated pooled connection makes
	// changes() and last_insert_rowid() refer to the statement that just ran,
	// instead of whatever the pool happened to do.
	var q queryer
	if tx := c.currentTx(); tx != nil {
		q = tx
	} else {
		db, err := c.db.Conn(ctx)
		if err != nil {
			return nil, fmt.Errorf("sqlite: %w", err)
		}
		defer db.Close()
		q = db
	}

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	if len(names) == 0 {
		// No result set: report how many rows the statement changed. changes()
		// counts the last INSERT/UPDATE/DELETE on the connection, so it is
		// only meaningful for a DML statement; for DDL, PRAGMA and transaction
		// statements it would report some earlier statement's count.
		res := &driver.Result{Duration: time.Since(start)}
		if isDMLStatement(query) {
			var affected int64
			if err := q.QueryRowContext(ctx, "SELECT changes()").Scan(&affected); err == nil {
				res.Affected = affected
			}
		}
		return res, nil
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
			break
		}
		row, err := scanRow(rows, len(names))
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Close implements driver.Connection.
func (c *conn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Closing the pool rolls back an open transaction; database/sql discards
	// what it never committed, so no work survives a connection silently.
	return c.db.Close()
}

// transactionControl routes a transaction-control statement the user typed to
// the connection's transaction manager, so the transaction owns a session for
// as long as it is open. Begin is idempotent: a raw BEGIN inside a managed
// transaction is handled locally instead of reaching SQLite, which rejects
// nested transactions. SAVEPOINT and RELEASE are not intercepted: inside a
// transaction they run on it, and outside one they are left to SQLite (which
// reports its own error for most of them).
func (c *conn) transactionControl(ctx context.Context, query string) (*driver.Result, bool, error) {
	action := driver.TransactionControl(query)
	switch action {
	case driver.TxBegin:
		if err := c.Begin(ctx); err != nil {
			return nil, true, err
		}
		return &driver.Result{}, true, nil
	case driver.TxCommit, driver.TxRollback:
		// SQLite refuses COMMIT/ROLLBACK when no transaction is active, so
		// without one the statement runs through the pool and the server's
		// normal error reaches the user instead of being hidden.
		if c.currentTx() == nil {
			return nil, false, nil
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
	case driver.TxSavepoint:
		// SQLite starts a transaction implicitly when SAVEPOINT is used
		// outside one. Running it on the pool would leave that transaction on
		// a pooled connection, invisible to InTx and to the next statement, so
		// the implicit transaction is opened on the driver's transaction
		// instead; the SAVEPOINT then runs on it.
		if c.currentTx() == nil {
			if err := c.Begin(ctx); err != nil {
				return nil, true, err
			}
		}
		return nil, false, nil
	}
	return nil, false, nil
}

// ---- Transactions ----

// queryer is what statements run through: the open transaction while one is
// open, the pool otherwise. Introspection routes through the same gate as
// execution, which matters for an in-memory database: its pool holds a single
// connection, so a transaction would otherwise starve every catalog query
// until it is closed.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// currentTx returns the open transaction, if any.
func (c *conn) currentTx() *sql.Tx {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tx
}

// queryer returns what statements should run through right now.
func (c *conn) queryer() queryer {
	if tx := c.currentTx(); tx != nil {
		return tx
	}
	return c.db
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
		return fmt.Errorf("sqlite: begin transaction: %w", err)
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
		return errors.New("sqlite: no transaction to commit")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
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
		return errors.New("sqlite: no transaction to roll back")
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("sqlite: rollback: %w", err)
	}
	return nil
}

// InTx implements driver.TransactionManager.
func (c *conn) InTx() bool { return c.currentTx() != nil }

// ---- Object definitions ----

// definitionTypes maps the catalog kind vocabulary onto SQLite's
// sqlite_master.type values. Kinds SQLite cannot store (functions,
// procedures, sequences, materialized views) are absent and produce a clear
// error instead of a wrong guess.
var definitionTypes = map[catalog.ResourceKind]string{
	catalog.KindTable:   "table",
	catalog.KindView:    "view",
	catalog.KindTrigger: "trigger",
	catalog.KindIndex:   "index",
}

// Definition implements driver.DefinitionProvider: SQLite stores every CREATE
// statement verbatim in (schema.)sqlite_master.sql, so the definition is read
// back exactly as it was written, semicolons in trigger bodies included.
func (c *conn) Definition(ctx context.Context, ref driver.DefinitionRef) (string, error) {
	if err := c.check(); err != nil {
		return "", err
	}
	typ, ok := definitionTypes[ref.Kind]
	if !ok {
		return "", fmt.Errorf("sqlite: %s has no stored definitions", ref.Kind)
	}
	schema := ref.Schema
	if schema == "" {
		schema = "main"
	}
	q := fmt.Sprintf("SELECT sql FROM %s.sqlite_master WHERE type = ? AND name = ?", quoteIdent(schema))
	var stored sql.NullString
	if err := c.queryer().QueryRowContext(ctx, q, typ, ref.Name).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("sqlite: %s %s.%s not found", typ, schema, ref.Name)
		}
		return "", fmt.Errorf("sqlite: definition of %s %s.%s: %w", typ, schema, ref.Name, err)
	}
	if !stored.Valid || strings.TrimSpace(stored.String) == "" {
		// Objects SQLite creates on its own (auto-indexes) have no stored
		// CREATE statement; the tree does not show them either.
		return "", fmt.Errorf("sqlite: %s %s.%s has no stored definition", typ, schema, ref.Name)
	}
	return stored.String, nil
}

// check guards against using a closed connection.
func (c *conn) check() error {
	if c.closed.Load() {
		return errors.New("sqlite: connection is closed")
	}
	return nil
}

// databases lists the attached databases (at least "main").
func (c *conn) databases(ctx context.Context) ([]string, error) {
	rows, err := c.queryer().QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list databases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return nil, fmt.Errorf("sqlite: list databases: %w", err)
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		out = []string{"main"} // always present, even for an empty database
	}
	return out, rows.Err()
}

// object is one table or view of a database.
type object struct {
	name, typ string
}

// objects lists the tables and views of a database (SQLite's internal ones are
// left out: they are implementation details, not user data). `_` is a LIKE
// wildcard, hence the ESCAPE: only the literal "sqlite_" prefix is reserved,
// and a user table named "sqlitex" must still be listed.
func (c *conn) objects(ctx context.Context, dbName string) ([]object, error) {
	q := fmt.Sprintf(`SELECT name, type FROM %s.sqlite_master
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%%' ESCAPE '\'
		ORDER BY type, name`, quoteIdent(dbName))
	rows, err := c.queryer().QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: introspect %s: %w", dbName, err)
	}
	defer rows.Close()
	var out []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.name, &o.typ); err != nil {
			return nil, fmt.Errorf("sqlite: introspect %s: %w", dbName, err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// resources reads the triggers and explicit indexes of a database. SQLite
// keeps their definitions in sqlite_master, alongside tables and views.
func (c *conn) resources(ctx context.Context, dbName string) ([]*catalog.Resource, error) {
	q := fmt.Sprintf(`SELECT type, name, tbl_name, sql FROM %s.sqlite_master
		WHERE type IN ('trigger', 'index')
		  AND name NOT LIKE 'sqlite\_%%' ESCAPE '\'
		  AND sql IS NOT NULL
		ORDER BY type, name`, quoteIdent(dbName))
	rows, err := c.queryer().QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: introspect resources of %s: %w", dbName, err)
	}
	defer rows.Close()

	var out []*catalog.Resource
	for rows.Next() {
		var typ, name, table, definition string
		if err := rows.Scan(&typ, &name, &table, &definition); err != nil {
			return nil, fmt.Errorf("sqlite: introspect resources of %s: %w", dbName, err)
		}
		resource := &catalog.Resource{Schema: dbName, Name: name, Owner: table}
		switch typ {
		case "trigger":
			resource.Kind = catalog.KindTrigger
			resource.Detail = sqliteTriggerDetail(definition)
		case "index":
			resource.Kind = catalog.KindIndex
			if strings.Contains(strings.ToUpper(definition), "UNIQUE INDEX") {
				resource.Detail = "unique"
			}
		}
		out = append(out, resource)
	}
	return out, rows.Err()
}

// sqliteTriggerDetail extracts the timing and events from a trigger definition
// for a compact tree label. The full definition stays in SQLite; this is only
// a description, so an unusual definition falls back to an empty detail rather
// than showing a wrong one.
func sqliteTriggerDetail(definition string) string {
	// Only the header before BEGIN describes the trigger; a body may contain
	// INSERT/UPDATE/DELETE statements of its own and must not become events.
	sql := " " + strings.Join(strings.Fields(strings.ToUpper(definition)), " ") + " "
	if i := strings.Index(sql, " BEGIN "); i >= 0 {
		sql = sql[:i] + " "
	}
	var timing string
	for _, candidate := range []string{"INSTEAD OF", "BEFORE", "AFTER"} {
		if strings.Contains(sql, " "+candidate+" ") {
			timing = candidate
			break
		}
	}
	var events []string
	for _, candidate := range []string{"INSERT", "UPDATE", "DELETE"} {
		if strings.Contains(sql, " "+candidate+" ") {
			events = append(events, candidate)
		}
	}
	parts := make([]string, 0, 2)
	if timing != "" {
		parts = append(parts, timing)
	}
	if len(events) > 0 {
		parts = append(parts, strings.Join(events, " OR "))
	}
	return strings.Join(parts, " ")
}

// tableFlags records the table attributes that decide whether a PRIMARY KEY
// column is implicitly NOT NULL.
type tableFlags struct {
	withoutRowid bool
	strict       bool
}

// tableKey keys the per-table flag map. A NUL byte cannot appear in an
// identifier, so the two names cannot collide.
func tableKey(schema, table string) string { return schema + "\x00" + table }

// tableFlags reads PRAGMA table_list, which reports each table's WITHOUT ROWID
// and STRICT flags, for every attached database. Older SQLite versions do not
// have the pragma; the map is then nil and columns falls back to the plain
// rowid-table rule, which is correct for ordinary tables.
func (c *conn) tableFlags(ctx context.Context) map[string]tableFlags {
	rows, err := c.queryer().QueryContext(ctx, "PRAGMA table_list")
	if err != nil {
		return nil
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil
	}
	schemaIdx, nameIdx, wrIdx, strictIdx := -1, -1, -1, -1
	for i, name := range names {
		switch strings.ToLower(name) {
		case "schema":
			schemaIdx = i
		case "name":
			nameIdx = i
		case "wr":
			wrIdx = i
		case "strict":
			strictIdx = i
		}
	}
	if schemaIdx < 0 || nameIdx < 0 || wrIdx < 0 || strictIdx < 0 {
		return nil
	}
	out := make(map[string]tableFlags)
	vals := make([]any, len(names))
	targets := make([]any, len(names))
	for i := range vals {
		targets[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return nil
		}
		schema, ok := vals[schemaIdx].(string)
		if !ok {
			continue
		}
		name, ok := vals[nameIdx].(string)
		if !ok {
			continue
		}
		out[tableKey(schema, name)] = tableFlags{
			withoutRowid: intValue(vals[wrIdx]) != 0,
			strict:       intValue(vals[strictIdx]) != 0,
		}
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return out
}

// intValue converts the integer a PRAGMA column scanned as (database/sql
// returns int64 for SQLite integers).
func intValue(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

// columns reads the column list of a table or view through PRAGMA table_xinfo.
// table_xinfo (unlike table_info) also lists generated columns — STORED and
// VIRTUAL — which are part of the table's shape even though they cannot be
// written. hidden==1 marks the internal columns of virtual tables (an FTS
// rowid, …); those stay out of the user's view.
func (c *conn) columns(ctx context.Context, dbName, table string, flags tableFlags) ([]*catalog.Column, error) {
	q := fmt.Sprintf("PRAGMA %s.table_xinfo(%s)", quoteIdent(dbName), quoteIdent(table))
	rows, err := c.queryer().QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: columns of %s.%s: %w", dbName, table, err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqlite: columns of %s.%s: %w", dbName, table, err)
	}
	// table_xinfo columns: cid, name, type, notnull, dflt_value, pk, hidden.
	// Find them by name so a future SQLite reordering cannot silently swap
	// the values (the pragma is stable, but the map scan is cheap).
	idx := func(want string) int {
		for i, name := range names {
			if strings.EqualFold(name, want) {
				return i
			}
		}
		return -1
	}
	cidIdx, nameIdx, typeIdx := idx("cid"), idx("name"), idx("type")
	notNullIdx, dfltIdx, pkIdx, hiddenIdx := idx("notnull"), idx("dflt_value"), idx("pk"), idx("hidden")
	if cidIdx < 0 || nameIdx < 0 || typeIdx < 0 || notNullIdx < 0 || dfltIdx < 0 || pkIdx < 0 || hiddenIdx < 0 {
		return nil, fmt.Errorf("sqlite: columns of %s.%s: unexpected table_xinfo shape %v", dbName, table, names)
	}

	type columnInfo struct {
		cid     int
		name    string
		typ     string
		notNull int
		def     sql.NullString
		pk      int
	}
	var (
		infos []columnInfo
		vals  = make([]any, len(names))
	)
	targets := make([]any, len(names))
	for i := range vals {
		targets[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("sqlite: columns of %s.%s: %w", dbName, table, err)
		}
		if intValue(vals[hiddenIdx]) == 1 {
			continue // a virtual table's internal column
		}
		info := columnInfo{
			cid:     int(intValue(vals[cidIdx])),
			notNull: int(intValue(vals[notNullIdx])),
			pk:      int(intValue(vals[pkIdx])),
		}
		info.name, _ = vals[nameIdx].(string)
		info.typ, _ = vals[typeIdx].(string)
		switch d := vals[dfltIdx].(type) {
		case string:
			info.def = sql.NullString{String: d, Valid: true}
		case []byte:
			info.def = sql.NullString{String: string(d), Valid: true}
		}
		infos = append(infos, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: columns of %s.%s: %w", dbName, table, err)
	}

	pkColumns := 0
	for _, info := range infos {
		if info.pk > 0 {
			pkColumns++
		}
	}
	out := make([]*catalog.Column, 0, len(infos))
	for _, info := range infos {
		// SQLite only guarantees NOT NULL for a primary key column when it is
		// an INTEGER PRIMARY KEY (the rowid alias), when NOT NULL is spelled
		// out, or when the table is WITHOUT ROWID/STRICT. An ordinary rowid
		// table accepts NULL in a TEXT PRIMARY KEY, so claiming otherwise
		// would make the grid offer an edit the database rejects.
		nullable := info.notNull == 0
		if info.pk > 0 {
			rowidAlias := pkColumns == 1 && strings.EqualFold(strings.TrimSpace(info.typ), "INTEGER")
			nullable = info.notNull == 0 && !rowidAlias && !flags.withoutRowid && !flags.strict
		}
		out = append(out, &catalog.Column{
			Name:     info.name,
			Type:     info.typ,
			Nullable: nullable,
			PK:       info.pk > 0,
			Default:  info.def.String,
			Ordinal:  info.cid,
		})
	}
	return out, nil
}

// scanRow reads one row, keeping NULLs apart from empty strings.
func scanRow(rows *sql.Rows, n int) ([]*string, error) {
	raw := make([]any, n)
	targets := make([]any, n)
	for i := range raw {
		targets[i] = &raw[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, fmt.Errorf("sqlite: read row: %w", err)
	}
	row := make([]*string, n)
	for i, v := range raw {
		switch t := v.(type) {
		case nil:
			row[i] = nil // SQL NULL
		case []byte:
			s := string(t)
			row[i] = &s
		case string:
			row[i] = &t
		default:
			s := fmt.Sprint(t)
			row[i] = &s
		}
	}
	return row, nil
}

// quoteIdent quotes a SQLite identifier.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// isInMemory reports whether the DSN points at a database that lives in memory
// (and is therefore private to a single connection unless cache=shared is
// given).
//
// SQLite accepts several spellings, and the pool must be forced to one
// connection for all of them: ":memory:", "file::memory:", any of those with a
// query string (":memory:?_pragma=busy_timeout(5000)"), and a URI whose mode
// parameter is memory ("file:memdb1?mode=memory&cache=shared"). Missing one
// means CREATE TABLE and INSERT can land on different private databases.
func isInMemory(loc string) bool {
	l := strings.ToLower(strings.TrimSpace(loc))
	if i := strings.IndexByte(l, '?'); i >= 0 {
		if queryHasMemoryMode(l[i+1:]) {
			return true
		}
		l = l[:i]
	}
	return strings.TrimSpace(l) == ":memory:" || strings.TrimSpace(l) == "file::memory:"
}

// queryHasMemoryMode reports whether a URI query string selects mode=memory.
func queryHasMemoryMode(query string) bool {
	for _, part := range strings.FieldsFunc(query, func(r rune) bool { return r == '&' || r == ';' }) {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "mode") &&
			strings.EqualFold(strings.TrimSpace(value), "memory") {
			return true
		}
	}
	return false
}

// ---- Statement classification ----

// isDMLStatement reports whether a statement changes rows, which is when
// SQLite's changes() counter describes it. Only the leading keyword (or, for a
// WITH statement, the verb of its main statement) decides.
func isDMLStatement(query string) bool {
	switch firstStatementKeyword(query) {
	case "INSERT", "UPDATE", "DELETE", "REPLACE":
		return true
	case "WITH":
		switch firstTopLevelVerb(query) {
		case "INSERT", "UPDATE", "DELETE", "REPLACE":
			return true
		}
	}
	return false
}

// firstStatementKeyword returns the first word of a statement uppercased,
// skipping leading whitespace and comments. A statement that starts with "(" is
// reported as "(".
func firstStatementKeyword(query string) string {
	s := skipStatementSpaceAndComments(query)
	if s == "" {
		return ""
	}
	if s[0] == '(' {
		return "("
	}
	i := 0
	for i < len(s) && isStatementWordByte(s[i]) {
		i++
	}
	return strings.ToUpper(s[:i])
}

// firstTopLevelVerb returns the first main-statement keyword of a WITH query:
// the CTE definitions are inside parentheses, so the first top-level verb is
// the keyword of the statement the WITH belongs to.
func firstTopLevelVerb(query string) string {
	depth := 0
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipStatementQuoted(query, i)
		case strings.HasPrefix(query[i:], "--"), c == '#':
			if j := strings.IndexByte(query[i:], '\n'); j >= 0 {
				i += j + 1
			} else {
				return ""
			}
		case strings.HasPrefix(query[i:], "/*"):
			end, ok := skipStatementBlockComment(query, i)
			if !ok {
				return ""
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
		case isStatementWordByte(c):
			j := i
			for j < len(query) && isStatementWordByte(query[j]) {
				j++
			}
			if depth == 0 {
				switch word := strings.ToUpper(query[i:j]); word {
				case "INSERT", "UPDATE", "DELETE", "REPLACE", "SELECT", "VALUES", "TABLE":
					return word
				}
			}
			i = j
		default:
			i++
		}
	}
	return ""
}

// skipStatementSpaceAndComments skips leading whitespace and comments.
func skipStatementSpaceAndComments(s string) string {
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
			end, ok := skipStatementBlockComment(s, 0)
			if !ok {
				return ""
			}
			s = s[end:]
			continue
		}
		return s
	}
}

// skipStatementBlockComment returns the index behind the block comment that
// starts at s[start].
func skipStatementBlockComment(s string, start int) (int, bool) {
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

// skipStatementQuoted returns the index behind the quoted string or identifier
// that starts at s[start]; a doubled quote escapes itself.
func skipStatementQuoted(s string, start int) int {
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

// isStatementWordByte reports whether c can be part of a keyword.
func isStatementWordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_', c == '$':
		return true
	}
	return false
}

// displayName turns a DSN into the name shown in the UI: a database is called
// after its file, not after the directory that happens to hold it.
func displayName(loc string) string {
	name := strings.TrimPrefix(loc, "file:")
	if i := strings.IndexAny(name, "?&"); i >= 0 {
		name = name[:i]
	}
	if name == "" || name == ":memory:" {
		return ":memory:"
	}
	return filepath.Base(name)
}
