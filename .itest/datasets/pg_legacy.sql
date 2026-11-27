-- pg_legacy.sql -- PostgreSQL 17 legacy database for TDB object-browser tests (database: legacy)
-- 12 non-system schemas (including public), each with 1-3 tables, messy identifiers,
-- mixed-case names, spaces, reserved words, empty tables and widely varied types.
-- Idempotent: drops and recreates every managed schema.
\set ON_ERROR_STOP on
\timing on
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SET synchronous_commit = off;

DROP SCHEMA IF EXISTS "Accounting" CASCADE;
DROP SCHEMA IF EXISTS "Human Resources" CASCADE;
DROP SCHEMA IF EXISTS "Sales & Marketing" CASCADE;
DROP SCHEMA IF EXISTS "Order Mgmt" CASCADE;
DROP SCHEMA IF EXISTS "CRM" CASCADE;
DROP SCHEMA IF EXISTS "Data Warehouse" CASCADE;
DROP SCHEMA IF EXISTS finance CASCADE;
DROP SCHEMA IF EXISTS hr CASCADE;
DROP SCHEMA IF EXISTS ops CASCADE;
DROP SCHEMA IF EXISTS "archive_2019" CASCADE;
DROP SCHEMA IF EXISTS "UPPER CASE" CASCADE;
DROP SCHEMA IF EXISTS public CASCADE;
CREATE SCHEMA public;
CREATE SCHEMA "Accounting";
CREATE SCHEMA "Human Resources";
CREATE SCHEMA "Sales & Marketing";
CREATE SCHEMA "Order Mgmt";
CREATE SCHEMA "CRM";
CREATE SCHEMA "Data Warehouse";
CREATE SCHEMA finance;
CREATE SCHEMA hr;
CREATE SCHEMA ops;
CREATE SCHEMA "archive_2019";
CREATE SCHEMA "UPPER CASE";
COMMENT ON SCHEMA "Accounting" IS 'Legacy accounting data (mixed-case schema name)';
COMMENT ON SCHEMA "Sales & Marketing" IS 'Legacy schema name with an ampersand and spaces';
COMMENT ON SCHEMA "archive_2019" IS 'Empty legacy archive schema';

