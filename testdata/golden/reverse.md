# Schema diff

- **Source** (the schema to match): S
- **Target** (the schema the script changes): T
- **Generated:** 2026-01-02 03:04

6 tables: 1 to create, 4 to change, 1 only in the target, 0 identical. Changes: 10 critical, 7 warnings, 9 suggestions, 6 safe.

## `audit_log` — drop

- 🛑 **Drop table `audit_log`.** It exists only in the target. Everything stored in it is deleted for good. The script leaves this commented out unless --include-drops is given.

```sql
-- DROP TABLE `audit_log`;
```

## `customers` — alter (MySQL: blocks writes)

- 🛑 **Change column `id`: `int unsigned` → `int`.** Values outside the new range are rejected (strict mode) or clipped. Check first: `SELECT MIN(id), MAX(id) FROM customers`. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- 🛑 **Change column `email`: `varchar(320)` → `varchar(255)`, character set utf8mb4 → utf8mb3.** Values longer than 255 characters are rejected (strict mode) or cut off. Check first: `SELECT MAX(CHAR_LENGTH(email)) FROM customers`. Characters the new character set can't store (for example emoji) are replaced or rejected. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- ⚠️ **Rename column `full_name` → `name`.** The data is kept. Treated as a rename because `full_name` exists only in the target, `name` only in the source, and both have the same definition. If they are really two different columns, replace this CHANGE COLUMN with a DROP COLUMN and an ADD COLUMN. MySQL only updates the table definition, so it is instant. *MySQL: instant.*
- 🛑 **Change column `status`: `enum('active','paused','closed','deleted')` → `enum('active','paused','closed')`.** Removes 'deleted': rows holding them become '' (or the ALTER fails in strict mode). MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- 🛑 **Change column `balance`: `decimal(12,2)` → `decimal(10,2)`.** Fewer digits: values that don't fit are rounded or rejected. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- ⚠️ **Change column `notes`: `text` → `mediumtext`.** Safe for the data. The limit only gets bigger. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- ⚠️ **Change column `created_at`: `datetime(3)` → `datetime`, default current_timestamp(3) → current_timestamp.** Fewer fractional-second digits: stored values are rounded. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- ✅ **Add column `legacy_flag` `tinyint` after `updated_at`.** No existing data changes. Instant on MySQL 8.0.29 and later. Older versions rebuild the table (reads and writes continue), because the column goes in the middle of the table. *MySQL: instant.*
- ✅ **Make index `idx_name` visible.** The optimizer can use it again. MySQL only updates the table definition, so it is instant. *MySQL: instant.*
- 💡 **Drop index `uq_email` (`email`).** No data is lost, but queries that use this index may get slower. To test first, hide it with `ALTER TABLE customers ALTER INDEX uq_email INVISIBLE` and watch performance. MySQL only updates the table definition (no rebuild); reads and writes continue. *MySQL: instant.*
- 💡 **Drop index `idx_status_created` (`status`, `created_at`).** No data is lost, but queries that use this index may get slower. To test first, hide it with `ALTER TABLE customers ALTER INDEX idx_status_created INVISIBLE` and watch performance. MySQL only updates the table definition (no rebuild); reads and writes continue. *MySQL: instant.*
- 💡 **Drop index `ft_notes` (`notes`).** No data is lost, but queries that use this index may get slower. To test first, hide it with `ALTER TABLE customers ALTER INDEX ft_notes INVISIBLE` and watch performance. MySQL only updates the table definition (no rebuild); reads and writes continue. *MySQL: instant.*
- 💡 **Add index `email_idx` (`email`).** No data changes. Reads and writes continue while it builds; the build reads the whole table, so it takes a while on a big one. *MySQL: online.*
- 💡 **Add index `idx_status` (`status`).** No data changes. Reads and writes continue while it builds; the build reads the whole table, so it takes a while on a big one. *MySQL: online.*
- 💡 **Drop CHECK constraint `chk_balance` `CHECK ((balance >= 0))`.** No data is lost, but MySQL stops enforcing the rule. MySQL only updates the table definition, so it is instant. *MySQL: instant.*
- ✅ **Change table options: comment 'Customer accounts' → ''.** No data changes. MySQL only updates the table definition, so it is instant. *MySQL: instant.*

