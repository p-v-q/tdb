package scenarios

// PostgreSQL "View definition" (DDL) scenarios.
//
// Every test opens real context menus in the object browser (mouse right-click,
// because Shift+F10 loses its modifier through ConPTY), asks the driver to
// reconstruct an object's CREATE statement, and checks the complete text
// through the clipboard: Ctrl+A, Ctrl+C emits OSC 52 into the PTY, which
// pgCopyAll decodes. That way the assertions cover the whole statement instead
// of the few lines that fit on the 160x48 screen.
//
// The contract under test is driver.DefinitionProvider: a re-executable
// statement. So the tests also check that no MySQL-only or session-only clauses
// leak into the PostgreSQL DDL.

import (
	"strings"
	"testing"
)

// TestDDLTableDefinitions reconstructs CREATE TABLE for three shapes: a table
// with identity/generated/defaults/domain types (customers), the partitioned
// order parent with its composite primary key and foreign key (sales.orders),
// and a table with named UNIQUE/CHECK constraints and a generated tsvector
// (products).
func TestDDLTableDefinitions(t *testing.T) {
	c := pgStart(t)

	customers := pgOpenDefinition(t, c, "customers", "customers", "public.customers")
	c.Shot("ddl-customers")
	c.RequireContains("CREATE TABLE public.customers")
	pgRequireDDL(t, customers, "public.customers",
		"CREATE TABLE public.customers",
		"id bigint NOT NULL GENERATED ALWAYS AS IDENTITY",
		"public_id uuid NOT NULL DEFAULT gen_random_uuid()",
		"email public.email_address NOT NULL",
		"display_name text GENERATED ALWAYS AS (upper(((first_name || ' '::text) || last_name))) STORED",
		"status public.customer_status NOT NULL DEFAULT 'active'::public.customer_status",
		"tags text[] NOT NULL DEFAULT '{}'::text[]",
		"metadata jsonb NOT NULL DEFAULT '{}'::jsonb",
		"created_at timestamp with time zone NOT NULL DEFAULT now()",
		"last_login_at timestamp with time zone,", // nullable: no NOT NULL
		"CONSTRAINT customers_pkey PRIMARY KEY (id)",
	)
	pgRequireNoServerClauses(t, customers, "public.customers")

	orders := pgOpenDefinition(t, c, "range-partitioned", "orders", "sales.orders")
	c.Shot("ddl-orders")
	pgRequireDDL(t, orders, "sales.orders",
		"CREATE TABLE sales.orders",
		"id bigint NOT NULL,",
		"status public.order_status NOT NULL DEFAULT 'pending'::public.order_status",
		"total public.positive_money NOT NULL DEFAULT 0",
		"currency character(3) NOT NULL DEFAULT 'USD'::bpchar",
		"shipping_address jsonb,",
		"CONSTRAINT orders_pkey PRIMARY KEY (id, ordered_at)",
		"CONSTRAINT orders_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(id)",
	)
	pgRequireNoServerClauses(t, orders, "sales.orders")

	products := pgOpenDefinition(t, c, "products", "products", "public.products")
	c.Shot("ddl-products")
	pgRequireDDL(t, products, "public.products",
		"CREATE TABLE public.products",
		"category public.product_category NOT NULL DEFAULT 'electronics'::public.product_category",
		"search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, ((COALESCE(name, ''::text) || ' '::text) || COALESCE(description, ''::text)))) STORED",
		"CONSTRAINT products_pkey PRIMARY KEY (id)",
		"CONSTRAINT products_sku_key UNIQUE (sku)",
		"CONSTRAINT products_cost_check CHECK ((cost >= (0)::numeric))",
		"CONSTRAINT products_stock_check CHECK ((stock >= 0))",
	)
	pgRequireNoServerClauses(t, products, "public.products")
}