-- ---------------------------------------------------------------- public
CREATE TABLE public."Mixed Case Table" (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "First Name"  text NOT NULL,
    "Last Name"   text NOT NULL,
    "E-mail"      text,
    "Created At"  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO public."Mixed Case Table" ("First Name", "Last Name", "E-mail")
SELECT 'First' || g, 'Last' || g, 'mixed' || g || '@legacy.example.com'
FROM generate_series(1, 25) AS g;

CREATE TABLE public."order" (
    "select" integer PRIMARY KEY,
    "from"   text,
    "where"  text,
    "group"  text,
    "limit"  numeric(10,2)
);
INSERT INTO public."order" VALUES
    (1, 'warehouse', 'active', 'A', 10.00),
    (2, 'customer', 'inactive', 'B', 20.50),
    (3, 'supplier', 'active', 'A', 30.75);

CREATE TABLE public."Empty Table" (
    id    integer PRIMARY KEY,
    label text
);
COMMENT ON TABLE public."Empty Table" IS 'Legacy table with no rows';

-- ---------------------------------------------------------------- Accounting
CREATE TABLE "Accounting"."Chart Of Accounts" (
    "Account Code" char(8) PRIMARY KEY,
    "Account Name" text NOT NULL,
    "Account Type" text NOT NULL,
    is_active      boolean NOT NULL DEFAULT true
);
INSERT INTO "Accounting"."Chart Of Accounts" VALUES
    ('1000', 'Assets', 'asset', true),
    ('2000', 'Liabilities', 'liability', true),
    ('4000', 'Revenue', 'revenue', true),
    ('5000', 'Expenses', 'expense', false);

CREATE TABLE "Accounting"."GL Entries" (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Account Code" char(8) REFERENCES "Accounting"."Chart Of Accounts"("Account Code"),
    debit         numeric(12,2) NOT NULL DEFAULT 0,
    credit        numeric(12,2) NOT NULL DEFAULT 0,
    posted_on     date NOT NULL,
    memo          text
);
INSERT INTO "Accounting"."GL Entries" ("Account Code", debit, credit, posted_on, memo)
SELECT (ARRAY['1000','2000','4000','5000'])[1 + (g % 4)],
       CASE WHEN g % 2 = 0 THEN (g % 1000)::numeric(12,2) ELSE 0 END,
       CASE WHEN g % 2 = 1 THEN (g % 500)::numeric(12,2) ELSE 0 END,
       DATE '2020-01-01' + (g % 2000),
       'entry ' || g
FROM generate_series(1, 1000) AS g;

-- ---------------------------------------------------------------- Human Resources
CREATE TABLE "Human Resources".employees (
    id          integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "First Name" text NOT NULL,
    "Last Name"  text NOT NULL,
    "Hire Date"  date NOT NULL,
    salary       numeric(12,2),
    "Manager ID" integer REFERENCES "Human Resources".employees(id),
    photo        bytea,
    tags         text[],
    "Homepage"   text
);
INSERT INTO "Human Resources".employees ("First Name", "Last Name", "Hire Date", salary, "Manager ID", photo, tags, "Homepage")
VALUES
    ('Ada', 'Lovelace', DATE '2019-03-01', 95000.00, NULL, decode('00ff','hex'), ARRAY['engineering','founder'], 'https://example.com/ada'),
    ('Grace', 'Hopper', DATE '2019-06-15', 120000.00, 1, NULL, ARRAY['engineering','navy'], NULL),
    ('Alan', 'Turing', DATE '2020-01-20', 110000.00, 1, NULL, ARRAY['research'], 'https://example.com/alan'),
    ('Katherine', 'Johnson', DATE '2021-07-01', 99000.00, 2, NULL, ARRAY['research','math'], NULL),
    ('林', '娜', DATE '2022-02-02', 88000.00, 2, NULL, ARRAY['design','中文'], NULL);

CREATE TABLE "Human Resources"."Time Off Requests" (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id integer NOT NULL REFERENCES "Human Resources".employees(id),
    "Start Date" date NOT NULL,
    "End Date"   date NOT NULL,
    hours        numeric(5,1) NOT NULL,
    approved     boolean NOT NULL DEFAULT false,
    reason       text
);
INSERT INTO "Human Resources"."Time Off Requests" (employee_id, "Start Date", "End Date", hours, approved, reason)
VALUES
    (1, DATE '2025-01-02', DATE '2025-01-06', 40.0, true, 'vacation'),
    (2, DATE '2025-02-10', DATE '2025-02-11', 16.0, true, 'sick'),
    (3, DATE '2025-03-01', DATE '2025-03-01', 8.0, false, 'personal');

-- ---------------------------------------------------------------- Sales & Marketing
CREATE TABLE "Sales & Marketing".campaigns (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    budget     money,
    starts_at  timestamptz NOT NULL,
    ends_at    timestamptz,
    audience   int4range,
    meta       jsonb
);
INSERT INTO "Sales & Marketing".campaigns (name, budget, starts_at, ends_at, audience, meta)
VALUES
    ('Spring Launch', '$25000.00'::money, timestamptz '2025-03-01 00:00:00+00', timestamptz '2025-05-31 00:00:00+00', '[18,35)'::int4range, '{"channel":"email"}'::jsonb),
    ('Summer Sale 夏季促销', '$12000.50'::money, timestamptz '2025-06-01 00:00:00+00', timestamptz '2025-08-31 00:00:00+00', '[25,55)'::int4range, '{"channel":"social"}'::jsonb),
    ('Year End', NULL, timestamptz '2025-11-01 00:00:00+00', NULL, '[30,65)'::int4range, NULL);

CREATE TABLE "Sales & Marketing".leads (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL,
    source     text,
    score      numeric(5,2),
    created_at timestamptz NOT NULL DEFAULT now(),
    notes      text
);
INSERT INTO "Sales & Marketing".leads (email, source, score, notes)
SELECT 'lead' || g || '@example.com',
       (ARRAY['web','event','referral','webinar'])[1 + (g % 4)],
       (g % 100)::numeric(5,2),
       CASE WHEN g % 5 = 0 THEN '来电咨询，请回电' ELSE NULL END
FROM generate_series(1, 200) AS g;

-- ---------------------------------------------------------------- Order Mgmt
CREATE TABLE "Order Mgmt".vendors (
    id     integer PRIMARY KEY,
    name   text NOT NULL,
    terms  interval,
    rating smallint CHECK (rating BETWEEN 1 AND 5)
);
INSERT INTO "Order Mgmt".vendors VALUES
    (1, 'Acme Supplies', interval '30 days', 5),
    (2, 'Globex GmbH', interval '45 days', 4),
    (3, 'Initech', interval '15 days', 2),
    (4, 'Umbrella 供应', interval '60 days', 3);

CREATE TABLE "Order Mgmt"."Purchase Orders" (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vendor_id  integer REFERENCES "Order Mgmt".vendors(id),
    "PO Date"  date NOT NULL,
    total      money,
    status     char(1) NOT NULL DEFAULT 'O'
);
INSERT INTO "Order Mgmt"."Purchase Orders" (vendor_id, "PO Date", total, status)
SELECT 1 + (g % 4), DATE '2024-01-01' + (g % 365), ('$' || ((g % 10000) + 100))::money,
       (ARRAY['O','P','C'])[1 + (g % 3)]
FROM generate_series(1, 150) AS g;

CREATE TABLE "Order Mgmt"."PO Lines" (
    po_id      bigint NOT NULL REFERENCES "Order Mgmt"."Purchase Orders"(id),
    line_no    integer NOT NULL,
    sku        text NOT NULL,
    qty        integer NOT NULL CHECK (qty > 0),
    unit_price numeric(12,2) NOT NULL,
    PRIMARY KEY (po_id, line_no)
);
INSERT INTO "Order Mgmt"."PO Lines" (po_id, line_no, sku, qty, unit_price)
SELECT po.id, line, 'SKU-' || po.id || '-' || line, 1 + (po.id % 20),
       round((((po.id * 10 + line) % 5000) + 100) / 100.0, 2)
FROM "Order Mgmt"."Purchase Orders" po
CROSS JOIN generate_series(1, 3) AS line;

-- ---------------------------------------------------------------- CRM
CREATE TABLE "CRM".contacts (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    full_name text NOT NULL,
    email     text,
    phone     text,
    company   text,
    tags      text[]
);
INSERT INTO "CRM".contacts (full_name, email, phone, company, tags)
SELECT (ARRAY['Alice Smith','Bob Jones','王小明','Chloé Martin','Иван Петров'])[1 + (g % 5)],
       'contact' || g || '@crm.example.com',
       '+1-555-' || lpad((g % 9999)::text, 4, '0'),
       (ARRAY['Acme','Globex','Initech','Hooli'])[1 + (g % 4)],
       ARRAY['segment' || (g % 6)]
FROM generate_series(1, 500) AS g;

CREATE TABLE "CRM".deals (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    contact_id  bigint REFERENCES "CRM".contacts(id),
    amount      numeric(14,2) NOT NULL,
    stage       text NOT NULL,
    close_date  date,
    probability real CHECK (probability BETWEEN 0 AND 1)
);
INSERT INTO "CRM".deals (contact_id, amount, stage, close_date, probability)
SELECT 1 + (g % 500), round((g % 200000) / 10.0, 2),
       (ARRAY['prospecting','qualification','proposal','negotiation','closed_won','closed_lost'])[1 + (g % 6)],
       DATE '2025-01-01' + (g % 365), round((g % 100) / 100.0, 2)
FROM generate_series(1, 800) AS g;

-- ---------------------------------------------------------------- Data Warehouse
CREATE TABLE "Data Warehouse"."Dim Customer" (
    customer_key integer PRIMARY KEY,
    name         text NOT NULL,
    segment      text,
    valid_from   date NOT NULL,
    valid_to     date
);
INSERT INTO "Data Warehouse"."Dim Customer" (customer_key, name, segment, valid_from, valid_to)
SELECT g, 'Customer ' || g, (ARRAY['consumer','smb','enterprise'])[1 + (g % 3)],
       DATE '2020-01-01', CASE WHEN g % 3 = 0 THEN NULL ELSE DATE '2030-01-01' END
FROM generate_series(1, 500) AS g;

CREATE TABLE "Data Warehouse"."Fact Sales" (
    id          bigint NOT NULL,
    date_key    integer NOT NULL,
    product_key integer NOT NULL,
    quantity    integer NOT NULL,
    revenue     numeric(14,2) NOT NULL,
    PRIMARY KEY (id, date_key)
);
INSERT INTO "Data Warehouse"."Fact Sales" (id, date_key, product_key, quantity, revenue)
SELECT g, 20240101 + (g % 365), 1 + (g % 500), 1 + (g % 10),
       round((g % 100000) / 100.0, 2)
FROM generate_series(1, 5000) AS g;

CREATE TABLE "Data Warehouse".staging_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw        json NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed  boolean NOT NULL DEFAULT false,
    payload    bytea
);
INSERT INTO "Data Warehouse".staging_events (raw, processed, payload)
SELECT json_build_object('event', 'click', 'n', g), g % 2 = 0, decode(md5('ev' || g), 'hex')
FROM generate_series(1, 300) AS g;

