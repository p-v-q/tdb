// Package postgres implements driver.Connection for PostgreSQL through pgx
// (github.com/jackc/pgx/v5), a pure Go driver, so no cgo is required.
//
// The package follows the same shape as driver/sqlite: a factory registered
// from init(), catalog introspection through pg_catalog, statements through a
// pooled connection, NULL kept apart from the empty string, a row cap with a
// truncation flag, and cancellation through context.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
)

// maxRows caps a result set so that `SELECT *` on a huge table cannot exhaust
// memory; a truncated result says so instead of silently lying.
const maxRows = 1000

// schemaOption is the driver option that carries the connection's default
// schema: the schema unqualified names resolve in once the connection is open.
const schemaOption = "schema"

// driverName is the name this package registers in the driver registry.
const driverName = "postgres"

func init() { driver.Register(factory{}) }

// factory opens PostgreSQL connections.
type factory struct{}

// Name implements driver.Factory.
func (factory) Name() string { return driverName }

// Open implements driver.Factory. A DSN wins; otherwise the usual
// host/port/user/password/database options are composed into a URL. With no
// configuration at all pgx's own defaults (and the PG* environment variables)
// apply, exactly like libpq.
func (factory) Open(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
	pc, err := pgxpool.ParseConfig(dataSource(cfg))
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid connection string: %w", err)
	}
	// A desktop client does not need a big pool, and a small one keeps a
	// server-side connection limit from being hit by several tabs.
	if pc.MaxConns > 4 {
		pc.MaxConns = 4
	}
	// Do not cache prepared statements: in a SQL client the schema changes while
	// the program is running (ALTER TABLE, search_path, …), and a cached plan
	// would then describe the old shape of the table. Exec still uses the
	// extended protocol for parameters and result metadata, one round trip.
	pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	// The default schema has to hold on every connection of the pool, not
	// only on the first one: the pool opens more connections as the workload
	// grows, and a session without the search_path would resolve unqualified
	// names somewhere else.
	if schema := strings.TrimSpace(cfg.Options[schemaOption]); schema != "" {
		pc.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			return setSearchPath(ctx, conn, schema)
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: %s: %w", endpoint(pc), err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: %s: %w", endpoint(pc), err)
	}
	return &conn{
		pool:    pool,
		name:    displayName(pc),
		dialect: dialect.MustGet(driverName),
		types:   pgtype.NewMap(),
	}, nil
}

// conn is an open PostgreSQL database.
type conn struct {
	pool    *pgxpool.Pool
	name    string
	dialect dialect.Dialect
	types   *pgtype.Map
	// closed is read on the query goroutine and written by Close on the UI
	// goroutine, so it is atomic rather than a plain bool.
	closed atomic.Bool

	// mu guards tx: statements run on a background goroutine while the UI
	// may trigger Commit or Rollback concurrently.
	mu sync.Mutex
	tx pgx.Tx
}

// The connection offers explicit transaction control on top of Connection.
var _ driver.TransactionManager = (*conn)(nil)

// Name implements driver.Connection: the database name, as libpq calls it.
func (c *conn) Name() string { return c.name }

// Dialect implements driver.Connection.
func (c *conn) Dialect() dialect.Dialect { return c.dialect }

