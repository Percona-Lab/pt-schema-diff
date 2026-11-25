-- ptdbtest on the TARGET server (arunpxcnd1, 10.30.51.106): an older structure that drifted.
-- customers is small, so its changes get no pt-online-schema-change advice.
CREATE TABLE `customers` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `email` varchar(320) NOT NULL,
  `full_name` varchar(200) NOT NULL DEFAULT '',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,         -- country missing
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_email` (`email`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- orders gets 1.2M rows, and its ALTER copies the table, so the report adds a pt-online-schema-change command.
CREATE TABLE `orders` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,                        -- source: bigint unsigned (copies the table)
  `customer_id` int unsigned NOT NULL,
  `status` enum('new','paid','shipped','cancelled') NOT NULL DEFAULT 'new', -- source appends 'refunded' (instant)
  `amount` decimal(10,2) NOT NULL DEFAULT '0.00',                   -- source: decimal(12,2) (copies the table)
  `note` varchar(100) DEFAULT NULL,                                 -- source: varchar(255)
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,         -- currency and updated_at missing
  PRIMARY KEY (`id`),
  KEY `idx_customer` (`customer_id`)                                -- source: (customer_id, created_at) and idx_status
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
