package catalog

import "time"

// DemoCatalog returns built-in e-commerce sample metadata for offline
// demonstrations of the object browser and completion. Once real drivers are
// wired in, this data only appears on the `demo` connection.
func DemoCatalog() *Catalog {
	now := time.Now()
	return &Catalog{
		Dialect:  "demo",
		LoadedAt: now,
		Capabilities: Capable(
			KindTable, KindView, KindMaterializedView, KindFunction,
			KindProcedure, KindTrigger, KindIndex, KindSequence,
		),
		Schemas: []*Schema{
			{
				Name:      "public",
				Tables:    demoPublicTables(),
				Resources: demoPublicResources(),
			},
			{
				Name:      "reporting",
				Tables:    demoReportingTables(),
				Resources: demoReportingResources(),
			},
		},
	}
}

func demoPublicTables() []*Table {
	users := &Table{
		Schema: "public", Name: "users", Type: TypeTable, Comment: "registered users",
		Columns: []*Column{
			{Name: "id", Type: "uuid", PK: true, Comment: "primary key"},
			{Name: "email", Type: "varchar(255)", Unique: true, Comment: "login email"},
			{Name: "full_name", Type: "varchar(120)", Comment: "display name"},
			{Name: "status", Type: "user_status", Default: "'active'", Comment: "account status"},
			{Name: "country_code", Type: "char(2)", Nullable: true},
			{Name: "created_at", Type: "timestamptz", Default: "now()"},
			{Name: "last_login_at", Type: "timestamptz", Nullable: true},
		},
	}
	orders := &Table{
		Schema: "public", Name: "orders", Type: TypeTable, Comment: "orders header",
		Columns: []*Column{
			{Name: "id", Type: "bigint", PK: true},
			{Name: "user_id", Type: "uuid", Comment: "ordering user -> users.id"},
			{Name: "status", Type: "order_status", Default: "'pending'", Comment: "order status"},
			{Name: "total_amount", Type: "numeric(12,2)", Comment: "order total"},
			{Name: "currency", Type: "char(3)", Default: "'CNY'"},
			{Name: "shipping_address", Type: "jsonb", Nullable: true, Comment: "shipping address snapshot"},
			{Name: "placed_at", Type: "timestamptz", Default: "now()"},
			{Name: "cancelled_at", Type: "timestamptz", Nullable: true},
		},
	}
	orderItems := &Table{
		Schema: "public", Name: "order_items", Type: TypeTable, Comment: "order line items",
		Columns: []*Column{
			{Name: "id", Type: "bigint", PK: true},
			{Name: "order_id", Type: "bigint", Comment: "owning order -> orders.id"},
			{Name: "product_id", Type: "bigint", Comment: "product -> products.id"},
			{Name: "quantity", Type: "int"},
			{Name: "unit_price", Type: "numeric(12,2)", Comment: "unit price at order time"},
		},
	}
	products := &Table{
		Schema: "public", Name: "products", Type: TypeTable, Comment: "product",
		Columns: []*Column{
			{Name: "id", Type: "bigint", PK: true},
			{Name: "sku", Type: "varchar(64)", Unique: true},
			{Name: "name", Type: "varchar(200)"},
			{Name: "category_id", Type: "bigint", Nullable: true, Comment: "category -> categories.id"},
			{Name: "price", Type: "numeric(12,2)"},
			{Name: "stock", Type: "int", Default: "0"},
			{Name: "on_sale", Type: "boolean", Default: "false"},
			{Name: "attrs", Type: "jsonb", Nullable: true, Comment: "extended attributes"},
			{Name: "created_at", Type: "timestamptz", Default: "now()"},
		},
	}
	categories := &Table{
		Schema: "public", Name: "categories", Type: TypeTable, Comment: "product category",
		Columns: []*Column{
			{Name: "id", Type: "bigint", PK: true},
			{Name: "parent_id", Type: "bigint", Nullable: true, Comment: "parent category -> categories.id"},
			{Name: "name", Type: "varchar(100)"},
			{Name: "path", Type: "varchar(500)", Comment: "materialized path, e.g. /electronics/phones"},
		},
	}
	orderEvents := &Table{
		Schema: "public", Name: "order_events", Type: TypeTable, Comment: "order event log",
		Columns: []*Column{
			{Name: "id", Type: "bigint", PK: true},
			{Name: "order_id", Type: "bigint"},
			{Name: "event_type", Type: "varchar(50)", Comment: "created/paid/shipped/..."},
			{Name: "payload", Type: "jsonb", Nullable: true},
			{Name: "occurred_at", Type: "timestamptz", Default: "now()"},
		},
	}
	return []*Table{users, orders, orderItems, products, categories, orderEvents}
}

