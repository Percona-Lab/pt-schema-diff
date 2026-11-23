-- "Staging": an older copy of the production schema that drifted. Every difference below exercises a rule.
CREATE TABLE `customers` (
  `id` int NOT NULL AUTO_INCREMENT,                                      -- int -> int unsigned (sign change: critical)
  `email` varchar(255) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci NOT NULL, -- charset change, longer
  `name` varchar(200) NOT NULL DEFAULT '',                               -- renamed to full_name in prod
  `status` enum('active','paused','closed') NOT NULL DEFAULT 'active',   -- prod appends 'deleted' (instant)
  `balance` decimal(10,2) NOT NULL DEFAULT '0.00',                       -- prod has more digits
  `notes` mediumtext,                                                    -- prod has text (shrink: critical)
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,              -- prod has fractional seconds
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `legacy_flag` tinyint(1) DEFAULT NULL,                                 -- only in staging: drop column
  `country` char(2) CHARACTER SET ascii NOT NULL DEFAULT 'US',
  PRIMARY KEY (`id`),
  KEY `email_idx` (`email`),                                             -- prod: UNIQUE uq_email instead
  KEY `idx_status` (`status`),                                           -- prod: (status, created_at)
  KEY `idx_name` (`name`(50))                                            -- prod: invisible
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `orders` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` int NOT NULL,                                            -- unsigned in prod
  `amount` decimal(10,2) NOT NULL,
  `state` varchar(10) NULL,                                              -- prod: varchar(20) NOT NULL DEFAULT 'new'
  `ref_code` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `customer_id` (`customer_id`),                                     -- renamed idx_customer in prod
  KEY `idx_ref_old` (`ref_code`)                                         -- renamed idx_ref in prod
) ENGINE=MyISAM DEFAULT CHARSET=latin1;                                  -- engine and charset differ

CREATE TABLE `events` (
  `happened` date NOT NULL,                                              -- column order differs
  `id` int NOT NULL,
  PRIMARY KEY (`id`)                                                     -- primary key differs, no partitioning
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE `old_stuff` (                                               -- only in staging: drop table
  `id` int NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE settings (
  k varchar(64) PRIMARY KEY,
  v json
);

-- Some rows, so the script runs against real data (all of them fit the production definitions).
INSERT INTO customers (email, name, status, balance, notes) VALUES
  ('ann@example.com', 'Ann', 'active', 10.50, 'first customer'),
  ('bob@example.com', 'Bob', 'paused', 0, NULL),
  ('cy@example.com',  'Cy',  'closed', 99.99, 'vip');
INSERT INTO orders (customer_id, amount, state, ref_code) VALUES (1, 25.00, 'paid', 'A1'), (2, 7.50, 'new', NULL);
INSERT INTO events VALUES ('2025-06-01', 1), ('2025-12-31', 2);
INSERT INTO old_stuff VALUES (1);
INSERT INTO settings VALUES ('theme', '"dark"');