```sql
ALTER TABLE `customers`
  MODIFY COLUMN `id` int NOT NULL AUTO_INCREMENT,
  MODIFY COLUMN `email` varchar(255) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci NOT NULL,
  CHANGE COLUMN `full_name` `name` varchar(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT '',
  MODIFY COLUMN `status` enum('active','paused','closed') CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT 'active',
  MODIFY COLUMN `balance` decimal(10,2) NOT NULL DEFAULT '0.00',
  MODIFY COLUMN `notes` mediumtext CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci,
  MODIFY COLUMN `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ADD COLUMN `legacy_flag` tinyint(1) DEFAULT NULL AFTER `updated_at`,
  ALTER INDEX `idx_name` VISIBLE,
  DROP INDEX `uq_email`,
  DROP INDEX `idx_status_created`,
  DROP INDEX `ft_notes`,
  ADD INDEX `email_idx` (`email`),
  ADD INDEX `idx_status` (`status`),
  DROP CHECK `chk_balance`,
  COMMENT='';
```

```diff
--- target
+++ source
@@ -1,17 +1,16 @@
 CREATE TABLE `customers` (
-  `id` int unsigned NOT NULL AUTO_INCREMENT,
-  `email` varchar(320) COLLATE utf8mb4_0900_ai_ci NOT NULL,
-  `full_name` varchar(200) NOT NULL DEFAULT '',
-  `status` enum('active','paused','closed','deleted') NOT NULL DEFAULT 'active',
-  `balance` decimal(12,2) NOT NULL DEFAULT '0.00',
-  `notes` text,
-  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
+  `id` int NOT NULL AUTO_INCREMENT,
+  `email` varchar(255) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci NOT NULL,
+  `name` varchar(200) NOT NULL DEFAULT '',
+  `status` enum('active','paused','closed') NOT NULL DEFAULT 'active',
+  `balance` decimal(10,2) NOT NULL DEFAULT '0.00',
+  `notes` mediumtext,
+  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
   `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
+  `legacy_flag` tinyint(1) DEFAULT NULL,
   `country` char(2) CHARACTER SET ascii NOT NULL DEFAULT 'US',
   PRIMARY KEY (`id`),
-  UNIQUE KEY `uq_email` (`email`),
-  KEY `idx_status_created` (`status`,`created_at`),
-  KEY `idx_name` (`full_name`(50)) INVISIBLE,
-  FULLTEXT KEY `ft_notes` (`notes`),
-  CONSTRAINT `chk_balance` CHECK ((`balance` >= 0))
-) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Customer accounts'
+  KEY `email_idx` (`email`),
+  KEY `idx_status` (`status`),
+  KEY `idx_name` (`name`(50))
+) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
```

## `orders` — alter (MySQL: blocks writes)

- 🛑 **Change column `customer_id`: `int unsigned` → `int`.** Values outside the new range are rejected (strict mode) or clipped. Check first: `SELECT MIN(customer_id), MAX(customer_id) FROM orders`. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- 🛑 **Change column `state`: `varchar(20)` → `varchar(10)`, NOT NULL → NULL allowed, character set utf8mb4 → latin1, default 'new' → none.** Values longer than 10 characters are rejected (strict mode) or cut off. Check first: `SELECT MAX(CHAR_LENGTH(state)) FROM orders`. Safe for the data. Characters the new character set can't store (for example emoji) are replaced or rejected. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- 🛑 **Change column `ref_code`: `varchar(64)` → `varchar(32)`, character set utf8mb4 → latin1.** Values longer than 32 characters are rejected (strict mode) or cut off. Check first: `SELECT MAX(CHAR_LENGTH(ref_code)) FROM orders`. Characters the new character set can't store (for example emoji) are replaced or rejected. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*
- 🛑 **Drop column `total_cents` `bigint`.** Everything stored in this column is deleted for good. It exists only in the target. The script leaves this commented out unless --include-drops is given. Instant on MySQL 8.0.29 and later; older versions rebuild the table. *MySQL: instant.*
- ✅ **Rename index `idx_customer` → `customer_id`.** Same columns; only the name changes. MySQL only updates the table definition (no rebuild); reads and writes continue. *MySQL: instant.*
- ✅ **Rename index `idx_ref` → `idx_ref_old`.** Same columns; only the name changes. MySQL only updates the table definition (no rebuild); reads and writes continue. *MySQL: instant.*
- 💡 **Drop foreign key `fk_orders_customer` (customer_id → `customers`).** No data is lost, but MySQL stops checking that customer_id values exist in `customers`. MySQL only updates the table definition, so it is instant. *MySQL: instant.*
- ⚠️ **Add foreign key `orders_ibfk_1` (customer_id → `customers`).** The ALTER fails if any existing row points at a row missing from `customers`. MySQL checks every row and blocks writes to the table while it runs. *MySQL: blocks writes.*
- ⚠️ **Change table options: engine innodb → myisam; default character set utf8mb4 / utf8mb4_0900_ai_ci → latin1 / default; row_format dynamic → default.** The default character set applies only to columns added later; existing columns are changed by their own MODIFY COLUMN above. MySQL copies the whole table and blocks writes to it until the copy finishes. *MySQL: blocks writes.*

```sql
ALTER TABLE `orders`
  DROP FOREIGN KEY `fk_orders_customer`;

ALTER TABLE `orders`
  MODIFY COLUMN `customer_id` int NOT NULL,
  MODIFY COLUMN `state` varchar(10) CHARACTER SET latin1 NULL,
  MODIFY COLUMN `ref_code` varchar(32) CHARACTER SET latin1 DEFAULT NULL,
  RENAME INDEX `idx_customer` TO `customer_id`,
  RENAME INDEX `idx_ref` TO `idx_ref_old`,
  ENGINE=MYISAM, DEFAULT CHARSET=latin1, ROW_FORMAT=DEFAULT;

-- ALTER TABLE `orders`
--   DROP COLUMN `total_cents`;

ALTER TABLE `orders`
  ADD CONSTRAINT `orders_ibfk_1` FOREIGN KEY (`customer_id`) REFERENCES `customers` (`id`);
```

```diff
--- target
+++ source
@@ -1,12 +1,11 @@
 CREATE TABLE `orders` (
   `id` bigint unsigned NOT NULL AUTO_INCREMENT,
-  `customer_id` int unsigned NOT NULL,
+  `customer_id` int NOT NULL,
   `amount` decimal(10,2) NOT NULL,
-  `state` varchar(20) NOT NULL DEFAULT 'new',
-  `ref_code` varchar(64) DEFAULT NULL,
-  `total_cents` bigint GENERATED ALWAYS AS ((`amount` * 100)) STORED,
+  `state` varchar(10) NULL,
+  `ref_code` varchar(32) DEFAULT NULL,
   PRIMARY KEY (`id`),
-  KEY `idx_customer` (`customer_id`),
-  KEY `idx_ref` (`ref_code`),
-  CONSTRAINT `fk_orders_customer` FOREIGN KEY (`customer_id`) REFERENCES `customers` (`id`) ON DELETE CASCADE
-) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
+  KEY `customer_id` (`customer_id`),
+  KEY `idx_ref_old` (`ref_code`),
+  CONSTRAINT `orders_ibfk_1` FOREIGN KEY (`customer_id`) REFERENCES `customers` (`id`)
+) ENGINE=MyISAM DEFAULT CHARSET=latin1
```

## `settings` — alter (MySQL: online)

- 🛑 **Drop column `scope` `varchar(20)`.** Everything stored in this column is deleted for good. It exists only in the target. The script leaves this commented out unless --include-drops is given. Instant on MySQL 8.0.29 and later; older versions rebuild the table. *MySQL: instant.*
- 💡 **Drop index `scope` (`scope`).** No data is lost, but queries that use this index may get slower. To test first, hide it with `ALTER TABLE settings ALTER INDEX scope INVISIBLE` and watch performance. MySQL only updates the table definition (no rebuild); reads and writes continue. *MySQL: instant.*

```sql
ALTER TABLE `settings`
  DROP INDEX `scope`;

-- ALTER TABLE `settings`
--   DROP COLUMN `scope`;
```

```diff
--- target
+++ source
@@ -2,6 +2,4 @@
   `k` varchar(64),
   `v` json,
-  `scope` varchar(20),
-  PRIMARY KEY (`k`),
-  UNIQUE INDEX `scope` (`scope`)
+  PRIMARY KEY (`k`)
 )
```

## `events` — alter (MySQL: online, rebuilds table)

- 💡 **Column order differs (not changed by the script).** Queries that name their columns are not affected, and moving columns would mean copying the whole table.
- ⚠️ **Change the primary key: (`id`, `happened`) → (`id`).** Fails if those columns hold duplicate values or NULLs. MySQL rebuilds the table, because InnoDB stores rows in primary key order: reads and writes continue, but it takes time and disk space on a big table. *MySQL: online, rebuilds table.*
- ⚠️ **Partitioning differs (not changed by the script).** Source: `none`. Target: `PARTITION BY RANGE (year(happened)) (PARTITION p2025 VALUES LESS THAN (2026) ENGINE = InnoDB, PARTITION pmax VALUES LESS THAN MAXVALUE ENGINE = InnoDB)`. Repartitioning copies the whole table, so plan it as a separate change.

```sql
ALTER TABLE `events`
  DROP PRIMARY KEY, ADD PRIMARY KEY (`id`);
```

```diff
--- target
+++ source
@@ -1,8 +1,5 @@
 CREATE TABLE `events` (
+  `happened` date NOT NULL,
   `id` int NOT NULL,
-  `happened` date NOT NULL,
-  PRIMARY KEY (`id`,`happened`)
+  PRIMARY KEY (`id`)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
-  PARTITION BY RANGE (year(`happened`))
-(PARTITION p2025 VALUES LESS THAN (2026) ENGINE = InnoDB,
- PARTITION pmax VALUES LESS THAN MAXVALUE ENGINE = InnoDB)
```

## `old_stuff` — create

- ✅ **Create table `old_stuff` (1 columns, 1 indexes).** It exists only in the source. A new, empty table: nothing else is affected.

```sql
CREATE TABLE `old_stuff` (
  `id` int NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;
```

## Migration script

```sql
-- Schema migration generated by pt-schema-diff, 2026-01-02 03:04
-- Makes: T
-- match:  S
-- Review every statement and test on a copy first. Run during low traffic: some ALTERs copy
-- their table and block writes to it while they run (see the report for which ones).

-- Statements that delete data (DROP TABLE, DROP COLUMN) are commented out. Run with
-- --include-drops to include them.

USE `shop`;

-- Drop foreign keys that change or go away (first, so their indexes and columns can change)

ALTER TABLE `orders`
  DROP FOREIGN KEY `fk_orders_customer`;

-- Create the tables that exist only in the source

CREATE TABLE `old_stuff` (
  `id` int NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;

-- Change existing tables

ALTER TABLE `customers`
  MODIFY COLUMN `id` int NOT NULL AUTO_INCREMENT,
  MODIFY COLUMN `email` varchar(255) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci NOT NULL,
  CHANGE COLUMN `full_name` `name` varchar(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT '',
  MODIFY COLUMN `status` enum('active','paused','closed') CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT 'active',
  MODIFY COLUMN `balance` decimal(10,2) NOT NULL DEFAULT '0.00',
  MODIFY COLUMN `notes` mediumtext CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci,
  MODIFY COLUMN `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ADD COLUMN `legacy_flag` tinyint(1) DEFAULT NULL AFTER `updated_at`,
  ALTER INDEX `idx_name` VISIBLE,
  DROP INDEX `uq_email`,
  DROP INDEX `idx_status_created`,
  DROP INDEX `ft_notes`,
  ADD INDEX `email_idx` (`email`),
  ADD INDEX `idx_status` (`status`),
  DROP CHECK `chk_balance`,
  COMMENT='';

ALTER TABLE `events`
  DROP PRIMARY KEY, ADD PRIMARY KEY (`id`);

ALTER TABLE `orders`
  MODIFY COLUMN `customer_id` int NOT NULL,
  MODIFY COLUMN `state` varchar(10) CHARACTER SET latin1 NULL,
  MODIFY COLUMN `ref_code` varchar(32) CHARACTER SET latin1 DEFAULT NULL,
  RENAME INDEX `idx_customer` TO `customer_id`,
  RENAME INDEX `idx_ref` TO `idx_ref_old`,
  ENGINE=MYISAM, DEFAULT CHARSET=latin1, ROW_FORMAT=DEFAULT;

-- ALTER TABLE `orders`
--   DROP COLUMN `total_cents`;

ALTER TABLE `settings`
  DROP INDEX `scope`;

-- ALTER TABLE `settings`
--   DROP COLUMN `scope`;

-- Drop the tables that exist only in the target

-- DROP TABLE `audit_log`;

-- Add foreign keys (last, so the tables and indexes they need exist)

ALTER TABLE `orders`
  ADD CONSTRAINT `orders_ibfk_1` FOREIGN KEY (`customer_id`) REFERENCES `customers` (`id`);
```