// Databases implements driver.ServerLister: the databases a user can connect
// to. Templates and databases that refuse connections are left out, because
// naming one in the connection form could never work.
func (c *conn) Databases(ctx context.Context) ([]string, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	rows, err := c.pool.Query(ctx, `
SELECT datname
FROM pg_catalog.pg_database
WHERE datallowconn AND NOT datistemplate
ORDER BY datname`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list databases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgres: list databases: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Schemas implements driver.SchemaLister: the user schemas of the connected
// database. The server's own schemas (pg_catalog, information_schema, …) are
// left out: the form offers places a user's statements can work in, and the
// connection cannot name a system schema as its default meaningfully.
func (c *conn) Schemas(ctx context.Context) ([]string, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	rows, err := c.pool.Query(ctx, `
SELECT nspname
FROM pg_catalog.pg_namespace
WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
ORDER BY nspname`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list schemas: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgres: list schemas: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Catalog implements driver.Connection: it reads the user schemas and their
// tables, views and columns from pg_catalog.
func (c *conn) Catalog(ctx context.Context) (*catalog.Catalog, error) {
	if err := c.check(); err != nil {
		return nil, err
	}

	out := &catalog.Catalog{
		Dialect:  driverName,
		LoadedAt: time.Now(),
		Capabilities: catalog.Capable(
			catalog.KindTable, catalog.KindView, catalog.KindMaterializedView,
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
		return nil, errors.New("postgres: nothing to execute")
	}

	start := time.Now()
	// Transaction-control statements typed by the user must own the session
	// exactly like the UI's manual-commit mode does (see TxAction): running
	// BEGIN through the pool would release the connection at the end of the
	// statement, and pgxpool destroys a connection left in a transaction, so
	// the transaction was rolled back while every following statement
	// autocommitted.
	if res, handled, err := c.transactionControl(ctx, query); handled {
		if err != nil {
			return nil, err
		}
		res.Duration = time.Since(start)
		return res, nil
	}

	// The row cap must stop the server-side statement, not drain it: pgx's
	// Rows.Close() reads and discards the rest of the result, which for a
	// `SELECT * FROM huge_table` keeps the UI waiting for the whole result
	// set. A cancellable child context lets the cap abort the query through
	// pgx's cancel request (and through the transaction's context).
	queryCtx, cancelQuery := context.WithCancel(ctx)
	defer cancelQuery()

	// Statements run through the open transaction while one exists, so the
	// whole batch lands in it; otherwise the pool decides which connection
	// serves the statement, exactly as before.
	var (
		rows pgx.Rows
		err  error
	)
	if tx := c.currentTx(); tx != nil {
		rows, err = tx.Query(queryCtx, query, args...)
	} else {
		rows, err = c.pool.Query(queryCtx, query, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	fields := rows.FieldDescriptions()

	res := &driver.Result{Duration: time.Since(start)}
	if len(fields) > 0 {
		res.Columns = make([]driver.ResultColumn, len(fields))
		for i, f := range fields {
			col := driver.ResultColumn{Name: f.Name}
			if typ, ok := c.types.TypeForOID(f.DataTypeOID); ok {
				col.Type = typ.Name
			}
			res.Columns[i] = col
		}
	}
	// JSON, arrays and timestamptz are read from the server's own text
	// representation: decoding and re-encoding them changes what the user
	// sees (large JSON integers, number spelling, key order, array
	// dimensions and bounds, the session's time zone).
	rawText := make([]bool, len(fields))
	needValues := false
	for i, f := range fields {
		rawText[i] = c.prefersRawText(f.DataTypeOID)
		if !rawText[i] {
			needValues = true
		}
	}

	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			cancelQuery()
			break
		}
		raw := rows.RawValues()
		var values []any
		if needValues {
			var err error
			values, err = rows.Values()
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("postgres: read row: %w", err)
			}
		}
		row := make([]*string, len(fields))
		for i := range fields {
			if i < len(rawText) && rawText[i] && i < len(raw) {
				if raw[i] == nil {
					continue // SQL NULL
				}
				s := string(raw[i])
				row[i] = &s
				continue
			}
			if i >= len(values) || values[i] == nil {
				continue // SQL NULL
			}
			s := c.formatValue(fields[i].DataTypeOID, values[i])
			row[i] = &s
		}
		res.Rows = append(res.Rows, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil && !res.Truncated {
		// A cancellation caused by the row cap is expected: the rows we kept
		// are the answer. Any other error is a real failure.
		return nil, fmt.Errorf("postgres: %w", err)
	}
	// Statements without a result set (INSERT/UPDATE/DELETE/DDL) report how many
	// rows they changed through the command tag instead.
	if len(fields) == 0 {
		res.Affected = rows.CommandTag().RowsAffected()
	}
	res.Duration = time.Since(start)
	return res, nil
}

// transactionControl routes a transaction-control statement the user typed to
// the connection's transaction manager, so the transaction owns a session for
// as long as it is open. Begin is idempotent: a raw BEGIN inside a managed
// transaction is handled locally instead of being sent to PostgreSQL, which
// rejects nested transactions. SAVEPOINT and RELEASE are not intercepted: inside
// a transaction they run on it, and outside one the server reports its normal
// error.
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
			// Outside a transaction PostgreSQL accepts COMMIT and ROLLBACK
			// with a warning and returns the command tag, so the user's
			// statement is not an error; there is nothing to end.
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
	c.pool.Close()
	return nil
}

// check guards against using a closed connection.
func (c *conn) check() error {
	if c.closed.Load() {
		return errors.New("postgres: connection is closed")
	}
	return nil
}

// ---- Transactions ----

// currentTx returns the open transaction, if any.
func (c *conn) currentTx() pgx.Tx {
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
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin transaction: %w", err)
	}
	c.tx = tx
	return nil
}

// Commit implements driver.TransactionManager.
func (c *conn) Commit(ctx context.Context) error {
	c.mu.Lock()
	tx := c.tx
	c.tx = nil // the transaction is over even when the server refuses to commit
	c.mu.Unlock()
	if tx == nil {
		return errors.New("postgres: no transaction to commit")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// Rollback implements driver.TransactionManager.
func (c *conn) Rollback(ctx context.Context) error {
	c.mu.Lock()
	tx := c.tx
	c.tx = nil
	c.mu.Unlock()
	if tx == nil {
		return errors.New("postgres: no transaction to roll back")
	}
	// ErrTxClosed means the transaction was already ended (by the server or
	// by a racing action); from the caller's point of view it is rolled back.
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("postgres: rollback: %w", err)
	}
	return nil
}

// InTx implements driver.TransactionManager.
func (c *conn) InTx() bool { return c.currentTx() != nil }

// ---- Introspection ----

// schemaNames lists the user-visible schemas (the system ones are implementation
// details, not user data).
func (c *conn) schemaNames(ctx context.Context) ([]string, error) {
	const q = `
SELECT n.nspname
FROM pg_catalog.pg_namespace n
WHERE n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
ORDER BY n.nspname`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres: list schemas: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgres: list schemas: %w", err)
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

// loadTables reads tables, partitioned tables, views and materialized views
// into their schemas, and returns them by id for the column pass. Foreign
// tables (relkind 'f') are deliberately left out: the driver has no
// CREATE FOREIGN TABLE assembly (it needs the foreign server, the user
// mapping and the per-column options), so showing the object would offer a
// "View definition" that cannot work. See the known limits on
// tableDefinitionQuery.
func (c *conn) loadTables(ctx context.Context, schemas map[string]*catalog.Schema, out *catalog.Catalog) (map[tableID]*catalog.Table, error) {
	const q = `
SELECT n.nspname, c.relname,
       CASE c.relkind
           WHEN 'v' THEN 'view'
           WHEN 'm' THEN 'materialized_view'
           ELSE 'table'
       END,
       COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), '')
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm')
  AND n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
ORDER BY n.nspname, c.relname`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres: list tables: %w", err)
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
			return nil, fmt.Errorf("postgres: list tables: %w", err)
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
		switch typ {
		case "view":
			t.Type = catalog.TypeView
		case "materialized_view":
			t.Type = catalog.TypeMaterializedView
		}
		s.Tables = append(s.Tables, t)
		tables[id] = t
	}
	return tables, rows.Err()
}

// loadColumns fills in the column list of every table: type, nullability,
// primary key, uniqueness, default, comment and ordinal position.
func (c *conn) loadColumns(ctx context.Context, tables map[tableID]*catalog.Table) error {
	const q = `
SELECT n.nspname, c.relname, a.attname,
       pg_catalog.format_type(a.atttypid, a.atttypmod),
       NOT a.attnotnull,
       COALESCE(pg_catalog.pg_get_expr(d.adbin, d.adrelid), ''),
       a.attnum,
       COALESCE(pg_catalog.col_description(c.oid, a.attnum), ''),
       EXISTS (
           SELECT 1 FROM pg_catalog.pg_index i
           WHERE i.indrelid = c.oid AND i.indisprimary
             AND a.attnum = ANY (i.indkey)
       ),
       EXISTS (
           SELECT 1 FROM pg_catalog.pg_index i
           WHERE i.indrelid = c.oid AND i.indisunique
             AND i.indisvalid AND i.indpred IS NULL AND i.indexprs IS NULL
             AND i.indnkeyatts = 1
             AND a.attnum = ANY (i.indkey)
       )
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attnum > 0 AND NOT a.attisdropped
  AND c.relkind IN ('r', 'p', 'v', 'm')
  AND n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
ORDER BY n.nspname, c.relname, a.attnum`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("postgres: list columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id       tableID
			name     string
			typ      string
			nullable bool
			def      string
			ordinal  int
			comment  string
			pk       bool
			unique   bool
		)
		if err := rows.Scan(&id.schema, &id.table, &name, &typ, &nullable,
			&def, &ordinal, &comment, &pk, &unique); err != nil {
			return fmt.Errorf("postgres: list columns: %w", err)
		}
		t := tables[id]
		if t == nil {
			continue
		}
		t.Columns = append(t.Columns, &catalog.Column{
			Name:     name,
			Type:     typ,
			Nullable: nullable,
			PK:       pk,
			Unique:   unique,
			Default:  def,
			Comment:  comment,
			Ordinal:  ordinal,
		})
	}
	return rows.Err()
}

// loadResources reads the non-relation objects PostgreSQL exposes in
// pg_catalog. They are split into small queries instead of one UNION so each
// resource shape stays readable and a future resource only adds one function.
func (c *conn) loadResources(ctx context.Context, schemas map[string]*catalog.Schema) error {
	if err := c.loadFunctions(ctx, schemas); err != nil {
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

// loadFunctions reads ordinary functions and procedures. Functions contributed
// by an extension (pg_trgm, PostGIS, …) are listed along with the user's own:
// they are callable like any other function, and hiding them makes a database
// installed with extensions look emptier than it is. Only the system schemas
// stay hidden.
//
// Aggregates and window functions are left out on purpose: pg_get_functiondef
// cannot print them (CREATE AGGREGATE needs different syntax and a window
// function is a C implementation), so the catalog would offer a "View
// definition" that always fails. They remain callable by name from the editor.
func (c *conn) loadFunctions(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT n.nspname, p.proname,
       pg_catalog.pg_get_function_identity_arguments(p.oid),
       COALESCE(pg_catalog.pg_get_function_result(p.oid), ''),
       l.lanname,
       p.prokind::text,
       COALESCE(pg_catalog.obj_description(p.oid, 'pg_proc'), '')
FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
JOIN pg_catalog.pg_language l ON l.oid = p.prolang
WHERE n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
  AND p.prokind IN ('f', 'p')
ORDER BY n.nspname, p.proname, 3`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("postgres: list functions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			schema, name, args, result, language, kind, comment string
		)
		if err := rows.Scan(&schema, &name, &args, &result, &language, &kind, &comment); err != nil {
			return fmt.Errorf("postgres: list functions: %w", err)
		}
		s := schemas[schema]
		if s == nil {
			continue
		}
		resourceKind := catalog.KindFunction
		if kind == "p" {
			resourceKind = catalog.KindProcedure
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:    resourceKind,
			Schema:  schema,
			Name:    name,
			Detail:  functionDetail(args, result, language, kind),
			Comment: comment,
		})
	}
	return rows.Err()
}

// functionDetail builds the compact "(args) → result · language" description.
func functionDetail(args, result, language, kind string) string {
	out := "(" + args + ")"
	if kind != "p" && result != "" {
		out += " → " + result
	}
	switch kind {
	case "a":
		out += " · aggregate"
	case "w":
		out += " · window"
	}
	if language != "" {
		out += " · " + language
	}
	return out
}

// loadTriggers reads user-defined triggers. The tgtype bits are the stable
// pg_trigger representation of timing and events (see PostgreSQL's
// pg_trigger.h); decoding them avoids shipping full CREATE TRIGGER text into a
// one-line tree row.
func (c *conn) loadTriggers(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT n.nspname, t.tgname, c.relname, t.tgtype,
       COALESCE(pg_catalog.obj_description(t.oid, 'pg_trigger'), '')
FROM pg_catalog.pg_trigger t
JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE NOT t.tgisinternal
  AND n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
ORDER BY n.nspname, c.relname, t.tgname`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("postgres: list triggers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			schema, name, table, comment string
			kind                         int
		)
		if err := rows.Scan(&schema, &name, &table, &kind, &comment); err != nil {
			return fmt.Errorf("postgres: list triggers: %w", err)
		}
		s := schemas[schema]
		if s == nil {
			continue
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:    catalog.KindTrigger,
			Schema:  schema,
			Name:    name,
			Owner:   table,
			Detail:  triggerDetail(kind),
			Comment: comment,
		})
	}
	return rows.Err()
}

// triggerDetail decodes pg_trigger.tgtype into e.g. "BEFORE INSERT OR UPDATE".
func triggerDetail(tgtype int) string {
	timing := "AFTER"
	switch {
	case tgtype&(1<<1) != 0:
		timing = "BEFORE"
	case tgtype&(1<<6) != 0:
		timing = "INSTEAD OF"
	}
	var events []string
	if tgtype&(1<<2) != 0 {
		events = append(events, "INSERT")
	}
	if tgtype&(1<<3) != 0 {
		events = append(events, "DELETE")
	}
	if tgtype&(1<<4) != 0 {
		events = append(events, "UPDATE")
	}
	if tgtype&(1<<5) != 0 {
		events = append(events, "TRUNCATE")
	}
	if len(events) == 0 {
		return timing
	}
	return timing + " " + strings.Join(events, " OR ")
}

// loadIndexes reads valid indexes on user relations.
func (c *conn) loadIndexes(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT n.nspname, c.relname, i.relname, idx.indisunique, idx.indisprimary,
       COALESCE(pg_catalog.obj_description(i.oid, 'pg_class'), '')
FROM pg_catalog.pg_index idx
JOIN pg_catalog.pg_class i ON i.oid = idx.indexrelid
JOIN pg_catalog.pg_class c ON c.oid = idx.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE idx.indisvalid
  AND c.relkind IN ('r', 'p', 'm')
  AND n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
ORDER BY n.nspname, c.relname, i.relname`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("postgres: list indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			schema, table, name, comment string
			unique, primary              bool
		)
		if err := rows.Scan(&schema, &table, &name, &unique, &primary, &comment); err != nil {
			return fmt.Errorf("postgres: list indexes: %w", err)
		}
		s := schemas[schema]
		if s == nil {
			continue
		}
		detail := ""
		switch {
		case primary:
			detail = "primary key"
		case unique:
			detail = "unique"
		}
		s.Resources = append(s.Resources, &catalog.Resource{
			Kind:    catalog.KindIndex,
			Schema:  schema,
			Name:    name,
			Owner:   table,
			Detail:  detail,
			Comment: comment,
		})
	}
	return rows.Err()
}

// loadSequences reads standalone and serial-owned sequences.
func (c *conn) loadSequences(ctx context.Context, schemas map[string]*catalog.Schema) error {
	const q = `
SELECT n.nspname, c.relname,
       COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), '')
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'S'
  AND n.nspname <> 'information_schema'
  AND n.nspname !~ '^pg_'
ORDER BY n.nspname, c.relname`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("postgres: list sequences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, name, comment string
		if err := rows.Scan(&schema, &name, &comment); err != nil {
			return fmt.Errorf("postgres: list sequences: %w", err)
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

// ---- Default schema ----

// setSearchPath points a session at the connection's default schema, so that
// unqualified names in the editor resolve there.
//
// The schema is quoted as an identifier (pgx.Identifier.Sanitize), so a name
// with quotes, spaces or a semicolon cannot break out of the statement.
//
// A schema that does not exist is left alone: PostgreSQL accepts a search_path
// that names a missing schema without complaining, but every unqualified name
// would then resolve to nothing. Keeping the server default leaves the
// connection usable, and the client reports the missing schema on its own.
func setSearchPath(ctx context.Context, conn *pgx.Conn, schema string) error {
	if schema == "" {
		return nil
	}
	exists, err := schemaExists(ctx, conn, schema)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := conn.Exec(ctx, searchPathStatement(schema)); err != nil {
		return fmt.Errorf("postgres: set the search_path to %q: %w", schema, err)
	}
	return nil
}

// searchPathStatement returns the statement that points a session at a
// schema. The name is quoted as an identifier, so a name with quotes, spaces
// or a semicolon cannot break out of the statement.
//
// The chosen schema is put first, and the server's own default path is kept
// after it: unqualified names still find public tables (and a schema named
// after the role), exactly as they do in psql, where the default is
// "$user", public. Replacing the whole search_path would make "SELECT * FROM
// users" work while "SELECT * FROM customers" — a public table — suddenly
// failed, which is the kind of surprise the default is supposed to avoid.
func searchPathStatement(schema string) string {
	parts := []string{pgx.Identifier{schema}.Sanitize()}
	switch schema {
	case "$user":
		parts = append(parts, "public")
	case "public":
		parts = append(parts, `"$user"`)
	default:
		parts = append(parts, `"$user"`, "public")
	}
	return "SET search_path TO " + strings.Join(parts, ", ")
}

// schemaExists reports whether the database has a schema of that name. The
// rule is the one the catalog uses (see schemaNames): the system schemas are
// implementation details, not a place to work in, so they do not count.
func schemaExists(ctx context.Context, conn *pgx.Conn, schema string) (bool, error) {
	const q = `
SELECT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_namespace n
    WHERE n.nspname = $1
      AND n.nspname <> 'information_schema'
      AND n.nspname !~ '^pg_'
)`
	var exists bool
	if err := conn.QueryRow(ctx, q, schema).Scan(&exists); err != nil {
		return false, fmt.Errorf("postgres: look up the schema %q: %w", schema, err)
	}
	return exists, nil
}

// ---- Object definitions ----

// Definition implements driver.DefinitionProvider: PostgreSQL prints the
// definition of most catalog objects through pg_get_* functions; tables and
// sequences are assembled from pg_catalog instead, and views are wrapped in
// the CREATE VIEW statement that pg_get_viewdef omits, so every result is a
// re-executable statement — the contract driver.DefinitionProvider documents.
// When several objects share a name in one schema (overloaded functions,
// triggers on different tables), DefinitionRef.Owner selects the trigger's
// table and functions resolve deterministically to the lowest
// identity-argument order.
func (c *conn) Definition(ctx context.Context, ref driver.DefinitionRef) (string, error) {
	if err := c.check(); err != nil {
		return "", err
	}
	var (
		ddl string
		err error
	)
	switch ref.Kind {
	case catalog.KindTable:
		ddl, err = c.tableDefinition(ctx, ref)
	case catalog.KindView:
		// pg_get_viewdef prints only the SELECT body; wrapping it in
		// CREATE VIEW … AS makes the definition a re-executable statement,
		// the way the MySQL and SQLite drivers (and this driver's tables)
		// present it. The body names its relations the way the server
		// prints them — qualified only when they are not visible in the
		// session's search_path — so it is read with an empty search_path.
		//
		// Reloptions carry enforcement and planning behavior that the body
		// does not show (security_barrier, security_invoker, fillfactor, …)
		// and check_option is a reloption too: copying a view without them
		// re-creates a weaker view than the one on the server. They are
		// rendered where PostgreSQL accepts them — a WITH (...) list before
		// AS, and WITH [CASCADED|LOCAL] CHECK OPTION after the body — never
		// as a raw WITH (check_option=…) list, which the server rejects.
		ddl, err = c.qualifiedDefinition(ctx, `
SELECT 'CREATE VIEW ' || format('%I.%I', n.nspname, r.relname) ||
       coalesce((SELECT E'\nWITH (' || string_agg(opt, ', ' ORDER BY opt) || ')'
                   FROM unnest(coalesce(r.reloptions, '{}'::text[])) AS opt
                  WHERE opt NOT LIKE 'check_option=%'), '') ||
       E'\nAS\n' || pg_catalog.pg_get_viewdef(r.oid, true) ||
       coalesce((SELECT E'\nWITH ' || upper(split_part(opt, '=', 2)) || ' CHECK OPTION'
                   FROM unnest(coalesce(r.reloptions, '{}'::text[])) AS opt
                  WHERE opt LIKE 'check_option=%'
                  LIMIT 1), '')
FROM pg_catalog.pg_class r
JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
WHERE n.nspname = $1 AND r.relname = $2 AND r.relkind = 'v'`, ref)
	case catalog.KindMaterializedView:
		// WITH NO DATA: the definition is meant to be read, copied and
		// re-executed later; WITH DATA would run the underlying query at
		// creation time, which can be expensive — the reader refreshes the
		// materialized view when its contents are wanted.
		ddl, err = c.qualifiedDefinition(ctx, `
SELECT 'CREATE MATERIALIZED VIEW ' || format('%I.%I', n.nspname, r.relname) || E'\nAS\n' ||
       pg_catalog.pg_get_viewdef(r.oid, true) || E'\nWITH NO DATA'
FROM pg_catalog.pg_class r
JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
WHERE n.nspname = $1 AND r.relname = $2 AND r.relkind = 'm'`, ref)
	case catalog.KindFunction, catalog.KindProcedure:
		// The requested kind decides which pg_proc rows match: a function and
		// a procedure can share a name and a schema, and the catalog lists
		// them as separate objects.
		prokind := "f"
		if ref.Kind == catalog.KindProcedure {
			prokind = "p"
		}
		ddl, err = c.textDefinition(ctx, `
SELECT pg_catalog.pg_get_functiondef(p.oid)
FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = $1 AND p.proname = $2 AND p.prokind = $3
ORDER BY pg_catalog.pg_get_function_identity_arguments(p.oid)`, ref, prokind)
	case catalog.KindTrigger:
		ddl, err = c.triggerDefinition(ctx, ref)
	case catalog.KindIndex:
		// pg_get_indexdef qualifies the indexed relation only when it is not
		// visible in the session's search_path, so it runs with an empty one
		// like the table/view definitions: the printed statement must mean the
		// same thing wherever it is re-executed.
		ddl, err = c.qualifiedDefinition(ctx, `
SELECT pg_catalog.pg_get_indexdef(i.oid)
FROM pg_catalog.pg_index x
JOIN pg_catalog.pg_class i ON i.oid = x.indexrelid
JOIN pg_catalog.pg_class r ON r.oid = x.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
WHERE n.nspname = $1 AND i.relname = $2 AND x.indisvalid`, ref)
	case catalog.KindSequence:
		ddl, err = c.sequenceDefinition(ctx, ref)
	default:
		err = fmt.Errorf("postgres: no stored definitions for %s", ref.Kind)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(ddl) == "" {
		return "", fmt.Errorf("postgres: %s %s.%s not found", ref.Kind, ref.Schema, ref.Name)
	}
	return ddl, nil
}

// textDefinition runs a one-column query that selects a single definition,
// turning "no row" and NULL into errors that name the object.
func (c *conn) textDefinition(ctx context.Context, query string, ref driver.DefinitionRef, extra ...any) (string, error) {
	return readDefinition(ctx, c.pool, query, ref, extra...)
}

// qualifiedDefinition reads a definition with an empty search_path, so that
// the relations the server prints carry their schema: the body of a view
// names its tables unqualified as long as they are visible in the session's
// search_path, which would make the statement mean different things in
// different sessions.
func (c *conn) qualifiedDefinition(ctx context.Context, query string, ref driver.DefinitionRef) (string, error) {
	var ddl string
	err := c.withEmptySearchPath(ctx, func(ctx context.Context, q rowQuerier) error {
		var err error
		ddl, err = readDefinition(ctx, q, query, ref)
		return err
	})
	if err != nil {
		return "", err
	}
	return ddl, nil
}

// readDefinition runs a one-column query that selects a single definition,
// turning "no row" and NULL into errors that name the object. The query takes
// the schema and name as $1 and $2; extra arguments are appended after them.
func readDefinition(ctx context.Context, q rowQuerier, query string, ref driver.DefinitionRef, extra ...any) (string, error) {
	args := append([]any{ref.Schema, ref.Name}, extra...)
	var ddl sql.NullString
	if err := q.QueryRow(ctx, query, args...).Scan(&ddl); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("postgres: %s %s.%s not found", ref.Kind, ref.Schema, ref.Name)
		}
		return "", fmt.Errorf("postgres: definition of %s %s.%s: %w", ref.Kind, ref.Schema, ref.Name, err)
	}
	if !ddl.Valid {
		return "", fmt.Errorf("postgres: %s %s.%s has no stored definition", ref.Kind, ref.Schema, ref.Name)
	}
	return ddl.String, nil
}

// triggerDefinition reads a trigger's CREATE statement. Trigger names are
// unique per table, not per schema, so the owning table (DefinitionRef.Owner)
// narrows the search when the catalog provided one.
//
// Like the table/view/index definitions it runs with an empty search_path:
// pg_get_triggerdef qualifies the table and the trigger function only when they
// are not visible in the session's search_path, and the definition must be
// re-executable somewhere else without binding to the wrong function.
func (c *conn) triggerDefinition(ctx context.Context, ref driver.DefinitionRef) (string, error) {
	const query = `
SELECT pg_catalog.pg_get_triggerdef(t.oid, true)
FROM pg_catalog.pg_trigger t
JOIN pg_catalog.pg_class r ON r.oid = t.tgrelid
JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
WHERE n.nspname = $1 AND t.tgname = $2 AND NOT t.tgisinternal
  AND ($3 = '' OR r.relname = $3)
ORDER BY r.relname`
	var ddl sql.NullString
	err := c.withEmptySearchPath(ctx, func(ctx context.Context, q rowQuerier) error {
		return q.QueryRow(ctx, query, ref.Schema, ref.Name, ref.Owner).Scan(&ddl)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("postgres: trigger %s.%s not found", ref.Schema, ref.Name)
		}
		return "", fmt.Errorf("postgres: definition of trigger %s.%s: %w", ref.Schema, ref.Name, err)
	}
	if !ddl.Valid {
		return "", fmt.Errorf("postgres: trigger %s.%s has no stored definition", ref.Schema, ref.Name)
	}
	return ddl.String, nil
}

// tableDefinitionQuery assembles the CREATE TABLE statement of one table. The
// column list and the named constraints are built in separate steps so the
// comma between them only appears when a column list exists: a table declared
// with table-level constraints alone (or nothing at all) must not render a
// leading comma inside the parentheses. Catalog helpers are schema-qualified
// so a search_path that places pg_catalog after a user schema cannot shadow
// them.
//
// Known limits, deliberate for now: identity sequence options
// (START/INCREMENT/…), PARTITION BY, inheritance and per-column storage
// attributes (COLLATE/COMPRESSION/STORAGE) are not reproduced; a partitioned
// table must not make the query fail either. Foreign tables are not
// catalogued at all (see loadTables), so no definition is offered for them.
// UNLOGGED is reproduced from relpersistence.
const tableDefinitionQuery = `
SELECT CASE c.relpersistence WHEN 'u' THEN 'CREATE UNLOGGED TABLE ' ELSE 'CREATE TABLE ' END ||
       pg_catalog.format('%I.%I', c.nspname, c.relname) || E'\n(\n' ||
       COALESCE(c.coldef, '') ||
       COALESCE(
           (SELECT CASE WHEN c.coldef IS NULL OR c.coldef = '' THEN '' ELSE E',\n' END ||
                   pg_catalog.string_agg(
                       '    CONSTRAINT ' || pg_catalog.format('%I', con.conname) || ' ' ||
                       pg_catalog.pg_get_constraintdef(con.oid),
                       E',\n'
                       ORDER BY CASE con.contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'c' THEN 2 WHEN 'f' THEN 3 ELSE 4 END,
                                con.conname)
            FROM pg_catalog.pg_constraint con
            WHERE con.conrelid = c.oid AND con.contype IN ('p', 'u', 'c', 'f', 'x')),
           '') ||
       E'\n);'
FROM (
    SELECT r.oid, n.nspname, r.relname, r.relpersistence,
           pg_catalog.string_agg(
               CASE WHEN a.attnum > 0 THEN
                   '    ' || pg_catalog.format('%I', a.attname) || ' ' ||
                   pg_catalog.format_type(a.atttypid, a.atttypmod) ||
                   CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END ||
                   CASE
                       WHEN a.attgenerated = 's' THEN
                           ' GENERATED ALWAYS AS (' || pg_catalog.pg_get_expr(d.adbin, d.adrelid) || ') STORED'
                       WHEN a.attgenerated = 'v' THEN
                           ' GENERATED ALWAYS AS (' || pg_catalog.pg_get_expr(d.adbin, d.adrelid) || ') VIRTUAL'
                       WHEN a.attidentity = 'a' THEN ' GENERATED ALWAYS AS IDENTITY'
                       WHEN a.attidentity = 'd' THEN ' GENERATED BY DEFAULT AS IDENTITY'
                       WHEN a.attgenerated = '' THEN
                           COALESCE(' DEFAULT ' || pg_catalog.pg_get_expr(d.adbin, d.adrelid), '')
                       ELSE ''
                   END
               END,
               E',\n' ORDER BY a.attnum) AS coldef
    FROM pg_catalog.pg_class r
    JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
    LEFT JOIN pg_catalog.pg_attribute a
      ON a.attrelid = r.oid AND a.attnum > 0 AND NOT a.attisdropped
    LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
    WHERE n.nspname = $1 AND r.relname = $2 AND r.relkind IN ('r', 'p')
    GROUP BY r.oid, n.nspname, r.relname, r.relpersistence
) c`

// tableDefinition assembles CREATE TABLE from pg_catalog: the column list in
// attribute order with types, NOT NULL, defaults, identity and generated
// columns (STORED, and VIRTUAL on PostgreSQL 18 and newer), followed by the
// named table constraints (primary key, unique, check, foreign key and
// exclusion) through pg_get_constraintdef, which reproduces referenced
// columns, ON UPDATE/DELETE actions, matching and deferrability. PostgreSQL
// cannot print a whole table definition itself, so this is the one definition
// the driver assembles — the statement is meant for reading and re-executing,
// which is what the object browser is for. The known limits live with
// tableDefinitionQuery above.
//
// The query runs with an empty search_path, so that the referenced relation of
// a foreign key is printed with its schema: the statement then means the same
// thing in every session and can be re-executed anywhere (see
// withEmptySearchPath).
func (c *conn) tableDefinition(ctx context.Context, ref driver.DefinitionRef) (string, error) {
	// The selected text starts from a string constant and every part that
	// could be NULL is COALESCEd, so the server never answers NULL and there
	// is no unreachable "no stored definition" branch to check; an empty
	// statement is rejected by Definition.
	var ddl string
	err := c.withEmptySearchPath(ctx, func(ctx context.Context, q rowQuerier) error {
		return q.QueryRow(ctx, tableDefinitionQuery, ref.Schema, ref.Name).Scan(&ddl)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("postgres: table %s.%s not found", ref.Schema, ref.Name)
		}
		return "", fmt.Errorf("postgres: definition of table %s.%s: %w", ref.Schema, ref.Name, err)
	}
	return ddl, nil
}

// rowQuerier reads a single row; a transaction and a connection both satisfy
// it.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// withEmptySearchPath runs fn on a pooled connection whose search_path holds
// no user schema, so that the catalog helpers print every relation with its
// schema.
//
// pg_get_constraintdef qualifies a referenced relation only when it is not
// visible in the session's search_path: with the default search_path
// ("$user", public) a foreign key into public prints as "REFERENCES
// parent(id)", while the same table in another schema prints as "REFERENCES
// other.parent(id)". The very same table would then produce two different
// statements depending on where the client was connected, and the unqualified
// one does not run in a schema other than the default one — the definition
// promises a re-executable statement.
//
// The setting is applied with SET LOCAL inside a transaction on a dedicated
// pooled connection: SET LOCAL is reverted when the transaction ends, so the
// connection returns to the pool with the search_path it had and no other user
// of the pool sees the change. The transaction is rolled back, so the query
// writes nothing; a connection that could not be rolled back (a cancelled
// context) is destroyed by the pool instead of being handed out again.
func (c *conn) withEmptySearchPath(ctx context.Context, fn func(context.Context, rowQuerier) error) error {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire a connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin a transaction: %w", err)
	}
	// The rollback ends the transaction and with it the local setting.
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO ''"); err != nil {
		return fmt.Errorf("postgres: clear the search_path: %w", err)
	}
	return fn(ctx, tx)
}

// sequenceDefinition reads a sequence's parameters from pg_sequence — the one
// shape pg_get_* does not cover — and assembles the CREATE SEQUENCE statement.
// A sequence owned by a column (a serial/identity sequence) carries that
// ownership in pg_depend, so the statement reproduces it instead of dropping
// it and leaving the sequence behind when the table is dropped.
func (c *conn) sequenceDefinition(ctx context.Context, ref driver.DefinitionRef) (string, error) {
	const q = `
SELECT format('%I.%I', n.nspname, r.relname),
       format_type(s.seqtypid, NULL),
       s.seqstart, s.seqmin, s.seqmax, s.seqincrement, s.seqcycle, s.seqcache,
       COALESCE((
           SELECT pg_catalog.format('%I.%I.%I', tn.nspname, tc.relname, ta.attname)
           FROM pg_catalog.pg_depend d
           JOIN pg_catalog.pg_class tc ON tc.oid = d.refobjid
           JOIN pg_catalog.pg_namespace tn ON tn.oid = tc.relnamespace
           JOIN pg_catalog.pg_attribute ta
             ON ta.attrelid = d.refobjid AND ta.attnum = d.refobjsubid
           WHERE d.classid = 'pg_catalog.pg_class'::pg_catalog.regclass
             AND d.objid = s.seqrelid
             AND d.refclassid = 'pg_catalog.pg_class'::pg_catalog.regclass
             AND d.deptype IN ('a', 'i')
           LIMIT 1), '')
FROM pg_catalog.pg_sequence s
JOIN pg_catalog.pg_class r ON r.oid = s.seqrelid
JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
WHERE n.nspname = $1 AND r.relname = $2`
	var (
		name, typ string
		start     int64
		min, max  int64
		inc       int64
		cycle     bool
		cache     int64
		ownedBy   string
	)
	if err := c.pool.QueryRow(ctx, q, ref.Schema, ref.Name).
		Scan(&name, &typ, &start, &min, &max, &inc, &cycle, &cache, &ownedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("postgres: sequence %s.%s not found", ref.Schema, ref.Name)
		}
		return "", fmt.Errorf("postgres: definition of sequence %s.%s: %w", ref.Schema, ref.Name, err)
	}
	return sequenceDDL(name, typ, start, min, max, inc, cycle, cache, ownedBy), nil
}

// sequenceDDL renders a CREATE SEQUENCE statement from the parameters
// pg_sequence reports. ownedBy is the "schema.table.column" the sequence
// belongs to, or "" for a standalone sequence.
func sequenceDDL(name, typ string, start, min, max, inc int64, cycle bool, cache int64, ownedBy string) string {
	cycleWord := "NO CYCLE"
	if cycle {
		cycleWord = "CYCLE"
	}
	ddl := fmt.Sprintf(
		"CREATE SEQUENCE %s\n AS %s\n    START WITH %d\n    INCREMENT BY %d\n    MINVALUE %d\n    MAXVALUE %d\n    CACHE %d\n    %s",
		name, typ, start, inc, min, max, cache, cycleWord)
	if ownedBy != "" {
		ddl += "\n    OWNED BY " + ownedBy
	}
	return ddl + ";"
}

// ---- Values and configuration ----

// prefersRawText reports whether a column is rendered from the server's own
// text representation instead of being decoded into Go values and re-encoded.
//
// That round trip is lossy in exactly the places a database client cannot
// afford it: json/jsonb numbers go through float64 in encoding/json (a
// snowflake ID becomes a different number, 1.0 becomes 1, 1e2 becomes 100, key
// order and duplicate keys are lost), arrays lose their dimensions and lower
// bounds when flattened, and timestamptz is decoded to a time.Time that is then
// printed in UTC instead of the session's time zone. OIDs whose text form is
// the whole point of the value are read with pgx's RawValues.
func (c *conn) prefersRawText(oid uint32) bool {
	switch oid {
	case pgtype.JSONOID, pgtype.JSONBOID, pgtype.TimestamptzOID:
		return true
	}
	typ, ok := c.types.TypeForOID(oid)
	if !ok {
		return false
	}
	// Arrays are registered with their internal name ("_int4") but always use
	// the array codec; the server's text form keeps the dimensions and bounds.
	_, isArray := typ.Codec.(*pgtype.ArrayCodec)
	return isArray
}

// formatValue renders a decoded PostgreSQL value the way the server would print
// it in text format, which is what a result grid wants.
func (c *conn) formatValue(oid uint32, v any) string {
	// PostgreSQL prints booleans as t/f in text format; true/false reads better
	// in a grid and matches what the other drivers show.
	if b, ok := v.(bool); ok {
		return strconv.FormatBool(b)
	}
	// pgtype's text encoder prints Go's "+Inf"/"-Inf", while PostgreSQL's own
	// text form (and the rest of the grid) says "Infinity"/"-Infinity".
	switch x := v.(type) {
	case float64:
		if math.IsInf(x, 1) {
			return "Infinity"
		}
		if math.IsInf(x, -1) {
			return "-Infinity"
		}
	case float32:
		if math.IsInf(float64(x), 1) {
			return "Infinity"
		}
		if math.IsInf(float64(x), -1) {
			return "-Infinity"
		}
	}
	if plan := c.types.PlanEncode(oid, pgtype.TextFormatCode, v); plan != nil {
		if buf, err := plan.Encode(v, nil); err == nil {
			return string(buf)
		}
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

// dataSource turns the driver configuration into a libpq-style connection
// string. A DSN wins; otherwise the form fields are composed into a URL.
func dataSource(cfg driver.Config) string {
	if dsn := strings.TrimSpace(cfg.DSN); dsn != "" {
		return dsn
	}
	opts := cfg.Options
	if opts == nil {
		return "" // let pgx apply its defaults (PGHOST, PGUSER, …)
	}
	host := strings.TrimSpace(opts["host"])
	user := strings.TrimSpace(opts["user"])
	password := opts["password"]
	database := strings.TrimSpace(opts["database"])
	port := strings.TrimSpace(opts["port"])
	if host == "" && user == "" && database == "" && port == "" {
		return ""
	}
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "5432"
	}

	u := url.URL{Scheme: "postgres", Host: hostPort(host, port)}
	if database != "" {
		u.Path = "/" + database
	}
	if user != "" {
		if password != "" {
			u.User = url.UserPassword(user, password)
		} else {
			u.User = url.User(user)
		}
	}
	return u.String()
}

// hostPort joins a host and a port, accepting a host that already carries its
// own port (the form normally keeps them apart).
func hostPort(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}

// displayName returns the name shown for the connection: the database name.
func displayName(pc *pgxpool.Config) string {
	if db := strings.TrimSpace(pc.ConnConfig.Database); db != "" {
		return db
	}
	return "postgres"
}

// endpoint describes the target for error messages without leaking a password.
func endpoint(pc *pgxpool.Config) string {
	cc := pc.ConnConfig
	host := cc.Host
	if host == "" {
		host = "localhost"
	}
	name := cc.Database
	if name == "" {
		name = "postgres"
	}
	return fmt.Sprintf("%s:%d/%s", host, cc.Port, name)
}
