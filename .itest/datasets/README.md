# TDB integration-test datasets

Realistic, large and deliberately awkward datasets for exercising TDB (the terminal
database client in `C:\Users\qxwxi\repos\tdb`) against every supported backend.

Everything here is **idempotent and repeatable**: `load.ps1` drops and recreates its
schemas/databases, so it can be run at any time and always produces the same data.

| Backend | Version | Address | Server state |
|---|---|---|---|
| PostgreSQL | 17.6 | `127.0.0.1:55432` | already running; `postgres` (trust), `tdb`/`tdbpass` superuser |
| MySQL | 8.4.4 | `127.0.0.1:53306` | already running; `root` (no password), `tdb`/`tdbpass` |
| MariaDB | 11.4.4 | `127.0.0.1:53307` | already running; `root`/`tdbpass`, `tdb`/`tdbpass` |
| SQLite | modernc.org/sqlite v1.60.1 (Go, pure) | files under `.dbs\sqlite\` | built by `gen_sqlite` |

## Quick start

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force

# Full idempotent reload of every dataset + verification (~4.5 minutes)
C:\Users\qxwxi\repos\tdb\.itest\datasets\load.ps1

# Verification only (no reload, ~10 seconds)
C:\Users\qxwxi\repos\tdb\.itest\datasets\load.ps1 -VerifyOnly
```

`load.ps1` prints a **row-count verification table**, an **acceptance-check table**
(PASS/FAIL for every required feature) and a per-step **timing table**, and writes the
whole transcript to `C:\Users\qxwxi\repos\tdb\.artifacts\dataset-load.log`.
It exits `0` only when every acceptance check passes.

Three consecutive end-to-end runs (each one reloading over the previous state, proving
idempotency) ended **STATUS PASS** with identical row counts. The most recent run took
**333.8 s** (~5.6 min):

| Step | Seconds |
|---|---|
| Pre-flight (roles/databases/users) | 0.62 |
| PostgreSQL shop | 204.68 |
| PostgreSQL legacy | 0.50 |
| MySQL shop | 83.30 |
| MySQL legacy | 1.69 |
| MariaDB shop | 19.95 |
| MariaDB legacy | 0.96 |
| SQLite build (2 DBs) | 9.64 |

## Files

| File | Purpose |
|---|---|
| `load.ps1` | Single idempotent loader + verifier (all backends). |
| `pg_shop.sql` | PostgreSQL `shop` database (schemas, bulk data, views, MV, routines, trigger). |
| `pg_legacy.sql` | PostgreSQL `legacy` database (12 messy schemas). |
| `mysql_shop.sql` | MySQL `shop` database (+ helper tables, dropped at end). |
| `mysql_legacy.sql` | MySQL `legacy` database + 6 `legacy_*` side databases. |
| `mariadb_shop.sql` | MariaDB `shop` database (system versioning, SEQUENCE, DEFINER routines). |
| `mariadb_legacy.sql` | MariaDB `legacy` database + 6 `legacy_*` side databases. |
| `gen_sqlite\` | Self-contained Go module that builds `shop.db` and `weird.db`. |

SQL files are UTF-8 **without** BOM (read directly by the DB clients). `load.ps1` is UTF-8
**with** BOM so Windows PowerShell 5.1 decodes its Unicode literals correctly.

---

## Connections usable by TDB

TDB accepts URL-style DSNs (`postgres://…`, `mysql://…`, `mariadb://…`), driver-native
DSNs, or `-d` + host/port/user/password/database fields. These are the exact verified
values for this machine:

### PostgreSQL

| Database | TDB command |
|---|---|
| `shop` | `.\bin\tdb -d postgres -dsn 'postgres://tdb:tdbpass@127.0.0.1:55432/shop?sslmode=disable'` |
| `legacy` | `.\bin\tdb -d postgres -dsn 'postgres://tdb:tdbpass@127.0.0.1:55432/legacy?sslmode=disable'` |
| admin (`postgres`) | `postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable` (trust auth, no password) |

Test-harness variable: `TDB_POSTGRES_DSN='postgres://tdb:tdbpass@127.0.0.1:55432/shop?sslmode=disable'`

### MySQL