-- ---------------------------------------------------------------- finance
CREATE TYPE finance.invoice_status AS ENUM ('draft', 'sent', 'paid', 'overdue', 'void');
CREATE TABLE finance.invoices (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invoice_no text NOT NULL UNIQUE,
    customer   text NOT NULL,
    amount     numeric(12,2) NOT NULL CHECK (amount >= 0),
    issued     date NOT NULL,
    due        date NOT NULL,
    status     finance.invoice_status NOT NULL DEFAULT 'draft',
    xml_doc    xml
);
INSERT INTO finance.invoices (invoice_no, customer, amount, issued, due, status, xml_doc)
SELECT 'INV-' || lpad(g::text, 6, '0'), 'Customer ' || (1 + (g % 500)),
       round((g % 50000) / 100.0, 2), DATE '2024-01-01' + (g % 700),
       DATE '2024-02-01' + (g % 700),
       (ARRAY['draft','sent','paid','overdue','void']::finance.invoice_status[])[1 + (g % 5)],
       CASE WHEN g % 10 = 0 THEN ('<invoice><id>' || g || '</id></invoice>')::xml ELSE NULL END
FROM generate_series(1, 1000) AS g;

CREATE TABLE finance.payments (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invoice_id bigint NOT NULL REFERENCES finance.invoices(id),
    paid_at    timestamptz NOT NULL,
    amount     numeric(12,2) NOT NULL,
    method     text
);
INSERT INTO finance.payments (invoice_id, paid_at, amount, method)
SELECT 1 + (g % 1000), timestamptz '2024-01-15 00:00:00+00' + make_interval(days => (g % 700)::int),
       round((g % 20000) / 100.0, 2), (ARRAY['wire','check','card'])[1 + (g % 3)]
