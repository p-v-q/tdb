// gen_sqlite builds the SQLite integration-test datasets for TDB.
//
//	gen_sqlite.exe -outdir C:\path\to\.dbs\sqlite           # (re)build shop.db + weird.db
//	gen_sqlite.exe -outdir C:\path\to\.dbs\sqlite -verify   # only print row counts
//
// It is self-contained and does not depend on TDB source code.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	outDir     = flag.String("outdir", ".", "directory that receives shop.db and weird.db")
	verifyOnly = flag.Bool("verify", false, "do not build; only verify row counts of existing databases")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen_sqlite:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	if *verifyOnly {
		return verify(*outDir)
	}
	shop := filepath.Join(*outDir, "shop.db")
	weird := filepath.Join(*outDir, "weird.db")
	for _, p := range []string{shop, weird} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	start := time.Now()
	if err := buildShop(shop); err != nil {
		return fmt.Errorf("shop.db: %w", err)
	}
	fmt.Printf("shop.db built in %s\n", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	if err := buildWeird(weird); err != nil {
		return fmt.Errorf("weird.db: %w", err)
	}
	fmt.Printf("weird.db built in %s\n", time.Since(start).Round(time.Millisecond))
	return verify(*outDir)
}

func open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=OFF",
		"PRAGMA synchronous=OFF",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA cache_size=-200000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=10000",
	} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return db, nil
}

func execScript(db *sql.DB, script string) error {
	// database/sql's Exec on modernc.org/sqlite accepts multiple statements.
	if _, err := db.Exec(script); err != nil {
		return fmt.Errorf("ddl: %w", err)
	}
	return nil
}

// insertChunked inserts total rows in multi-row chunks to keep statement counts low.
func insertChunked(tx *sql.Tx, table string, cols []string, total int, row func(i int) []any) error {
	maxParams := 900
	colList := strings.Join(cols, ",")
	single := "(" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ")"
	chunk := maxParams / len(cols)
	if chunk < 1 {
		chunk = 1
	}
	for start := 0; start < total; start += chunk {
		end := start + chunk
		if end > total {
			end = total
		}
		var b strings.Builder
		b.WriteString("INSERT INTO " + table + " (" + colList + ") VALUES ")
		args := make([]any, 0, (end-start)*len(cols))
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			b.WriteString(single)
			args = append(args, row(i)...)
		}
		if _, err := tx.Exec(b.String(), args...); err != nil {
			return fmt.Errorf("%s rows %d-%d: %w", table, start, end, err)
		}
	}
	return nil
}

var firstNames = []string{"Alice", "Bob", "Chloé", "王伟", "李娜", "José", "Yuki", "Priya", "Мюллер", "O'Brien",
	"Ahmed", "Fatima", "Søren", "Nguyễn", "Asha", "Diego", "Mei", "Ivan", "Leila", "Tomás"}
var lastNames = []string{"Smith", "Johnson", "张", "李", "García", "Müller", "Sato", "Patel", "陈", "O'Brien",
	"Kim", "Rossi", "Dubois", "Ivanov", "Tanaka", "Silva", "Ng", "Hernández", "Kowalski", "Brown"}
var countries = []string{"US", "CN", "DE", "FR", "JP", "BR", "IN", "GB", "ES", "CA"}
var statuses = []string{"active", "active", "active", "vip", "inactive", "churned"}
var categories = []string{"electronics", "clothing", "books", "home", "sports", "toys", "food", "beauty"}
var prodNames = []string{"Widget", "Gadget", "Gizmo", "Doohickey", "Thingamajig", "Sprocket", "量子笔记本", "Emoji Mug 😀"}
var orderStatuses = []string{"pending", "paid", "shipped", "delivered", "cancelled", "refunded"}
var eventTypes = []string{"page_view", "add_to_cart", "purchase", "search", "login", "signup"}
var cities = []string{"Springfield", "上海", "Tokyo", "München", "São Paulo", "Mumbai"}

func ts(i int) string {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(i) * time.Minute).Format("2006-01-02 15:04:05")
}