| Database | TDB command |
|---|---|
| `shop` | `.\bin\tdb -d mysql -dsn 'mysql://tdb:tdbpass@127.0.0.1:53306/shop'` |
| `legacy` | `.\bin\tdb -d mysql -dsn 'mysql://tdb:tdbpass@127.0.0.1:53306/legacy'` |
| `legacy_2019`, `legacy_2020`, `legacy_eu`, `legacy_us`, `legacy_apac`, `legacy_archive` | `mysql://tdb:tdbpass@127.0.0.1:53306/<db>` |
| driver-native form | `tdb:tdbpass@tcp(127.0.0.1:53306)/shop?charset=utf8mb4&parseTime=true` |
| admin | `mysql://root@127.0.0.1:53306/` (empty password) |

Test-harness variable: `TDB_MYSQL_DSN='tdb:tdbpass@tcp(127.0.0.1:53306)/shop?charset=utf8mb4&parseTime=true'`

### MariaDB

| Database | TDB command |
|---|---|
| `shop` | `.\bin\tdb -d mysql -dsn 'mysql://tdb:tdbpass@127.0.0.1:53307/shop'` (optionally `mariadb://…`) |
| `legacy` | `.\bin\tdb -d mysql -dsn 'mysql://tdb:tdbpass@127.0.0.1:53307/legacy'` |
| `legacy_2019`, `legacy_2020`, `legacy_eu`, `legacy_us`, `legacy_apac`, `legacy_archive` | `mysql://tdb:tdbpass@127.0.0.1:53307/<db>` |
| admin | `mysql://root:tdbpass@127.0.0.1:53307/` |

### SQLite

| Database | TDB command |
|---|---|
| shop | `.\bin\tdb -d sqlite -dsn 'C:\Users\qxwxi\repos\tdb\.dbs\sqlite\shop.db'` |
| weird | `.\bin\tdb -d sqlite -dsn 'C:\Users\qxwxi\repos\tdb\.dbs\sqlite\weird.db'` |

The SQLite files are plain single-file databases (no `-wal`/`-shm` files left behind) so
they can be copied/opened like any normal `.db` file.

---

## PostgreSQL `shop`

### Schemas (7, all quoted where needed)

| Schema | Purpose |
|---|---|
| `public` | customers, products, reviews, reserved-word/edge-case tables |
| `sales` | partitioned orders, order items, payments, invoices, audit |
| `inventory` | warehouses and stock |
| `analytics` | 1M-event JSONB stream, materialized view, functions/procedure |
| `reporting` | 6 read-only views incl. `WITH CHECK OPTION` view |
| `"LEGACY_2019"` | quoted uppercase legacy schema, 1 table |
| `"Warehouse"` | quoted schema, 1 table with `"Name"` column |

### Row counts (verified)

| Object | Rows |
|---|---|
| `public.customers` | 200,000 |
| `public.products` | 50,000 |
| `sales.orders` (partitioned parent total) | 500,000 |
| `sales.order_items` | 1,500,000 |
| `sales.payments` | 500,000 |
| `public.reviews` | 200,000 |
| `analytics.events` (JSONB) | 1,000,000 |
| `inventory.inventory` | 50,000 |
| `analytics.page_views` | 200,000 |
| `sales.order_tags` | 100,000 |
| `sales.invoices` | 10,000 |
| `sales.order_audit` | 42 (filled by trigger smoke test) |
| `analytics.mv_daily_events` (matview) | 2,160 |
| `inventory.warehouses` | 8 |
| `public.wide_table_40` | 2 |
| `public.empty_table_0` | 0 |
| `public.null_only` | 5 (every column NULL) |
| `public.large_text` | 3 (incl. 1 MB rows) |
| `public.unicode_test` | 20 |
| `public."order"` (reserved word) | 3 |
| `"LEGACY_2019".old_orders` | 2 |
| `"Warehouse".warehouse` | 2 |

### Objects scenario authors care about

* **Range-partitioned table**: `sales.orders` partitioned monthly on `ordered_at`
  (`orders_2024_01` … `orders_2025_12` = 24 monthly partitions + `orders_default` = 25).
  `sales.order_items` and `sales.payments` reference it through a **composite FK**
  `(order_id, order_ordered_at) → (id, ordered_at)`.
* **Materialized view**: `analytics.mv_daily_events` (+ unique index for refresh);
  refresh via `CALL analytics.proc_refresh_dashboard();`.