FROM generate_series(1, 400) AS g;

-- ---------------------------------------------------------------- hr
CREATE TABLE hr.departments (
    id        integer PRIMARY KEY,
    name      text NOT NULL,
    headcount integer NOT NULL DEFAULT 0,
    location  inet
);
INSERT INTO hr.departments VALUES
    (1, 'Engineering', 42, '10.0.1.1'),
    (2, 'Sales', 18, '10.0.1.2'),
    (3, '研发部', 7, '10.0.2.9');

CREATE TABLE hr."job_history" (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id integer NOT NULL,
    title       text NOT NULL,
    start_date  date NOT NULL,
    end_date    date,
    pay_grade   char(2)
);
INSERT INTO hr."job_history" (employee_id, title, start_date, end_date, pay_grade)
VALUES
    (1, 'Engineer', DATE '2019-03-01', DATE '2021-01-01', 'E3'),
    (1, 'Senior Engineer', DATE '2021-01-02', NULL, 'E4'),
    (2, 'Analyst', DATE '2020-05-01', DATE '2022-06-30', 'A2');

-- ---------------------------------------------------------------- ops
CREATE TABLE ops."Job Queue" (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_type text NOT NULL,
    payload  jsonb,
    run_at   timestamptz,
    state    text NOT NULL DEFAULT 'queued'
);
COMMENT ON TABLE ops."Job Queue" IS 'Legacy job queue (empty)';

