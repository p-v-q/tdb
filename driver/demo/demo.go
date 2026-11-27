// Package demo registers an offline demo connection: built-in sample metadata
// and no database behind it. It exists so that tdb can be explored without a
// server, and as the smallest possible example of a driver package.
//
// A driver is a package: it implements driver.Connection, calls
// driver.Register from init() and depends on nothing else in tdb. The binary
// (cmd/tdb) is the composition root that decides which drivers are linked in.
package demo

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
)

// factory creates demo connections.
type factory struct{}

// Name implements driver.Factory.
func (factory) Name() string { return "demo" }

// Open implements driver.Factory. The demo connection needs no configuration.
func (factory) Open(context.Context, driver.Config) (driver.Connection, error) {
	return &connection{name: "demo", dialect: dialect.MustGet("postgres")}, nil
}

// connection is an offline connection backed by catalog.DemoCatalog.
type connection struct {
	name    string
	dialect dialect.Dialect
	closed  atomic.Bool
}

// Name implements driver.Connection.
func (c *connection) Name() string { return c.name }

// Dialect implements driver.Connection (the demo data uses PostgreSQL types).
func (c *connection) Dialect() dialect.Dialect { return c.dialect }

// Catalog implements driver.Connection and catalog.Provider.
func (c *connection) Catalog(context.Context) (*catalog.Catalog, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("demo: connection is closed")
	}
	return catalog.DemoCatalog(), nil
}

// Query implements driver.Connection: the demo has nothing to execute against.
func (c *connection) Query(context.Context, string, ...any) (*driver.Result, error) {
	return nil, fmt.Errorf("the demo connection does not support executing queries: %w", driver.ErrNotImplemented)
}

// Definition implements driver.DefinitionProvider: the demo ships sample DDL
// so the object browser's "View definition" action can be explored offline,
// exactly like the sample catalog. Tables are generated from the catalog
// itself; the other kinds are written out to show the shape each kind has in
// PostgreSQL (the dialect the sample data uses).
func (c *connection) Definition(_ context.Context, ref driver.DefinitionRef) (string, error) {
	if c.closed.Load() {
		return "", fmt.Errorf("demo: connection is closed")
	}
	key := ref.Kind.String() + "\x00" + ref.Schema + "." + ref.Name
	if ddl, ok := demoDefinitions[key]; ok {
		return ddl, nil
	}
	if ref.Kind == catalog.KindTable {
		if tbl := catalog.DemoCatalog().FindTable(ref.Schema + "." + ref.Name); tbl != nil {
			return demoTableDDL(tbl), nil
		}
	}
	return "", fmt.Errorf("demo: no definition for %s %s.%s", ref.Kind, ref.Schema, ref.Name)
}

// demoTableDDL assembles a CREATE TABLE statement from the sample catalog's
// column metadata: name, type, NOT NULL, default and the primary key.
func demoTableDDL(tbl *catalog.Table) string {
	lines := make([]string, 0, len(tbl.Columns)+1)
	var pk []string
	for _, col := range tbl.Columns {
		line := "    " + col.Name + " " + col.Type
		if !col.Nullable {
			line += " NOT NULL"
		}
		if col.Default != "" {
			line += " DEFAULT " + col.Default
		}
		lines = append(lines, line)
		if col.PK {
			pk = append(pk, col.Name)
		}
	}
	if len(pk) > 0 {
		lines = append(lines, "    CONSTRAINT "+tbl.Name+"_pkey PRIMARY KEY ("+strings.Join(pk, ", ")+")")
	}
	return "CREATE TABLE " + tbl.Schema + "." + tbl.Name + " (\n" + strings.Join(lines, ",\n") + "\n);"
}