* **Views**: `reporting.v_customer_order_totals`, `v_product_sales`, `v_daily_revenue`,
  `v_low_stock`, `v_verified_reviews`, and `v_active_customers`
  (`security_barrier = true` + `WITH CHECK OPTION` — a view definition that enforces
  constraints).
* **Functions**: `analytics.fn_customer_lifetime(bigint)`, `analytics.fn_revenue_between(timestamptz,timestamptz)`
  (SQL), `analytics.fn_orders_by_status(order_status)` (**returns TABLE**, PL/pgSQL),
  `public.fn_next_order_number()` (PL/pgSQL), `sales.fn_audit_order_status()` (trigger fn).
* **Procedure**: `analytics.proc_refresh_dashboard()`.
* **Trigger + audit table**: `trg_orders_audit_status` on `sales.orders`
  (`AFTER UPDATE OF status`) writes `sales.order_audit`.
* **Sequences**: `sales.invoice_seq` (explicit; also default of `sales.invoices.invoice_no`)
  plus identity sequences on `customers`, `order_items`, `payments`, `reviews`, `events`,
  `page_views`, `order_audit`, `invoices`.
* **Types**: enums `customer_status`, `order_status`, `payment_method`, `product_category`;
  domains `email_address` (regex CHECK) and `positive_money`; `uuid`, `jsonb`, `numeric`,
  `timestamptz`, `text[]`, `bytea`, `inet`, `tsvector`, `int4range`, `char(2)`.
* **Generated stored columns**: `customers.display_name`, `products.search_vector`,
  `order_items.line_total`.
* **Indexes**: GIN on `customers.tags`, `customers.metadata` (jsonb_path_ops),
  `products.search_vector`, `products.attributes`, `analytics.events.payload`;
  partial indexes on customers/orders/reviews/payments/inventory/order_items;
  BRIN on `analytics.events.occurred_at`; composite + descending indexes.
* **Constraints**: 84 FK constraints (partitioned-table FKs included), 13 CHECKs,
  6 named UNIQUE constraints (e.g. `customers.email`, `products.sku`, `reviews(customer_id, product_id)`).
* **Comments**: 22 table/column comments (e.g. `public.customers`, `public.customers.public_id`,
  `sales.orders`, `analytics.mv_daily_events`, `analytics.fn_orders_by_status`).

### Edge-case objects (identifier/rendering tests)

| Object | Why |
|---|---|
| `public."order"` with columns `"select"`, `"from"`, `"where"`, `"group"` | quoted reserved-word table/columns |
| `"LEGACY_2019"`, `"Warehouse"` | uppercase / quoted schema names |
| `public.wide_table_40` | exactly **40 columns** across most PostgreSQL types |
| `public.empty_table_0` | 0 rows |
| `public.null_only` | 5 rows, **all columns NULL** |
| `public.large_text` | 1,048,576-char text (`id=1`), 1,000,000-char single line (no newline), 1 MB `bytea`; `id=3` is ~1 MB of CJK/emoji |
| `public.unicode_test` | 20 rows: Chinese, emoji, ZWJ family, Japanese, Korean, Arabic/Hebrew RTL, Cyrillic, accented, combining, zero-width, fullwidth, mixed, embedded newline/tab, 500-emoji run, math symbols, hieroglyphs |

---

## PostgreSQL `legacy`

**12 schemas / 27 tables** (mixed-case identifiers, spaces, reserved words, empty tables,
widely varying types). 1–3 tables per schema:

| Schema | Tables |
|---|---|
| `public` | `"Mixed Case Table"` (25 rows), `"order"` (reserved words), `"Empty Table"` (0 rows) |
| `"Accounting"` | `"Chart Of Accounts"` (4), `"GL Entries"` (1,000) |
| `"Human Resources"` | `employees` (5, self-FK, `bytea`, `text[]`), `"Time Off Requests"` |
| `"Sales & Marketing"` | `campaigns` (`uuid`, `money`, `int4range`, `jsonb`), `leads` (200) |
| `"Order Mgmt"` | `vendors` (`interval`), `"Purchase Orders"` (`money`), `"PO Lines"` |
| `"CRM"` | `contacts` (500), `deals` (800) |
| `"Data Warehouse"` | `"Fact Sales"` (5,000), `"Dim Customer"` (500), `staging_events` (`json`, `bytea`) |
| `finance` | `invoices` (1,000, enum + `xml`), `payments` (400) |
| `hr` | `departments` (`inet`), `"job_history"` |
| `ops` | `"Job Queue"` (0 rows), `"Maintenance Log"` (`interval`) |
| `"archive_2019"` | `"Old Orders"` (0 rows), `"Old Customers"` (2) |
| `"UPPER CASE"` | `"UPPER TABLE"`, `"MixedCase"` |

