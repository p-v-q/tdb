-- mysql_shop.sql -- MySQL 8.4 dataset for TDB integration tests (database: shop)
-- Idempotent: drops and recreates the shop database, then bulk-loads via helper table.
-- Run: mysql --default-character-set=utf8mb4 -v -e "source mysql_shop.sql"   (as root)
SET NAMES utf8mb4;
SET SESSION sql_mode = 'STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION';
SET SESSION unique_checks = 1;
SET SESSION foreign_key_checks = 1;

DROP DATABASE IF EXISTS shop;
CREATE DATABASE shop CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
USE shop;

-- =====================================================================
-- Helper number table (dropped at the end of the script)
-- =====================================================================
CREATE TABLE _digits (n TINYINT NOT NULL PRIMARY KEY);
INSERT INTO _digits (n) VALUES (0),(1),(2),(3),(4),(5),(6),(7),(8),(9);

CREATE TABLE _nums (n INT NOT NULL PRIMARY KEY);
INSERT INTO _nums (n)
SELECT a.n + b.n*10 + c.n*100 + d.n*1000 + e.n*10000 + f.n*100000
FROM _digits a, _digits b, _digits c, _digits d, _digits e, _digits f;

-- =====================================================================
-- 1. customers (150k)
-- =====================================================================
CREATE TABLE customers (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    uuid          CHAR(36) NOT NULL,
    email         VARCHAR(320) NOT NULL,
    first_name    VARCHAR(80) NOT NULL,
    last_name     VARCHAR(80) NOT NULL,
    full_name     VARCHAR(161) GENERATED ALWAYS AS (CONCAT(first_name, ' ', last_name)) STORED,
    status        ENUM('active','inactive','vip','churned') NOT NULL DEFAULT 'active',
    balance       DECIMAL(14,2) NOT NULL DEFAULT 0.00,
    tags          JSON,
    metadata      JSON,
    avatar        VARBINARY(256),
    country       CHAR(2) NOT NULL DEFAULT 'US',
    created_at    DATETIME(6) NOT NULL,
    last_login_at DATETIME(6) NULL,
    UNIQUE KEY uq_customers_uuid (uuid),
    UNIQUE KEY uq_customers_email (email),
    KEY ix_customers_status_created (status, created_at DESC),
    KEY ix_customers_country (country)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
ALTER TABLE customers COMMENT = '150k customers; JSON, ENUM, generated full_name, utf8mb4 Chinese/emoji';

INSERT INTO customers (uuid, email, first_name, last_name, status, balance, tags, metadata, avatar, country, created_at, last_login_at)
SELECT UUID(),
       CONCAT('cust', n, '@example.com'),
       ELT(1 + MOD(n, 20), 'Alice','Bob','Chloé','王伟','李娜','José','Yuki','Priya','Мюллер','O''Brien',
                            'Ahmed','Fatima','Søren','Nguyễn','Asha','Diego','Mei','Ivan','Leila','Tomás'),
       ELT(1 + MOD(n, 20), 'Smith','Johnson','张','李','García','Müller','Sato','Patel','陈','O''Brien',
                            'Kim','Rossi','Dubois','Ivanov','Tanaka','Silva','Ng','Hernández','Kowalski','Brown'),
       ELT(1 + MOD(n, 6), 'active','active','active','vip','inactive','churned'),
       ROUND(MOD(n, 100000) / 100, 2),
       JSON_ARRAY(CONCAT('tag', MOD(n, 50)), CONCAT('tier', MOD(n, 5))),
       JSON_OBJECT('signup_source', ELT(1 + MOD(n, 4), 'web','ios','android','referral'),
                   'score', MOD(n, 100),
                   'vip', MOD(n, 17) = 0,
                   'prefs', JSON_OBJECT('lang', ELT(1 + MOD(n, 4), 'en','zh','es','fr'))),
       CASE WHEN MOD(n, 10) = 0 THEN UNHEX(MD5(CONCAT('avatar', n))) ELSE NULL END,
       ELT(1 + MOD(n, 10), 'US','CN','DE','FR','JP','BR','IN','GB','ES','CA'),
       TIMESTAMPADD(SECOND, MOD(n * 37, 86400), DATE_ADD('2023-01-01 00:00:00', INTERVAL MOD(n, 730) DAY)),
       CASE WHEN MOD(n, 7) = 0 THEN NULL
            ELSE TIMESTAMPADD(SECOND, MOD(n * 37, 86400), DATE_ADD('2023-01-01 00:00:00', INTERVAL MOD(n, 730) DAY)) END
FROM _nums WHERE n < 150000;

-- =====================================================================
-- 2. products (40k)
-- =====================================================================
CREATE TABLE products (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sku          VARCHAR(64) NOT NULL,
    name         VARCHAR(255) NOT NULL,
    description  TEXT,
    category     ENUM('electronics','clothing','books','home','sports','toys','food','beauty') NOT NULL DEFAULT 'electronics',
    price        DECIMAL(12,2) NOT NULL,
    cost         DECIMAL(12,2),
    stock        INT NOT NULL DEFAULT 0,
    is_active    TINYINT(1) NOT NULL DEFAULT 1,
    tags         JSON,
    attributes   JSON,
    image        BLOB,
    created_at   DATETIME(6) NOT NULL,
    UNIQUE KEY uq_products_sku (sku),
    KEY ix_products_category_price (category, price),
    KEY ix_products_tags ( (CAST(tags->'$[*]' AS CHAR(64) ARRAY)) ),
    FULLTEXT KEY ft_products (name, description)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
ALTER TABLE products COMMENT = '40k products; FULLTEXT index, JSON + multi-valued JSON index, ENUM category';

INSERT INTO products (sku, name, description, category, price, cost, stock, is_active, tags, attributes, image, created_at)
SELECT CONCAT('SKU-', LPAD(n, 7, '0')),
       CONCAT(ELT(1 + MOD(n, 10), 'Widget','Gadget','Gizmo','Doohickey','Thingamajig',
                                    'Sprocket','Flux Capacitor','量子笔记本','Emoji Mug 😀','Café Grinder'), ' ', n),
       CASE WHEN MOD(n, 13) = 0
            THEN CONCAT('Product ', n, ' — 高质量商品 🚀 limited edition')
            ELSE CONCAT('Product ', n, ' description with lorem ipsum dolor sit amet') END,
       ELT(1 + MOD(n, 8), 'electronics','clothing','books','home','sports','toys','food','beauty'),
       ROUND((MOD(n, 90000) + 100) / 100, 2),
       ROUND((MOD(n, 60000) + 50) / 100, 2),
       MOD(n, 500),
       IF(MOD(n, 11) <> 0, 1, 0),
       JSON_ARRAY(CONCAT('cat', MOD(n, 8)), CONCAT('brand', MOD(n, 20))),
       JSON_OBJECT('color', ELT(1 + MOD(n, 6), 'red','blue','green','black','white','银色'),
                   'weight_g', MOD(n, 5000),
                   'dimensions', JSON_OBJECT('w', 1 + MOD(n, 100), 'h', 1 + MOD(n, 80)),
                   'battery', MOD(n, 3) = 0),
       CASE WHEN MOD(n, 10) = 0 THEN UNHEX(MD5(CONCAT('img', n))) ELSE NULL END,
       DATE_ADD('2023-06-01 00:00:00', INTERVAL MOD(n, 500) DAY)
FROM _nums WHERE n < 40000;

-- =====================================================================
-- 3. orders (300k, partitioned InnoDB)
-- =====================================================================
CREATE TABLE orders (
    id               BIGINT UNSIGNED NOT NULL,
    customer_id      BIGINT UNSIGNED NOT NULL,
    order_no         VARCHAR(32) NOT NULL,
    status           ENUM('pending','paid','shipped','delivered','cancelled','refunded') NOT NULL DEFAULT 'pending',
    total            DECIMAL(14,2) NOT NULL DEFAULT 0.00,
    currency         CHAR(3) NOT NULL DEFAULT 'USD',
    ordered_at       DATETIME(6) NOT NULL,
    shipping_address JSON,
    notes            TEXT,
    created_at       DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_orders_order_no (order_no),
    KEY ix_orders_customer (customer_id, ordered_at),
    KEY ix_orders_status (status, ordered_at),
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
ALTER TABLE orders COMMENT = '300k orders; FK to customers; partitioned sibling table order_events holds RANGE partitions';

INSERT INTO orders (id, customer_id, order_no, status, total, currency, ordered_at, shipping_address, notes, created_at)
SELECT n + 1,
       1 + MOD(n * 37, 150000),
       CONCAT('ORD-', LPAD(n + 1, 10, '0')),
       ELT(1 + MOD(n, 6), 'pending','paid','shipped','delivered','cancelled','refunded'),
       ROUND((MOD(n * 7, 500000) + 500) / 100, 2),
       ELT(1 + MOD(n, 6), 'USD','USD','USD','EUR','CNY','JPY'),
       TIMESTAMPADD(SECOND, n * 63, '2024-01-01 00:00:00'),
       JSON_OBJECT('line1', CONCAT(MOD(n, 300), ' Main Street'),
                   'city', ELT(1 + MOD(n, 6), 'Springfield','上海','Tokyo','München','São Paulo','Mumbai'),
                   'country', ELT(1 + MOD(n, 6), 'US','CN','JP','DE','BR','IN'),
                   'postal', LPAD(MOD(n * 13, 99999), 5, '0')),
       CASE WHEN MOD(n, 100) = 0 THEN '礼盒包装 🎁 请小心轻放'
            WHEN MOD(n, 37) = 0 THEN 'Leave at the front door' ELSE NULL END,
       TIMESTAMPADD(SECOND, n * 63 + 300, '2024-01-01 00:00:00')
FROM _nums WHERE n < 300000;

-- =====================================================================
-- 4. order_items (800k)
-- =====================================================================
CREATE TABLE order_items (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    order_id   BIGINT UNSIGNED NOT NULL,
    product_id BIGINT UNSIGNED NOT NULL,
    quantity   INT NOT NULL,
    unit_price DECIMAL(12,2) NOT NULL,
    discount   DECIMAL(5,2) NOT NULL DEFAULT 0.00,
    line_total DECIMAL(14,2) GENERATED ALWAYS AS (ROUND(quantity * unit_price * (1 - discount / 100), 2)) STORED,
    KEY ix_order_items_order (order_id),
    KEY ix_order_items_product (product_id),
    KEY ix_order_items_total (line_total),
    CONSTRAINT fk_items_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
    CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE CASCADE,
    CONSTRAINT ck_items_qty CHECK (quantity BETWEEN 1 AND 50),
    CONSTRAINT ck_items_discount CHECK (discount BETWEEN 0 AND 100)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
ALTER TABLE order_items COMMENT = '800k order lines; stored generated line_total; FK to orders/products';

-- =====================================================================
-- 4b. order_events (400k, partitioned InnoDB by RANGE(id))
-- =====================================================================
CREATE TABLE order_events (
    id          BIGINT UNSIGNED NOT NULL,
    order_id    BIGINT UNSIGNED NOT NULL,
    event_type  VARCHAR(32) NOT NULL,
    payload     JSON,
    occurred_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY ix_order_events_order (order_id),
    KEY ix_order_events_type (event_type, occurred_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
PARTITION BY RANGE (id) (
    PARTITION p0 VALUES LESS THAN (100001),
    PARTITION p1 VALUES LESS THAN (200001),
    PARTITION p2 VALUES LESS THAN (300001),
    PARTITION p3 VALUES LESS THAN MAXVALUE
);
ALTER TABLE order_events COMMENT = '400k order lifecycle events; InnoDB table partitioned by RANGE(id)';

INSERT INTO order_events (id, order_id, event_type, payload, occurred_at)
SELECT n + 1,
       1 + MOD(n, 300000),
       ELT(1 + MOD(n, 8), 'created','paid','packed','shipped','delivered','refunded','cancelled','noted'),
       JSON_OBJECT('actor', ELT(1 + MOD(n, 3), 'customer','system','ops'),
                   'channel', ELT(1 + MOD(n, 4), 'web','app','api','batch'),
                   'note', CASE WHEN MOD(n, 50) = 0 THEN '跨境订单 🚚' ELSE NULL END),
       TIMESTAMPADD(SECOND, n * 47, '2024-01-01 00:00:00')
FROM _nums WHERE n < 400000;

INSERT INTO order_items (order_id, product_id, quantity, unit_price, discount)
SELECT 1 + MOD(n, 300000),
       1 + MOD(n * 7, 40000),
       1 + MOD(n, 10),
       ROUND((MOD(n, 90000) + 100) / 100, 2),
       MOD(n, 5) * 2
FROM _nums WHERE n < 800000;

-- =====================================================================
-- 5. payments (300k)
-- =====================================================================
CREATE TABLE payments (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    order_id     BIGINT UNSIGNED NOT NULL,
    method       ENUM('card','paypal','bank_transfer','crypto','gift_card') NOT NULL,
    amount       DECIMAL(14,2) NOT NULL,
    provider_ref VARCHAR(64) NOT NULL,
    paid_at      DATETIME(6) NOT NULL,
    raw_response JSON,
    UNIQUE KEY uq_payments_provider_ref (provider_ref),
    KEY ix_payments_paid_at (paid_at),
    KEY ix_payments_order (order_id),
    CONSTRAINT fk_payments_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO payments (order_id, method, amount, provider_ref, paid_at, raw_response)
SELECT n + 1,
       ELT(1 + MOD(n, 5), 'card','paypal','bank_transfer','crypto','gift_card'),
       ROUND((MOD(n * 7, 500000) + 500) / 100, 2),
       CONCAT('pay_', n + 1),
       TIMESTAMPADD(SECOND, n * 63 + 120, '2024-01-01 00:00:00'),
       JSON_OBJECT('status', ELT(1 + MOD(n, 4), 'succeeded','succeeded','succeeded','pending'),
                   'provider', ELT(1 + MOD(n, 3), 'stripe','paypal','adyen'),
                   'fee_cents', MOD(n * 3, 500),
                   'risk_score', MOD(n, 100))
FROM _nums WHERE n < 300000;

-- =====================================================================
-- 6. Audit table + trigger
-- =====================================================================
CREATE TABLE order_audit (
    audit_id   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    order_id   BIGINT UNSIGNED NOT NULL,
    old_status VARCHAR(16),
    new_status VARCHAR(16),
    changed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    changed_by VARCHAR(64) NOT NULL DEFAULT 'unknown'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DELIMITER //
CREATE TRIGGER trg_orders_audit_status
AFTER UPDATE ON orders
FOR EACH ROW
BEGIN
    IF OLD.status <> NEW.status THEN
        INSERT INTO order_audit (order_id, old_status, new_status, changed_by)
        VALUES (NEW.id, OLD.status, NEW.status, CURRENT_USER());
    END IF;
END//
DELIMITER ;

-- =====================================================================
-- 7. Small reference tables, reserved words, views, routines
-- =====================================================================
CREATE TABLE categories (
    id   INT NOT NULL PRIMARY KEY,
    code VARCHAR(32) NOT NULL,
    name VARCHAR(100) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO categories VALUES
    (1,'electronics','电子产品'),(2,'clothing','服装'),(3,'books','Books'),(4,'home','Home & Garden'),
    (5,'sports','Sports'),(6,'toys','Toys'),(7,'food','Food'),(8,'beauty','Beauty');

-- Reserved-word identifiers (table and columns require backticks)
CREATE TABLE `order` (
    `select` INT NOT NULL PRIMARY KEY,
    `from`   VARCHAR(20),
    `where`  VARCHAR(20),
    `group`  VARCHAR(20),
    `rank`   INT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO `order` VALUES (1,'upstream','active','A',1),(2,'downstream','inactive','B',2),(3,'sideways','active','A',3);

CREATE TABLE empty_table (
    id   BIGINT NOT NULL PRIMARY KEY,
    name VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE VIEW v_customer_summary AS
SELECT c.id, c.email, c.full_name, c.status, COUNT(o.id) AS order_count, COALESCE(SUM(o.total),0) AS lifetime_value
FROM customers c
LEFT JOIN orders o ON o.customer_id = c.id
GROUP BY c.id, c.email, c.full_name, c.status;

CREATE VIEW v_product_sales AS
SELECT p.id, p.sku, p.name, p.category, SUM(oi.quantity) AS units_sold, SUM(oi.line_total) AS revenue
FROM products p
JOIN order_items oi ON oi.product_id = p.id
GROUP BY p.id, p.sku, p.name, p.category;

CREATE VIEW v_order_totals AS
SELECT o.id, o.order_no, o.status, o.total, COUNT(oi.id) AS line_count, SUM(oi.line_total) AS lines_total
FROM orders o
LEFT JOIN order_items oi ON oi.order_id = o.id
GROUP BY o.id, o.order_no, o.status, o.total;

DELIMITER //
CREATE PROCEDURE sp_customer_orders(IN in_customer_id BIGINT UNSIGNED)
BEGIN
    SELECT o.id, o.order_no, o.status, o.total, o.ordered_at
    FROM orders o
    WHERE o.customer_id = in_customer_id
    ORDER BY o.ordered_at DESC
    LIMIT 100;
END//

CREATE FUNCTION fn_customer_total(in_customer_id BIGINT UNSIGNED)
RETURNS DECIMAL(14,2)
DETERMINISTIC
READS SQL DATA
BEGIN
    DECLARE v_total DECIMAL(14,2);
    SELECT COALESCE(SUM(total), 0) INTO v_total FROM orders WHERE customer_id = in_customer_id;
    RETURN v_total;
END//
DELIMITER ;

-- Fire the trigger a few times so order_audit is non-empty
UPDATE orders SET status = 'shipped' WHERE id <= 20 AND status <> 'shipped';
UPDATE orders SET status = 'delivered' WHERE id BETWEEN 21 AND 40 AND status <> 'delivered';

ANALYZE TABLE customers, products, orders, order_items, payments, order_audit, categories;

DROP TABLE _nums;
DROP TABLE _digits;

SELECT 'mysql_shop load complete' AS status;
