-- mariadb_legacy.sql -- MariaDB 11.4 legacy database for TDB object-browser tests.
-- Creates the messy `legacy` database plus 6 small side databases (12 databases total
-- on the server including information_schema/mysql/performance_schema/sys/shop).
-- Idempotent: drops and recreates every database it owns.
SET NAMES utf8mb4;
SET SESSION sql_mode = 'STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION';

-- =====================================================================
-- legacy
-- =====================================================================
DROP DATABASE IF EXISTS legacy;
CREATE DATABASE legacy CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy;

CREATE TABLE `MixedCaseTable` (
    id         INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `FirstName` VARCHAR(50) NOT NULL,
    `LastName`  VARCHAR(50) NOT NULL,
    `Email`     VARCHAR(120) UNIQUE,
    `CreatedAt` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `IsActive`  TINYINT(1) NOT NULL DEFAULT 1
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Mixed-case table and column names';

INSERT INTO `MixedCaseTable` (`FirstName`, `LastName`, `Email`)
SELECT CONCAT('First', n), CONCAT('Last', n), CONCAT('mixed', n, '@legacy.example.com')
FROM (SELECT a.n + b.n*10 + 1 AS n FROM
      (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
       UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) a,
      (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2) b) AS s;

CREATE TABLE `Order Details` (
    `OrderID`   INT NOT NULL,
    `ProductID` INT NOT NULL,
    `Qty`       SMALLINT NOT NULL,
    `UnitPrice` DECIMAL(10,2) NOT NULL,
    PRIMARY KEY (`OrderID`, `ProductID`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Table name and columns with spaces';

INSERT INTO `Order Details` VALUES
    (1, 101, 2, 9.99), (1, 102, 1, 19.50), (2, 101, 5, 9.99),
    (2, 103, 3, 4.25), (3, 104, 1, 99.00);

CREATE TABLE `order` (
    `select` INT NOT NULL PRIMARY KEY,
    `from`   VARCHAR(20),
    `where`  VARCHAR(20),
    `group`  VARCHAR(20),
    `rank`   INT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Reserved-word table and column names';

INSERT INTO `order` VALUES
    (1, 'warehouse', 'active', 'A', 1),
    (2, 'customer', 'inactive', 'B', 2),
    (3, 'supplier', 'active', 'A', 3);

CREATE TABLE `EmptyTable` (
    id    BIGINT NOT NULL PRIMARY KEY,
    label VARCHAR(100)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Deliberately empty (0 rows)';

CREATE TABLE `DataTypes` (
    id          INT NOT NULL PRIMARY KEY,
    bit_col     BIT(8),
    tiny_col    TINYINT,
    small_col   SMALLINT,
    medium_col  MEDIUMINT,
    int_col     INT,
    big_col     BIGINT,
    dec_col     DECIMAL(18,4),
    float_col   FLOAT,
    double_col  DOUBLE,
    date_col    DATE,
    time_col    TIME(6),
    dt_col      DATETIME(6),
    ts_col      TIMESTAMP NULL,
    year_col    YEAR,
    char_col    CHAR(4),
    vchar_col   VARCHAR(64),
    bin_col     BINARY(4),
    vbin_col    VARBINARY(64),
    blob_col    BLOB,
    text_col    TEXT,
    enum_col    ENUM('one','two','three'),
    set_col     SET('a','b','c'),
    json_col    JSON,
    geom_col    POINT,
    bool_col    TINYINT(1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Wide variety of MySQL types';

INSERT INTO `DataTypes` VALUES
    (1, b'10101010', 7, 1234, 56789, 123456, 9007199254740993, 12345.6789, 1.5, 2.25,
     '2024-05-01', '13:45:12.123456', '2024-05-01 13:45:12.123456', '2024-05-01 13:45:12',
     2024, 'ABCD', 'varchar value 你好', 0xDEADBEEF, 0x0102030405, 0xDEADBEEF, 'text 汉字段',
     'two', 'a,c', JSON_OBJECT('k','v','n',1), ST_GeomFromText('POINT(1 2)'), 1),
    (2, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
     NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
     NULL, NULL, NULL, NULL, NULL);

CREATE TABLE `Legacy Customers` (
    id          INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `Full Name` VARCHAR(100) NOT NULL,
    email       VARCHAR(120),
    legacy_flag CHAR(1) NOT NULL DEFAULT 'N',
    notes       TEXT,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO `Legacy Customers` (`Full Name`, email, legacy_flag, notes) VALUES
    ('Alice Smith', 'alice@legacy.example.com', 'N', 'migrated 2019'),
    ('Bob Jones', 'bob@legacy.example.com', 'Y', NULL),
    ('王小明', 'wang@legacy.example.com', 'N', '中文客户 😀'),
    ('Chloé Martin', 'chloe@legacy.example.com', 'Y', 'données anciennes'),
    ('Иван Петров', 'ivan@legacy.example.com', 'N', NULL);

CREATE TABLE `FK Parent` (
    id   INT NOT NULL PRIMARY KEY,
    name VARCHAR(50) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO `FK Parent` VALUES (1,'parent one'), (2,'parent two'), (3,'parent three');

CREATE TABLE `FK Child` (
    id        INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    parent_id INT NOT NULL,
    payload   JSON,
    CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES `FK Parent` (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO `FK Child` (parent_id, payload)
SELECT 1 + MOD(n, 3), JSON_OBJECT('n', n)
FROM (SELECT a.n + b.n*10 + 1 AS n FROM
      (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
       UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) a,
      (SELECT 0 n UNION ALL SELECT 1) b) AS s;

CREATE VIEW `Legacy View` AS
SELECT c.id, c.`Full Name`, c.legacy_flag, COUNT(ch.id) AS child_count
FROM `Legacy Customers` c
LEFT JOIN `FK Child` ch ON ch.id = c.id
GROUP BY c.id, c.`Full Name`, c.legacy_flag;

-- =====================================================================
-- 6 side databases (object-browser filter tests)
-- =====================================================================
DROP DATABASE IF EXISTS legacy_2019;
CREATE DATABASE legacy_2019 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy_2019;
CREATE TABLE archive_orders (id INT NOT NULL PRIMARY KEY, placed_at DATE, total DECIMAL(10,2)) ENGINE=InnoDB;
INSERT INTO archive_orders VALUES (1,'2019-01-01',12.34),(2,'2019-02-03',99.00);
CREATE TABLE archive_notes (id INT NOT NULL PRIMARY KEY, note VARCHAR(200)) ENGINE=InnoDB;

DROP DATABASE IF EXISTS legacy_2020;
CREATE DATABASE legacy_2020 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy_2020;
CREATE TABLE `2020 Orders` (id INT NOT NULL PRIMARY KEY, total DECIMAL(10,2)) ENGINE=InnoDB;
INSERT INTO `2020 Orders` VALUES (1,1.00),(2,2.00);

DROP DATABASE IF EXISTS legacy_eu;
CREATE DATABASE legacy_eu CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy_eu;
CREATE TABLE customers_eu (id INT NOT NULL PRIMARY KEY, country CHAR(2), name VARCHAR(100)) ENGINE=InnoDB;
INSERT INTO customers_eu VALUES (1,'DE','Müller GmbH'),(2,'FR','Société Générale'),(3,'ES','Compañía 西班牙');

DROP DATABASE IF EXISTS legacy_us;
CREATE DATABASE legacy_us CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy_us;
CREATE TABLE customers_us (id INT NOT NULL PRIMARY KEY, state CHAR(2), name VARCHAR(100)) ENGINE=InnoDB;
INSERT INTO customers_us VALUES (1,'CA','Acme Inc'),(2,'NY','Globex LLC');

DROP DATABASE IF EXISTS legacy_apac;
CREATE DATABASE legacy_apac CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy_apac;
CREATE TABLE customers_apac (id INT NOT NULL PRIMARY KEY, country CHAR(2), name VARCHAR(100)) ENGINE=InnoDB;
INSERT INTO customers_apac VALUES (1,'CN','上海贸易有限公司'),(2,'JP','東京商事'),(3,'KR','서울상사');

DROP DATABASE IF EXISTS legacy_archive;
CREATE DATABASE legacy_archive CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE legacy_archive;
CREATE TABLE snapshots (id BIGINT NOT NULL PRIMARY KEY, taken_at DATETIME(6), payload JSON) ENGINE=InnoDB;
INSERT INTO snapshots VALUES (1,'2024-01-01 00:00:00',JSON_OBJECT('rows',100)),(2,'2024-02-01 00:00:00',JSON_OBJECT('rows',200));
CREATE TABLE empty_snapshot (id BIGINT NOT NULL PRIMARY KEY) ENGINE=InnoDB;

SELECT 'mariadb_legacy load complete' AS status;