CREATE TABLE ops."Maintenance Log" (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service     text NOT NULL,
    performed_at timestamptz NOT NULL,
    duration    interval,
    technician  text,
    notes       text
);
INSERT INTO ops."Maintenance Log" (service, performed_at, duration, technician, notes)
VALUES
    ('db-vacuum', timestamptz '2025-01-01 02:00:00+00', interval '01:15:00', 'ops-oncall', 'routine'),
    ('disk-rotate', timestamptz '2025-01-08 02:00:00+00', interval '00:10:30', 'ops-oncall', 'ok'),
    ('index-rebuild 索引重建', timestamptz '2025-01-15 03:00:00+00', interval '2 hours', '张三', '耗时较长');

-- ---------------------------------------------------------------- archive_2019
CREATE TABLE "archive_2019"."Old Orders" (
    id        integer PRIMARY KEY,
    placed_at date,
    total     numeric(10,2),
    note      text
);
COMMENT ON TABLE "archive_2019"."Old Orders" IS 'Empty legacy archive table';

CREATE TABLE "archive_2019"."Old Customers" (
    id       integer PRIMARY KEY,
    name     text,
    email    text,
    migrated boolean NOT NULL DEFAULT false
);
INSERT INTO "archive_2019"."Old Customers" VALUES
    (1, 'Old Alice', 'oldalice@example.com', true),
    (2, 'Old Bob', 'oldbob@example.com', false);

-- ---------------------------------------------------------------- "UPPER CASE"
CREATE TABLE "UPPER CASE"."UPPER TABLE" (
    "ID"    integer PRIMARY KEY,
    "NAME"  varchar(50) NOT NULL,
    "VALUE" numeric(10,2)
);
INSERT INTO "UPPER CASE"."UPPER TABLE" VALUES (1, 'ALPHA', 1.10), (2, 'BETA', 2.20);

CREATE TABLE "UPPER CASE"."MixedCase" (
    "CamelCaseID"    integer PRIMARY KEY,
    "snake_case_col" text,
    "Hybrid-Column"  text
);
INSERT INTO "UPPER CASE"."MixedCase" VALUES (1, 'snake', 'hybrid'), (2, 'case', 'mix');

-- Stats
ANALYZE public."Mixed Case Table";
ANALYZE "Accounting"."GL Entries";
ANALYZE "Human Resources".employees;
ANALYZE "Sales & Marketing".leads;
ANALYZE "Order Mgmt"."PO Lines";
ANALYZE "CRM".deals;
ANALYZE "Data Warehouse"."Fact Sales";
ANALYZE finance.invoices;
ANALYZE hr.departments;
ANALYZE ops."Maintenance Log";
ANALYZE "archive_2019"."Old Customers";
ANALYZE "UPPER CASE"."UPPER TABLE";

\echo 'pg_legacy.sql load complete'