---

## MySQL `shop`

### Row counts (verified)

| Object | Rows |
|---|---|
| `customers` | 150,000 |
| `products` | 40,000 |
| `orders` | 300,000 |
| `order_items` | 800,000 |
| `payments` | 300,000 |
| `order_events` (partitioned) | 400,000 |
| `order_audit` | 33 |
| `categories` | 8 |
| `` `order` `` (reserved word) | 3 |
| `empty_table` | 0 |

### Objects scenario authors care about

* **Partitioned InnoDB table**: `order_events` `PARTITION BY RANGE (id)` with
  `p0`–`p3` (4 partitions). `orders` itself is *not* partitioned so that FKs can be used
  (MySQL/InnoDB limitation: partitioned tables cannot take part in FKs).
* **Foreign keys (4)**: `orders.customer_id → customers`, `order_items.order_id → orders`,
  `order_items.product_id → products`, `payments.order_id → orders` (all `ON DELETE CASCADE`
  except `orders.customer_id`).
* **FULLTEXT**: `products` has `FULLTEXT KEY ft_products (name, description)`.
* **Multi-valued JSON index**: `products.tags` (`CAST(tags->'$[*]' AS CHAR(64) ARRAY)`).
* **Generated STORED columns**: `customers.full_name`, `order_items.line_total`
  (`ROUND(quantity*unit_price*(1-discount/100),2)`).
* **JSON columns (7)**: `customers.tags/metadata`, `products.tags/attributes`,
  `orders.shipping_address`, `payments.raw_response`, `order_events.payload`.
* **ENUM columns**: customer status, order status, payment method, product category.
* **Routines**: procedure `sp_customer_orders(IN customer_id)`,
  function `fn_customer_total(customer_id) READS SQL DATA`.
* **Trigger**: `trg_orders_audit_status` (`AFTER UPDATE` on `orders`) →
  `order_audit` (33 rows from the smoke test).
* **Views (3)**: `v_customer_summary`, `v_product_sales`, `v_order_totals`.
* **Reserved-word identifiers**: table `` `order` `` with columns `` `select` ``,
  `` `from` ``, `` `where` ``, `` `group` ``, `` `rank` ``.
* **Unicode**: `customers` names include 中文/emoji (`id=4` = `王伟 李`); product names/descriptions
  include 🚀/😀; utf8mb4 throughout.
* **Wide types**: `utf8mb4`, `JSON`, `BIT`, `VARBINARY`, `BLOB`, `MEDIUMINT`, `YEAR`,
  `DATETIME(6)`, `SET`, `ENUM`.

> Windows MySQL/MariaDB run with `lower_case_table_names=1`, so mixed-case table names are
> stored lowercase in `information_schema`; identifiers that require quoting still work
> exactly as written in the SQL files.

---

## MariaDB `shop`

### Row counts (verified)

| Object | Rows |
|---|---|
| `customers` | 100,000 |
| `products` (**SYSTEM VERSIONED**) | 30,000 |
| `products FOR SYSTEM_TIME ALL` (current + 500 history rows) | 30,500 |
| `orders` | 200,000 |
| `order_items` | 300,000 |
| `payments` | 200,000 |
| `invoices` (SEQUENCE-backed) | 5,000 |
| `order_audit` | 24 |
| `` `order` `` (reserved word) | 3 |
| `empty_table` | 0 |

### MariaDB-specific objects

* **System-versioned table**: `products` `WITH SYSTEM VERSIONING`. MariaDB 11.4 hides the
  internal `products_history` table from `SHOW TABLES`/`information_schema` (as seen on this
  server), so history must be queried with
  `SELECT COUNT(*) FROM shop.products FOR SYSTEM_TIME ALL;` → 30,500 rows
  (30,000 current + 500 rows updated by the loader).