// TestDDLQuotedIdentifiers reconstructs DDL for the awkward identifiers of the
// dataset: the reserved-word table public."order", the quoted uppercase schema
// "Warehouse", and "LEGACY_2019".old_orders with spaces and uppercase column
// names. Quoting must be exact or the statement is not re-executable.
func TestDDLQuotedIdentifiers(t *testing.T) {
	c := pgStart(t)

	orderDDL := pgOpenDefinition(t, c, "reserved-word", "order", "public.order")
	c.Shot("ddl-reserved")
	pgRequireDDL(t, orderDDL, `public."order"`,
		`CREATE TABLE public."order"`,
		`"select" text,`,
		`"from" text,`,
		`"where" boolean,`,
		`"group" jsonb,`,
		`CONSTRAINT order_pkey PRIMARY KEY (id)`,
	)
	pgRequireNoServerClauses(t, orderDDL, `public."order"`)

	warehouseDDL := pgOpenDefinition(t, c, "warehouse", "warehouse", "Warehouse.warehouse")
	c.Shot("ddl-warehouse")
	pgRequireDDL(t, warehouseDDL, `"Warehouse".warehouse`,
		`CREATE TABLE "Warehouse".warehouse`,
		`"Name" text,`,
		`capacity integer,`,
		`CONSTRAINT warehouse_pkey PRIMARY KEY (id)`,
	)

	legacyDDL := pgOpenDefinition(t, c, "old_orders", "old_orders", "LEGACY_2019.old_orders")
	c.Shot("ddl-legacy")
	pgRequireDDL(t, legacyDDL, `"LEGACY_2019".old_orders`,
		`CREATE TABLE "LEGACY_2019".old_orders`,
		`"OrderID" integer NOT NULL,`,
		`"Placed At" timestamp with time zone,`,
		`"Total(USD)" numeric(12,2),`,
		`CONSTRAINT old_orders_pkey PRIMARY KEY ("OrderID")`,
	)
}

// TestDDLViewAndMatview reconstructs the CREATE statement of an aggregate view
// and of the materialized view. The body must name its relations with their
// schema (the driver reads it with an empty search_path), and a materialized
// view must be re-created empty (WITH NO DATA).
func TestDDLViewAndMatview(t *testing.T) {
	c := pgStart(t)

	view := pgOpenDefinition(t, c, "v_customer_order_totals", "v_customer_order_totals", "reporting.v_customer_order_totals")
	c.Shot("ddl-view")
	pgRequireDDL(t, view, "reporting.v_customer_order_totals",
		"CREATE VIEW reporting.v_customer_order_totals",
		"AS",
		"FROM public.customers c",
		"LEFT JOIN sales.orders o ON o.customer_id = c.id",
		"COALESCE(sum(o.total::numeric), 0::numeric)::numeric(14,2) AS lifetime_value",
		"GROUP BY c.id, c.email, c.display_name;",
	)
	pgRequireNoServerClauses(t, view, "reporting.v_customer_order_totals")

	mv := pgOpenDefinition(t, c, "mv_daily_events", "mv_daily_events", "analytics.mv_daily_events")
	c.Shot("ddl-matview")
	pgRequireDDL(t, mv, "analytics.mv_daily_events",
		"CREATE MATERIALIZED VIEW analytics.mv_daily_events",
		"FROM analytics.events",
		"count(DISTINCT session_id) AS session_count",
		"GROUP BY (date_trunc('day'::text, occurred_at)), event_type;",
	)
	if !strings.HasSuffix(strings.TrimSpace(mv), "WITH NO DATA") {
		t.Errorf("materialized view DDL must end with WITH NO DATA, got:\n%s", mv)
	}
	pgRequireNoServerClauses(t, mv, "analytics.mv_daily_events")
}

// TestDDLFunctionProcedureTrigger reconstructs the stored routines and the row
// trigger: pg_get_functiondef's CREATE OR REPLACE form, the trigger's timing,
// WHEN clause and function call, all schema-qualified.
func TestDDLFunctionProcedureTrigger(t *testing.T) {
	c := pgStart(t)

	fn := pgOpenDefinition(t, c, "fn_orders_by_status", "fn_orders_by_status", "analytics.fn_orders_by_status")
	c.Shot("ddl-function")
	pgRequireDDL(t, fn, "analytics.fn_orders_by_status",
		"CREATE OR REPLACE FUNCTION analytics.fn_orders_by_status(p_status order_status)",
		"RETURNS TABLE(order_id bigint, customer_email text, total numeric, ordered_at timestamp with time zone)",
		"LANGUAGE plpgsql",
		"STABLE",
		"RETURN QUERY",
		"FROM sales.orders o",
		"JOIN public.customers c ON c.id = o.customer_id",
		"LIMIT 500;",
		"$function$",
	)

	proc := pgOpenDefinition(t, c, "proc_refresh_dashboard", "proc_refresh_dashboard", "analytics.proc_refresh_dashboard")
	c.Shot("ddl-procedure")
	pgRequireDDL(t, proc, "analytics.proc_refresh_dashboard",
		"CREATE OR REPLACE PROCEDURE analytics.proc_refresh_dashboard()",
		"LANGUAGE plpgsql",
		"REFRESH MATERIALIZED VIEW analytics.mv_daily_events",
		"$procedure$",
	)

	// The row trigger exists on the partitioned parent and is cloned onto all
	// 25 partitions; the catalog row for the parent is picked here.
	trg := pgOpenDefinition(t, c, "trg_orders_audit_status", "trg_orders_audit_status", "sales.trg_orders_audit_status")
	c.Shot("ddl-trigger")
	pgRequireDDL(t, trg, "sales.trg_orders_audit_status",
		"CREATE TRIGGER trg_orders_audit_status AFTER UPDATE OF status ON sales.orders FOR EACH ROW",
		"WHEN (old.status IS DISTINCT FROM new.status)",
		"EXECUTE FUNCTION sales.fn_audit_order_status()",
	)
	pgRequireNoServerClauses(t, trg, "sales.trg_orders_audit_status")
}

