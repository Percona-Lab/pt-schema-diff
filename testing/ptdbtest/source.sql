-- ptdbtest on the SOURCE server (arundb2, 10.30.51.44): the structure we want.
CREATE TABLE `customers` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `email` varchar(320) NOT NULL,
  `full_name` varchar(200) NOT NULL DEFAULT '',
  `country` char(2) NOT NULL DEFAULT 'US',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_email` (`email`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `orders` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` int unsigned NOT NULL,
  `status` enum('new','paid','shipped','cancelled','refunded') NOT NULL DEFAULT 'new',
  `amount` decimal(12,2) NOT NULL DEFAULT '0.00',
  `currency` char(3) NOT NULL DEFAULT 'USD',
  `note` varchar(255) DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_customer_created` (`customer_id`,`created_at`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