* **SEQUENCE**: `seq_invoice_no` (starts at 500,000). `shop.invoices.invoice_no` values are
  drawn from it via `NEXT VALUE FOR seq_invoice_no`; `MIN(invoice_no)` is 500,000.
* **DEFINER routines**: `sp_customer_orders` and `fn_customer_total` are created as
  `DEFINER='tdb'@'%'` (visible in `SHOW CREATE PROCEDURE/FUNCTION` and
  `information_schema.ROUTINES`).
* **Views (3)**: `v_customer_summary` (`ALGORITHM=MERGE`), `v_product_sales`
  (`ALGORITHM=TEMPTABLE`), `v_order_totals`.
* **JSON alias columns**: `customers.tags/metadata`, `products.tags/attributes`,
  `orders.shipping_address`, `payments.raw_response` become `LONGTEXT` + `json_valid()` CHECK
  (6 checks visible in `information_schema.CHECK_CONSTRAINTS`).
* **Generated columns**: `customers.full_name`, `order_items.line_total` (STORED).
* **FULLTEXT**: `products` has `FULLTEXT KEY ft_products (name, description)`.
* **ENUM columns**, reserved-word `` `order` `` table, utf8mb4 Chinese/emoji data.
* **Trigger**: `trg_orders_audit_status` → `order_audit` (24 rows from the smoke test).

---

## Legacy databases (`legacy` on MySQL and MariaDB)

Each server has **12 databases total** (`information_schema`, `mysql`, `performance_schema`,
`sys`, `shop`, `legacy` + six `legacy_*` side databases) for database-filter/object-browser
tests:

`legacy_2019`, `legacy_2020`, `legacy_eu`, `legacy_us`, `legacy_apac`, `legacy_archive`
(each 1–2 small tables with locale-specific Unicode data).

The `legacy` database itself has **8 base tables + 1 view**:

| Object | Why it matters |
|---|---|
| `MixedCaseTable` (`FirstName`, `CreatedAt`, …) | mixed-case identifiers (lowercased on this Windows server) |
| `Order Details` (`OrderID`, `Qty`, …) | spaces in table/column names |
| `` `order` `` (`` `select` ``, `` `from` ``, `` `where` ``, `` `group` ``, `` `rank` ``) | reserved words |
| `EmptyTable` | 0 rows |
| `DataTypes` | ~26 MySQL types: BIT, TINYINT…BIGINT, DECIMAL, FLOAT/DOUBLE, DATE/TIME/DATETIME(6)/TIMESTAMP/YEAR, CHAR/VARCHAR, BINARY/VARBINARY, BLOB, TEXT, ENUM, SET, JSON, POINT |
| `Legacy Customers` | 5 rows incl. 中文 😀, accented, Cyrillic |
| `FK Parent` / `FK Child` | FK demo |
| `Legacy View` | view with a name containing a space |

---

## SQLite