// demoDefinitions is the sample DDL of the non-table demo objects, keyed by
// kind, schema and name. Tables are absent on purpose: they are generated
// from the catalog so every demo table has a definition.
var demoDefinitions = map[string]string{
	"view\x00reporting.v_daily_sales": `CREATE VIEW reporting.v_daily_sales AS
SELECT date(o.placed_at) AS sale_date,
       count(*) AS order_count,
       sum(o.total_amount) AS gmv,
       avg(o.total_amount) AS avg_order_value
FROM public.orders o
WHERE o.status = 'paid'
GROUP BY 1`,
	"view\x00reporting.v_top_products": `CREATE VIEW reporting.v_top_products AS
SELECT oi.product_id,
       p.name AS product_name,
       sum(oi.quantity) AS units_sold,
       sum(oi.quantity * oi.unit_price) AS revenue
FROM public.order_items oi
JOIN public.products p ON p.id = oi.product_id
GROUP BY oi.product_id, p.name
ORDER BY revenue DESC
LIMIT 10`,
	"view\x00reporting.v_churn_risk": `CREATE VIEW reporting.v_churn_risk AS
SELECT u.id AS user_id,
       max(o.placed_at) AS last_order_at,
       date_part('day', now() - max(o.placed_at))::int AS days_since_order,
       CASE WHEN date_part('day', now() - max(o.placed_at)) > 90 THEN 'high' ELSE 'low' END AS risk_level
FROM public.users u
LEFT JOIN public.orders o ON o.user_id = u.id
GROUP BY u.id`,
	"materialized_view\x00reporting.mv_sales_daily": `CREATE MATERIALIZED VIEW reporting.mv_sales_daily AS
SELECT date(placed_at) AS sale_date,
       count(*) AS order_count,
       sum(total_amount) AS gmv
FROM public.orders
WHERE status = 'paid'
GROUP BY 1
WITH NO DATA`,
	"function\x00public.order_total": `CREATE FUNCTION public.order_total(order_id bigint) RETURNS numeric
    LANGUAGE sql STABLE
    AS $$
    SELECT sum(oi.quantity * oi.unit_price)
    FROM public.order_items oi
    WHERE oi.order_id = order_total.order_id
$$`,
	"function\x00public.user_status_label": `CREATE FUNCTION public.user_status_label(status user_status) RETURNS text
    LANGUAGE plpgsql IMMUTABLE
    AS $$
    BEGIN
        RETURN CASE status
                   WHEN 'active' THEN 'Active'
                   WHEN 'suspended' THEN 'Suspended'
                   ELSE 'Deleted'
               END;
    END;
$$`,
	"function\x00reporting.refresh_daily_sales": `CREATE PROCEDURE reporting.refresh_daily_sales()
    LANGUAGE sql
    AS $$
    REFRESH MATERIALIZED VIEW reporting.mv_sales_daily;
$$`,
	"procedure\x00public.archive_orders": `CREATE PROCEDURE public.archive_orders(before date)
    LANGUAGE plpgsql
    AS $$
    BEGIN
        INSERT INTO public.order_events (order_id, event_type, occurred_at)
        SELECT id, 'archived', now() FROM public.orders
        WHERE placed_at < before AND status = 'cancelled';
        DELETE FROM public.orders WHERE placed_at < before AND status = 'cancelled';
    END;
$$`,
	"trigger\x00public.orders_touch_updated": `CREATE TRIGGER orders_touch_updated BEFORE UPDATE ON public.orders
FOR EACH ROW EXECUTE FUNCTION public.touch_updated_at()`,
	"trigger\x00public.users_audit": `CREATE TRIGGER users_audit AFTER INSERT OR UPDATE ON public.users
FOR EACH ROW EXECUTE FUNCTION public.write_audit_event()`,
	"index\x00public.idx_orders_user_id": `CREATE INDEX idx_orders_user_id ON public.orders (user_id)`,
	"index\x00public.users_email_key":    `CREATE UNIQUE INDEX users_email_key ON public.users (email)`,
	"sequence\x00public.order_number_seq": `CREATE SEQUENCE public.order_number_seq
 AS bigint
    START WITH 1
    INCREMENT BY 1
    MINVALUE 1
    MAXVALUE 9223372036854775807
    CACHE 1
    NO CYCLE;`,
}

// Close implements driver.Connection.
func (c *connection) Close() error { c.closed.Store(true); return nil }

func init() { driver.Register(factory{}) }