func demoReportingTables() []*Table {
	dailySales := &Table{
		Schema: "reporting", Name: "v_daily_sales", Type: TypeView, Comment: "daily sales totals",
		Columns: []*Column{
			{Name: "sale_date", Type: "date"},
			{Name: "order_count", Type: "bigint"},
			{Name: "gmv", Type: "numeric(16,2)", Comment: "gross merchandise value"},
			{Name: "avg_order_value", Type: "numeric(12,2)"},
		},
	}
	topProducts := &Table{
		Schema: "reporting", Name: "v_top_products", Type: TypeView, Comment: "best-selling products",
		Columns: []*Column{
			{Name: "product_id", Type: "bigint"},
			{Name: "product_name", Type: "varchar(200)"},
			{Name: "units_sold", Type: "bigint"},
			{Name: "revenue", Type: "numeric(16,2)"},
		},
	}
	churnRisk := &Table{
		Schema: "reporting", Name: "v_churn_risk", Type: TypeView, Comment: "users at risk of churn",
		Columns: []*Column{
			{Name: "user_id", Type: "uuid"},
			{Name: "last_order_at", Type: "timestamptz"},
			{Name: "days_since_order", Type: "int"},
			{Name: "risk_level", Type: "varchar(16)"},
		},
	}
	dailyRollup := &Table{
		Schema: "reporting", Name: "mv_sales_daily", Type: TypeMaterializedView, Comment: "cached sales rollup",
		Columns: []*Column{
			{Name: "sale_date", Type: "date"},
			{Name: "order_count", Type: "bigint"},
			{Name: "gmv", Type: "numeric(16,2)"},
		},
	}
	return []*Table{dailySales, topProducts, churnRisk, dailyRollup}
}

// demoPublicResources exercises every non-relation resource kind the UI knows
// about. A real driver fills only the kinds its database supports.
func demoPublicResources() []*Resource {
	return []*Resource{
		{Kind: KindFunction, Schema: "public", Name: "order_total",
			Detail: "(order_id bigint) → numeric", Comment: "sum of an order's line items"},
		{Kind: KindFunction, Schema: "public", Name: "user_status_label",
			Detail: "(status user_status) → text", Comment: "human-readable status"},
		{Kind: KindProcedure, Schema: "public", Name: "archive_orders",
			Detail: "(before date)", Comment: "move old orders to the archive"},
		{Kind: KindTrigger, Schema: "public", Name: "orders_touch_updated",
			Owner: "orders", Detail: "BEFORE UPDATE", Comment: "maintain updated_at"},
		{Kind: KindTrigger, Schema: "public", Name: "users_audit",
			Owner: "users", Detail: "AFTER INSERT OR UPDATE", Comment: "write an audit event"},
		{Kind: KindIndex, Schema: "public", Name: "idx_orders_user_id",
			Owner: "orders", Detail: "user_id", Comment: "orders by customer"},
		{Kind: KindIndex, Schema: "public", Name: "users_email_key",
			Owner: "users", Detail: "email · unique", Comment: "login lookup"},
		{Kind: KindSequence, Schema: "public", Name: "order_number_seq",
			Comment: "human-readable order numbers"},
	}
}

func demoReportingResources() []*Resource {
	return []*Resource{
		{Kind: KindFunction, Schema: "reporting", Name: "refresh_daily_sales",
			Detail: "() → void", Comment: "refresh the materialized view"},
	}
}