Built by `gen_sqlite\gen_sqlite.exe` (self-contained Go module, `modernc.org/sqlite`
v1.60.1, no CGO). Files land in `C:\Users\qxwxi\repos\tdb\.dbs\sqlite\`.

### `shop.db` (verified)

| Object | Rows |
|---|---|
| `users` | 100,000 |
| `products` | 5,000 |
| `orders` | 200,000 |
| `order_items` | 300,000 |
| `events` | 20,000 |
| `order_audit` | 33 |
| **users + orders + order_items** | **600,000** |

Objects: `users` (uuid/JSON-text metadata/BLOB avatar/CJK+emoji names), `products`
(BLOB image, JSON text attributes), `orders` (FK to users, JSON text address, unicode
notes), `order_items` (**stored generated column** `line_total`, FKs, CHECKs),
`events` (JSON text payload), `order_audit` + trigger `trg_order_audit_status`,
views `v_user_order_summary`, `v_product_sales`, `v_order_totals`, unique/partial/
composite indexes, `ANALYZE` statistics.

### `weird.db` (edge-case schema)

| Object | Rows | Notes |
|---|---|---|
| `"select"` | 1 | reserved-word table/columns (`"from"`, `"where"`, `"group"`, `"order"`) |
| `"order"` | 1 | reserved-word table (`"select"`, `"rank"`) |
| `"weird table with spaces"` | 1 | spaces and `-` in column names |
| `"UPPERCASE"` | 1 | quoted uppercase identifiers |
| `"表"` | 1 | non-ASCII table/column names (`"键"`, `"值"`) |
| `"empty table"` | 0 | deliberately empty |
| `"nulls"` | 2 | all columns NULL |
| `"types test"` | 3 | INTEGER/REAL/TEXT/BLOB/`"Date Col"` |
| `"autoinc"` | 2 | AUTOINCREMENT (`sqlite_sequence` present) |
| `"gen_col"` | 2 | STORED + VIRTUAL generated columns |
| `"no_rowid"` | 2 | `WITHOUT ROWID` |
| `"strict_t"` | 1 | `STRICT` table |
| `"json_docs"` | 2 | `CHECK (json_valid(doc))` |
| `"big text"` | 1 | 1 MB text, 1 MB single line, 1 MB BLOB |
| `"unicode"` | 15 | CJK/emoji/RTL/combining/zero-width/etc. |
| `"audit_target"` / `"audit_log"` | 1 / 1 | trigger `trg_weird_audit` |
| `"wide_40"` | 0 | 40-column table (empty) |
| `"weird view"` | 3 | view across weird tables |

`gen_sqlite.exe -outdir <dir> -verify` re-checks counts and Unicode round-trip/generated-column/
trigger/1 MB probes and exits non-zero on mismatch.

---

## Unicode round-trip verification (executed by `load.ps1`)

The loader reads the values back and compares them to expected strings/patterns; all pass:

* PostgreSQL: `public.unicode_test` `id=1` → `你好，世界！数据库测试`,
  `id=3` → `🚀🎉😀🔥👍`, `id=5` → `こんにちは世界、テストです`;
  40,000 customer rows with CJK names, 20,000 review bodies with 👍.
* MySQL: `shop.customers` `id=4` → `王伟 李`; 3,077 product descriptions containing 🚀.
* MariaDB: `shop.customers` `id=4` → `王伟 李`; 3,000 product names containing 😀.
* SQLite: `weird.unicode` emoji row → `🚀🎉😀🔥👍`, Chinese row → `你好，世界！数据库测试`;
  23,336 users with CJK names, 385 product descriptions with 🚀.

Manual spot check:

```powershell
# PostgreSQL
& "$PGBIN\psql.exe" -d shop -c "SELECT id, phrase FROM public.unicode_test WHERE id IN (1,3,5);"
# MySQL / MariaDB
& "$MYBIN\mysql.exe" -e "SELECT id, full_name FROM shop.customers WHERE id=4;"
& "$MABIN\mariadb.exe" -uroot -ptdbpass -e "SELECT id, full_name FROM shop.customers WHERE id=4;"
# SQLite via the generator
& .\gen_sqlite\gen_sqlite.exe -outdir C:\Users\qxwxi\repos\tdb\.dbs\sqlite -verify
```

---

## Notes & caveats

* **Idempotency**: PostgreSQL loaders `DROP SCHEMA … CASCADE` / recreate; MySQL and MariaDB
  loaders `DROP DATABASE …` / recreate (`shop`, `legacy`, `legacy_*`); SQLite artifacts are
  deleted and rebuilt. Helper tables (`_nums`, `_digits`) are dropped at the end of the load.
* **Deterministic data**: data is generated with modulo formulas from `generate_series` /
  helper numbers / a fixed Go loop, so reloads produce identical row counts and values.
* **PostgreSQL trigger count** shows 26 in `pg_trigger` because the row trigger is cloned onto
  all 25 partitions; there is one logical trigger (`trg_orders_audit_status`).
* **MySQL `orders`** intentionally has no partitions so it can be the FK target for
  `order_items`/`payments`; the partitioned InnoDB requirement is covered by `order_events`.
* **MariaDB system-versioned history** is not directly selectable as a table name on this
  server build — use `FOR SYSTEM_TIME ALL` / `FOR SYSTEM_TIME AS OF`.
* Row counts above are from the last full `load.ps1` run (see
  `.artifacts\dataset-load.log`); they are stable across reloads.
* Server processes/ports are never started, stopped or reconfigured by these scripts.
