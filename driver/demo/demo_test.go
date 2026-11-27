package demo

import (
	"context"
	"strings"
	"testing"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

// TestDefinitionCoversEveryObjectKind checks that the demo connection can
// describe each of the eight object kinds its sample catalog shows — this is
// what makes the object browser's "View definition" action explorable
// offline, including a stored function, which SQLite cannot provide.
func TestDefinitionCoversEveryObjectKind(t *testing.T) {
	conn, err := factory{}.Open(context.Background(), driver.Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	prov, ok := conn.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the demo connection must implement driver.DefinitionProvider")
	}
	ctx := context.Background()

	cases := []struct {
		ref  driver.DefinitionRef
		want string
	}{
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: "users"}, "CREATE TABLE public.users"},
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: "orders"}, "CREATE TABLE public.orders"},
		{driver.DefinitionRef{Kind: catalog.KindView, Schema: "reporting", Name: "v_daily_sales"}, "CREATE VIEW reporting.v_daily_sales"},
		{driver.DefinitionRef{Kind: catalog.KindMaterializedView, Schema: "reporting", Name: "mv_sales_daily"}, "CREATE MATERIALIZED VIEW reporting.mv_sales_daily"},
		{driver.DefinitionRef{Kind: catalog.KindFunction, Schema: "public", Name: "order_total"}, "CREATE FUNCTION public.order_total(order_id bigint)"},
		{driver.DefinitionRef{Kind: catalog.KindFunction, Schema: "reporting", Name: "refresh_daily_sales"}, "CREATE PROCEDURE reporting.refresh_daily_sales"},
		{driver.DefinitionRef{Kind: catalog.KindProcedure, Schema: "public", Name: "archive_orders"}, "CREATE PROCEDURE public.archive_orders"},
		{driver.DefinitionRef{Kind: catalog.KindTrigger, Schema: "public", Name: "users_audit", Owner: "users"}, "CREATE TRIGGER users_audit AFTER INSERT OR UPDATE ON public.users"},
		{driver.DefinitionRef{Kind: catalog.KindIndex, Schema: "public", Name: "idx_orders_user_id", Owner: "orders"}, "CREATE INDEX idx_orders_user_id ON public.orders (user_id)"},
		{driver.DefinitionRef{Kind: catalog.KindIndex, Schema: "public", Name: "users_email_key", Owner: "users"}, "CREATE UNIQUE INDEX users_email_key ON public.users (email)"},
		{driver.DefinitionRef{Kind: catalog.KindSequence, Schema: "public", Name: "order_number_seq"}, "CREATE SEQUENCE public.order_number_seq"},
	}
	for _, tc := range cases {
		ddl, err := prov.Definition(ctx, tc.ref)
		if err != nil {
			t.Errorf("Definition(%s %s.%s): %v", tc.ref.Kind, tc.ref.Schema, tc.ref.Name, err)
			continue
		}
		if !strings.Contains(ddl, tc.want) {
			t.Errorf("Definition(%s %s.%s) = %q, want it to contain %q",
				tc.ref.Kind, tc.ref.Schema, tc.ref.Name, ddl, tc.want)
		}
	}

	// An unknown object and a closed connection both fail with a clear error.
	_, err = prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: "nope"})
	if err == nil || !strings.Contains(err.Error(), "no definition") {
		t.Errorf("an unknown object must report that it has no definition, got %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: "users"}); err == nil {
		t.Error("a closed connection must refuse to fetch definitions")
	}
}

// TestDemoTableDDL checks the generated CREATE TABLE: columns with their
// NOT NULL and defaults, and the primary key constraint.
func TestDemoTableDDL(t *testing.T) {
	tbl := catalog.DemoCatalog().FindTable("public.users")
	if tbl == nil {
		t.Fatal("the demo catalog must contain public.users")
	}
	ddl := demoTableDDL(tbl)
	for _, want := range []string{
		"CREATE TABLE public.users (",
		"    id uuid NOT NULL",
		"    email varchar(255) NOT NULL",
		"    status user_status NOT NULL DEFAULT 'active'",
		"    country_code char(2)",
		"    CONSTRAINT users_pkey PRIMARY KEY (id)",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("demoTableDDL(users) = %q, want it to contain %q", ddl, want)
		}
	}
	// A nullable column carries no NOT NULL.
	if strings.Contains(ddl, "country_code char(2) NOT NULL") {
		t.Errorf("country_code is nullable, its line must not say NOT NULL:\n%s", ddl)
	}
}