func buildShop(path string) error {
	db, err := open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, p := range []string{"PRAGMA journal_mode=OFF", "PRAGMA synchronous=OFF", "PRAGMA foreign_keys=ON"} {
		if _, err := db.Exec(p); err != nil {
			return err
		}
	}
	if err := execScript(db, shopSchema); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// users: 100k
	if err := insertChunked(tx, "users", []string{
		"id", "uuid", "email", "first_name", "last_name", "full_name", "status", "balance",
		"tags", "metadata", "avatar", "country", "created_at", "last_login_at",
	}, 100000, func(i int) []any {
		n := i + 1
		first := firstNames[i%len(firstNames)]
		last := lastNames[(i/3)%len(lastNames)]
		var avatar any
		if i%10 == 0 {
			avatar = []byte(fmt.Sprintf("avatar-%d", n))
		}
		var login any
		if i%7 != 0 {
			login = ts(i*37 + 1440)
		}
		return []any{
			n,
			fmt.Sprintf("00000000-0000-4000-8000-%012d", n),
			fmt.Sprintf("user%d@example.com", n),
			first, last, strings.ToUpper(first + " " + last),
			statuses[i%len(statuses)],
			float64((i*73)%100000) / 100.0,
			fmt.Sprintf(`["tag%d","tier%d"]`, i%50, i%5),
			fmt.Sprintf(`{"signup_source":"%s","score":%d,"vip":%t}`,
				[]string{"web", "ios", "android", "referral"}[i%4], i%100, i%17 == 0),
			avatar,
			countries[i%len(countries)],
			ts(i * 7),
			login,
		}
	}); err != nil {
		return err
	}

	// products: 5k
	if err := insertChunked(tx, "products", []string{
		"id", "sku", "name", "description", "category", "price", "cost", "stock",
		"is_active", "tags", "attributes", "image", "created_at",
	}, 5000, func(i int) []any {
		n := i + 1
		var image any
		if i%10 == 0 {
			image = []byte(fmt.Sprintf("image-bytes-%d", n))
		}
		desc := fmt.Sprintf("Product %d description with lorem ipsum dolor sit amet", n)
		if i%13 == 0 {
			desc = fmt.Sprintf("Product %d — 高质量商品 🚀 limited edition", n)
		}
		return []any{
			n,
			fmt.Sprintf("SKU-%07d", n),
			fmt.Sprintf("%s %d", prodNames[i%len(prodNames)], n),
			desc,
			categories[i%len(categories)],
			float64((i*7)%90000+100) / 100.0,
			float64((i*5)%60000+50) / 100.0,
			i % 500,
			i%11 != 0,
			fmt.Sprintf(`["cat%d","brand%d"]`, i%8, i%20),
			fmt.Sprintf(`{"color":"%s","weight_g":%d,"battery":%t}`,
				[]string{"red", "blue", "green", "black", "white", "银色"}[i%6], i%5000, i%3 == 0),
			image,
			ts(i * 11),
		}
	}); err != nil {
		return err
	}

	// orders: 200k
	if err := insertChunked(tx, "orders", []string{
		"id", "user_id", "order_no", "status", "total", "currency", "ordered_at",
		"shipping_address", "notes", "created_at",
	}, 200000, func(i int) []any {
		n := i + 1
		var notes any
		switch {
		case i%100 == 0:
			notes = "礼盒包装 🎁 请小心轻放"
		case i%37 == 0:
			notes = "Leave at the front door"
		}
		return []any{
			n,
			1 + (i*37)%100000,
			fmt.Sprintf("ORD-%010d", n),
			orderStatuses[i%len(orderStatuses)],
			float64((i*7)%500000+500) / 100.0,
			[]string{"USD", "USD", "USD", "EUR", "CNY", "JPY"}[i%6],
			ts(i * 13),
			fmt.Sprintf(`{"line1":"%d Main Street","city":"%s","country":"%s","postal":"%05d"}`,
				i%300, cities[i%len(cities)], countries[i%len(countries)], (i*13)%99999),
			notes,
			ts(i*13 + 5),
		}
	}); err != nil {
		return err
	}

	// order_items: 300k (600k total across users/orders/items)
	if err := insertChunked(tx, "order_items", []string{
		"id", "order_id", "product_id", "quantity", "unit_price", "discount",
	}, 300000, func(i int) []any {
		n := i + 1
		return []any{
			n,
			1 + i%200000,
			1 + (i*7)%5000,
			1 + i%10,
			float64((i*13)%90000+100) / 100.0,
			float64((i%5)*2) / 1.0,
		}
	}); err != nil {
		return err
	}

	// events: 20k
	if err := insertChunked(tx, "events", []string{
		"id", "occurred_at", "session_id", "event_type", "user_id", "payload",
	}, 20000, func(i int) []any {
		n := i + 1
		var uid any
		if i%5 != 0 {
			uid = 1 + (i*17)%100000
		}
		return []any{
			n,
			ts(i * 3),
			fmt.Sprintf("10000000-0000-4000-8000-%012d", n),
			eventTypes[i%len(eventTypes)],
			uid,
			fmt.Sprintf(`{"path":"%s","duration_ms":%d,"device":{"os":"%s"},"props":{"product_id":%d}}`,
				[]string{"/", "/products", "/cart", "/checkout"}[i%4], (i*13)%60000,
				[]string{"ios", "android", "windows", "macos"}[i%4], 1+i%5000),
		}
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Secondary indexes, views and trigger (after bulk load).
	if err := execScript(db, shopPostDDL); err != nil {
		return err
	}
	// Fire the audit trigger.
	if _, err := db.Exec(`UPDATE orders SET status = 'shipped' WHERE id <= 20 AND status <> 'shipped'`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE orders SET status = 'delivered' WHERE id BETWEEN 21 AND 40 AND status <> 'delivered'`); err != nil {
		return err
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	return nil
}

const shopSchema = `
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    uuid          TEXT NOT NULL,
    email         TEXT NOT NULL,
    first_name    TEXT NOT NULL,
    last_name     TEXT NOT NULL,
    full_name     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','vip','churned')),
    balance       REAL NOT NULL DEFAULT 0.0 CHECK (balance >= 0),
    tags          TEXT NOT NULL DEFAULT '[]',
    metadata      TEXT NOT NULL DEFAULT '{}',
    avatar        BLOB,
    country       TEXT NOT NULL DEFAULT 'US',
    created_at    TEXT NOT NULL,
    last_login_at TEXT
);

CREATE TABLE products (
    id          INTEGER PRIMARY KEY,
    sku         TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    category    TEXT NOT NULL,
    price       REAL NOT NULL CHECK (price >= 0),
    cost        REAL,
    stock       INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0),
    is_active   INTEGER NOT NULL DEFAULT 1,
    tags        TEXT,
    attributes  TEXT,
    image       BLOB,
    created_at  TEXT NOT NULL
);

CREATE TABLE orders (
    id               INTEGER PRIMARY KEY,
    user_id          INTEGER NOT NULL REFERENCES users(id),
    order_no         TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    total            REAL NOT NULL DEFAULT 0 CHECK (total >= 0),
    currency         TEXT NOT NULL DEFAULT 'USD',
    ordered_at       TEXT NOT NULL,
    shipping_address TEXT,
    notes            TEXT,
    created_at       TEXT NOT NULL
);

CREATE TABLE order_items (
    id         INTEGER PRIMARY KEY,
    order_id   INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id INTEGER NOT NULL REFERENCES products(id),
    quantity   INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 50),
    unit_price REAL NOT NULL CHECK (unit_price >= 0),
    discount   REAL NOT NULL DEFAULT 0 CHECK (discount BETWEEN 0 AND 100),
    line_total REAL GENERATED ALWAYS AS (round(quantity * unit_price * (1 - discount / 100.0), 2)) STORED
);

CREATE TABLE events (
    id          INTEGER PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    payload     TEXT
);

CREATE TABLE order_audit (
    audit_id   INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id   INTEGER NOT NULL,
    old_status TEXT,
    new_status TEXT,
    changed_at TEXT NOT NULL DEFAULT (datetime('now')),
    changed_by TEXT NOT NULL DEFAULT 'system'
);
`

const shopPostDDL = `
CREATE UNIQUE INDEX ux_users_email ON users(email);
CREATE INDEX ix_users_status_created ON users(status, created_at);
CREATE INDEX ix_users_country ON users(country);
CREATE INDEX ix_products_category_price ON products(category, price);
CREATE UNIQUE INDEX ux_orders_order_no ON orders(order_no);
CREATE INDEX ix_orders_user_ordered ON orders(user_id, ordered_at);
CREATE INDEX ix_orders_status ON orders(status, ordered_at);
CREATE INDEX ix_orders_paid ON orders(ordered_at) WHERE status IN ('paid','shipped','delivered');
CREATE INDEX ix_items_order ON order_items(order_id);
CREATE INDEX ix_items_product ON order_items(product_id);
CREATE INDEX ix_items_total ON order_items(line_total) WHERE line_total > 1000;
CREATE INDEX ix_events_time ON events(occurred_at);
CREATE INDEX ix_events_type ON events(event_type, occurred_at);

CREATE VIEW v_user_order_summary AS
SELECT u.id, u.email, u.full_name, u.status,
       COUNT(o.id) AS order_count,
       COALESCE(SUM(o.total), 0) AS lifetime_value
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id, u.email, u.full_name, u.status;

CREATE VIEW v_product_sales AS
SELECT p.id, p.sku, p.name, p.category,
       SUM(oi.quantity) AS units_sold,
       SUM(oi.line_total) AS revenue
FROM products p
JOIN order_items oi ON oi.product_id = p.id
GROUP BY p.id, p.sku, p.name, p.category;

CREATE VIEW v_order_totals AS
SELECT o.id, o.order_no, o.status, o.total,
       COUNT(oi.id) AS line_count,
       COALESCE(SUM(oi.line_total), 0) AS lines_total
FROM orders o
LEFT JOIN order_items oi ON oi.order_id = o.id
GROUP BY o.id, o.order_no, o.status, o.total;

CREATE TRIGGER trg_order_audit_status
AFTER UPDATE OF status ON orders
FOR EACH ROW
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO order_audit (order_id, old_status, new_status, changed_by)
    VALUES (NEW.id, OLD.status, NEW.status, 'trigger');
END;
`

func buildWeird(path string) error {
	db, err := open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := execScript(db, weirdSchema); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Widely varied types.
	rows := [][]any{
		{1, int64(42), 3.14159, "text value", []byte{0xDE, 0xAD, 0xBE, 0xEF}, "2024-01-02 03:04:05"},
		{2, nil, nil, nil, nil, nil},
		{3, int64(-1), -0.5, "unicode 你好 😀", []byte("raw-bytes"), nil},
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO "types test" (id, int_col, real_col, text_col, blob_col, "Date Col") VALUES (?,?,?,?,?,?)`, r...); err != nil {
			return err
		}
	}

	// Table and column names that need quoting.
	if _, err := tx.Exec(`INSERT INTO "select" ("from", "where", "group", "order") VALUES (1, 'upstream', 'active', x'00ff')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "order" ("select", "from", "where") VALUES (1, 'downstream', 'inactive')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "weird table with spaces" ("First Name", "full name", "col-with-dash", "  leading") VALUES ('Alice', 'Alice Smith', 'dash', 'lead')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "UPPERCASE" ("ID", "NAME", "MixedCase") VALUES (1, 'UPPER', 'mixed')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "表" ("键", "值") VALUES (1, '中文标识符')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "nulls" DEFAULT VALUES`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "nulls" DEFAULT VALUES`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "autoinc" ("name") VALUES ('a'), ('b')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "gen_col" (a, b) VALUES (2, 3.0), (10, 0.5)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "no_rowid" (k, v) VALUES ('k1', 10), ('k2', 20)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "strict_t" (id, name, amount) VALUES (1, 'strict row', 1.5)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO "json_docs" (id, doc) VALUES (1, json('{"a":1}')), (2, json('[1,2,3]'))`); err != nil {
		return err
	}
	// 1MB text, one 1MB single line, and a 1MB blob.
	longLine := strings.Repeat("0123456789", 100000)
	if _, err := tx.Exec(`INSERT INTO "big text" (id, label, content, long_line, binary_data) VALUES (1, 'one-megabyte', ?, ?, ?)`,
		strings.Repeat("A", 1048576), longLine, []byte(strings.Repeat("ab", 524288))); err != nil {
		return err
	}
	unicodeRows := [][2]string{
		{"chinese", "你好，世界！数据库测试"},
		{"emoji", "🚀🎉😀🔥👍"},
		{"family-zwj", "👨‍👩‍👧‍👦"},
		{"japanese", "こんにちは世界、テストです"},
		{"korean", "안녕하세요 세계"},
		{"arabic", "مرحبا بالعالم"},
		{"hebrew", "שלום עולם"},
		{"cyrillic", "Привет, мир"},
		{"accented", "Château Müller café naïve"},
		{"combining", "e\u0301 a\u0308"},
		{"zero-width", "ab\u200bcd"},
		{"fullwidth", "ＡＢＣ１２３"},
		{"mixed", "Mixed 混合 текст 🎌"},
		{"newline-tab", "line1\nline2\ttabbed"},
		{"math", "∑ ∫ √ π ≈ ∞"},
	}
	for i, r := range unicodeRows {
		if _, err := tx.Exec(`INSERT INTO "unicode" (id, category, phrase) VALUES (?,?,?)`, i+1, r[0], r[1]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO "audit_target" (id, name, state) VALUES (1, 'row one', 'new')`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Fire weird trigger.
	if _, err := db.Exec(`UPDATE "audit_target" SET state = 'done' WHERE id = 1`); err != nil {
		return err
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		return err
	}
	return db.Close()
}

const weirdSchema = `
CREATE TABLE "types test" (
    id        INTEGER PRIMARY KEY,
    int_col   INTEGER,
    real_col  REAL,
    text_col  TEXT,
    blob_col  BLOB,
    "Date Col" TEXT
);

CREATE TABLE "select" (
    "from"  INTEGER PRIMARY KEY,
    "where" TEXT,
    "group" TEXT,
    "order" BLOB
);

CREATE TABLE "order" (
    "select" INTEGER PRIMARY KEY,
    "from"   TEXT,
    "where"  TEXT,
    "rank"   INTEGER
);

CREATE TABLE "weird table with spaces" (
    "First Name"        TEXT,
    "full name"         TEXT,
    "col-with-dash"     TEXT,
    "  leading"         TEXT,
    "trailing  "        TEXT
);

CREATE TABLE "UPPERCASE" (
    "ID"        INTEGER PRIMARY KEY,
    "NAME"      TEXT,
    "MixedCase" TEXT
);

CREATE TABLE "表" (
    "键" INTEGER PRIMARY KEY,
    "值" TEXT
);

CREATE TABLE "empty table" (
    id    INTEGER PRIMARY KEY,
    label TEXT
);

CREATE TABLE "nulls" (
    a TEXT,
    b INTEGER,
    c REAL,
    d BLOB
);

CREATE TABLE "autoinc" (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT
);

CREATE TABLE "gen_col" (
    id            INTEGER PRIMARY KEY,
    a             INTEGER,
    b             REAL,
    sum_stored    INTEGER GENERATED ALWAYS AS (a + 10) STORED,
    product       REAL GENERATED ALWAYS AS (a * b) VIRTUAL
);

CREATE TABLE "no_rowid" (
    k TEXT PRIMARY KEY,
    v INTEGER
) WITHOUT ROWID;

CREATE TABLE "strict_t" (
    id     INTEGER PRIMARY KEY,
    name   TEXT NOT NULL,
    amount REAL NOT NULL
) STRICT;

CREATE TABLE "json_docs" (
    id  INTEGER PRIMARY KEY,
    doc TEXT CHECK (json_valid(doc))
);

CREATE TABLE "big text" (
    id          INTEGER PRIMARY KEY,
    label       TEXT,
    content     TEXT,
    long_line   TEXT,
    binary_data BLOB
);

CREATE TABLE "unicode" (
    id       INTEGER PRIMARY KEY,
    category TEXT NOT NULL,
    phrase   TEXT NOT NULL
);

CREATE TABLE "audit_target" (
    id    INTEGER PRIMARY KEY,
    name  TEXT,
    state TEXT
);

CREATE TABLE "audit_log" (
    log_id     INTEGER PRIMARY KEY AUTOINCREMENT,
    target_id  INTEGER,
    old_state  TEXT,
    new_state  TEXT,
    changed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE "wide_40" (
    id INTEGER PRIMARY KEY,
    c01 INTEGER, c02 INTEGER, c03 INTEGER, c04 INTEGER, c05 INTEGER,
    c06 INTEGER, c07 INTEGER, c08 INTEGER, c09 INTEGER, c10 INTEGER,
    c11 TEXT, c12 TEXT, c13 TEXT, c14 TEXT, c15 TEXT,
    c16 TEXT, c17 TEXT, c18 TEXT, c19 TEXT, c20 TEXT,
    c21 REAL, c22 REAL, c23 REAL, c24 REAL, c25 REAL,
    c26 REAL, c27 REAL, c28 REAL, c29 REAL, c30 REAL,
    c31 BLOB, c32 BLOB, c33 BLOB, c34 BLOB, c35 BLOB,
    c36 NUMERIC, c37 NUMERIC, c38 NUMERIC, c39 NUMERIC, c40 NUMERIC
);

CREATE INDEX ix_types_text ON "types test"(text_col);
CREATE INDEX ix_unicode_category ON "unicode"(category);
CREATE INDEX ix_big_text_label ON "big text"(label);
CREATE UNIQUE INDEX ux_uppercase_name ON "UPPERCASE"("NAME");

CREATE VIEW "weird view" AS
SELECT t.id, t.text_col, u.category, u.phrase
FROM "types test" t
LEFT JOIN "unicode" u ON u.id = t.id;

CREATE TRIGGER trg_weird_audit
AFTER UPDATE ON "audit_target"
FOR EACH ROW
BEGIN
    INSERT INTO "audit_log" (target_id, old_state, new_state)
    VALUES (NEW.id, OLD.state, NEW.state);
END;
`

// verify opens the databases and prints machine-readable "object<TAB>rows" lines.
func verify(dir string) error {
	shopPath := filepath.Join(dir, "shop.db")
	weirdPath := filepath.Join(dir, "weird.db")
	shopCounts := []string{
		"users", "products", "orders", "order_items", "events", "order_audit",
		"v_user_order_summary", "v_product_sales", "v_order_totals",
	}
	weirdCounts := []string{
		`"types test"`, `"select"`, `"order"`, `"weird table with spaces"`, `"UPPERCASE"`,
		`"表"`, `"empty table"`, `"nulls"`, `"autoinc"`, `"gen_col"`, `"no_rowid"`,
		`"strict_t"`, `"json_docs"`, `"big text"`, `"unicode"`, `"audit_target"`,
		`"audit_log"`, `"wide_40"`, `"weird view"`,
	}
	db, err := sql.Open("sqlite", shopPath)
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Printf("shop.db\t%s\n", shopPath)
	for _, t := range shopCounts {
		var n int64
		if err := db.QueryRow(`SELECT count(*) FROM "` + t + `"`).Scan(&n); err != nil {
			return fmt.Errorf("shop.db %s: %w", t, err)
		}
		fmt.Printf("shop.%s\t%d\n", t, n)
	}
	var unicodeOK int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE first_name IN ('王伟','李娜') OR last_name IN ('张','李','陈')`).Scan(&unicodeOK); err != nil {
		return err
	}
	fmt.Printf("shop.unicode_users_cjk\t%d\n", unicodeOK)
	if unicodeOK == 0 {
		return fmt.Errorf("shop.db: CJK names did not round-trip")
	}
	var emoji int
	if err := db.QueryRow(`SELECT count(*) FROM products WHERE description LIKE '%🚀%'`).Scan(&emoji); err != nil {
		return err
	}
	fmt.Printf("shop.unicode_products_emoji\t%d\n", emoji)
	db.Close()

	wdb, err := sql.Open("sqlite", weirdPath)
	if err != nil {
		return err
	}
	defer wdb.Close()
	fmt.Printf("weird.db\t%s\n", weirdPath)
	for _, t := range weirdCounts {
		var n int64
		if err := wdb.QueryRow(`SELECT count(*) FROM ` + t + ``).Scan(&n); err != nil {
			return fmt.Errorf("weird.db %s: %w", t, err)
		}
		fmt.Printf("weird.%s\t%d\n", strings.Trim(t, `"`), n)
	}
	for _, check := range []struct {
		label string
		query string
		want  string
	}{
		{"unicode_roundtrip", `SELECT phrase FROM "unicode" WHERE category = 'emoji'`, "🚀🎉😀🔥👍"},
		{"unicode_cjk", `SELECT phrase FROM "unicode" WHERE category = 'chinese'`, "你好，世界！数据库测试"},
		{"generated_stored", `SELECT sum_stored || ':' || CAST(product AS INT) FROM "gen_col" WHERE a = 2`, "12:6"},
		{"trigger_audit", `SELECT count(*) FROM "audit_log" WHERE old_state = 'new' AND new_state = 'done'`, "1"},
	} {
		var got string
		if err := wdb.QueryRow(check.query).Scan(&got); err != nil {
			return fmt.Errorf("weird.db %s: %w", check.label, err)
		}
		if got != check.want {
			return fmt.Errorf("weird.db %s: got %q want %q", check.label, got, check.want)
		}
		fmt.Printf("weird.check.%s\tOK\n", check.label)
	}
	var bigLen int64
	if err := wdb.QueryRow(`SELECT length(content) FROM "big text" WHERE id = 1`).Scan(&bigLen); err != nil {
		return fmt.Errorf("weird.db big text: %w", err)
	}
	if bigLen != 1048576 {
		return fmt.Errorf("weird.db big text length = %d, want 1048576", bigLen)
	}
	fmt.Printf("weird.check.big_text_length\t%d\n", bigLen)
	return nil
}