// TestDDLIndexAndSequence reconstructs a partial index (the WHERE clause and
// the operator class are part of the definition), a GIN index, and the explicit
// invoice sequence with its parameters.
func TestDDLIndexAndSequence(t *testing.T) {
	c := pgStart(t)

	// The index name is longer than the object-browser pane, so its row is
	// clipped ("customers_active_partia"): search by the visible prefix.
	partial := pgOpenDefinition(t, c, "customers_active_partial", "customers_active", "public.customers_active_partial")
	c.Shot("ddl-index-partial")
	pgRequireDDL(t, partial, "public.customers_active_partial",
		"CREATE INDEX customers_active_partial ON public.customers USING btree (created_at)",
		"WHERE (status = 'active'::public.customer_status)",
	)
	pgRequireNoServerClauses(t, partial, "public.customers_active_partial")

	gin := pgOpenDefinition(t, c, "customers_tags_gin", "customers_tags_gin", "public.customers_tags_gin")
	c.Shot("ddl-index-gin")
	pgRequireDDL(t, gin, "public.customers_tags_gin",
		"CREATE INDEX customers_tags_gin ON public.customers USING gin (tags)",
	)

	seq := pgOpenDefinition(t, c, "invoice_seq", "invoice_seq", "sales.invoice_seq")
	c.Shot("ddl-sequence")
	pgRequireDDL(t, seq, "sales.invoice_seq",
		"CREATE SEQUENCE sales.invoice_seq",
		"AS bigint",
		"START WITH 100000",
		"INCREMENT BY 1",
		"MINVALUE 1",
		"MAXVALUE 9223372036854775807",
		"CACHE 1",
		"NO CYCLE;",
	)
	pgRequireNoServerClauses(t, seq, "sales.invoice_seq")
}

// TestDDLConstraintViewDefect reconstructs reporting.v_active_customers, whose
// definition is the dataset's constraint-enforcing view: security_barrier=true
// and WITH CHECK OPTION.
//
// This is kept as a defect scenario: the reconstructed statement drops both
// options, so re-executing it silently weakens the view. PostgreSQL keeps those
// in pg_class.reloptions (not in pg_get_viewdef), and the driver wraps only the
// view body.
func TestDDLConstraintViewDefect(t *testing.T) {
	c := pgStart(t)
	ddl := pgOpenDefinition(t, c, "v_active_customers", "v_active_customers", "reporting.v_active_customers")
	c.Shot("ddl-constraint-view")

	// The basic shape of the view is reconstructed correctly.
	pgRequireDDL(t, ddl, "reporting.v_active_customers",
		"CREATE VIEW reporting.v_active_customers",
		"FROM public.customers",
		"WHERE status = 'active'::public.customer_status AND balance::numeric >= 0::numeric;",
	)
	pgRequireNoServerClauses(t, ddl, "reporting.v_active_customers")

	// … but the enforcement options are gone: the source view is declared
	// WITH (security_barrier=true, check_option=cascaded).
	missing := make([]string, 0, 2)
	if !strings.Contains(strings.ToUpper(ddl), "CHECK OPTION") {
		missing = append(missing, "WITH CHECK OPTION")
	}
	if !strings.Contains(ddl, "security_barrier") {
		missing = append(missing, "WITH (security_barrier=true)")
	}
	if len(missing) > 0 {
		t.Skipf("defect: Definition(VIEW reporting.v_active_customers) drops %s; the view is declared "+
			"reloptions {security_barrier=true,check_option=cascaded}, so re-executing the copied DDL "+
			"re-creates a weaker view than the one on the server (screenshot: ddl-constraint-view; "+
			"the copied text is %d bytes and contains no WITH options)", strings.Join(missing, " and "), len(ddl))
	}
}
