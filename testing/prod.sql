-- MySQL dump (source: production)
/*!40101 SET NAMES utf8mb4 */;
CREATE TABLE `customers` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `email` varchar(320) COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `full_name` varchar(200) NOT NULL DEFAULT '',
  `status` enum('active','paused','closed','deleted') NOT NULL DEFAULT 'active',
  `balance` decimal(12,2) NOT NULL DEFAULT '0.00',
  `notes` text,
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `country` char(2) CHARACTER SET ascii NOT NULL DEFAULT 'US',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_email` (`email`),
  KEY `idx_status_created` (`status`,`created_at`),
  KEY `idx_name` (`full_name`(50)) INVISIBLE,
  FULLTEXT KEY `ft_notes` (`notes`),
  CONSTRAINT `chk_balance` CHECK ((`balance` >= 0))
) ENGINE=InnoDB AUTO_INCREMENT=1001 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Customer accounts';

CREATE TABLE `orders` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` int unsigned NOT NULL,
  `amount` decimal(10,2) NOT NULL,
  `state` varchar(20) NOT NULL DEFAULT 'new',
  `ref_code` varchar(64) DEFAULT NULL,
  `total_cents` bigint GENERATED ALWAYS AS ((`amount` * 100)) STORED,
  PRIMARY KEY (`id`),
  KEY `idx_customer` (`customer_id`),
  KEY `idx_ref` (`ref_code`),
  CONSTRAINT `fk_orders_customer` FOREIGN KEY (`customer_id`) REFERENCES `customers` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE `audit_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `order_id` bigint unsigned NOT NULL,
  `message` varchar(255) NOT NULL,
  PRIMARY KEY (`id`),
  FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `events` (
  `id` int NOT NULL,
  `happened` date NOT NULL,
  PRIMARY KEY (`id`,`happened`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
/*!50100 PARTITION BY RANGE (year(`happened`))
(PARTITION p2025 VALUES LESS THAN (2026) ENGINE = InnoDB,
 PARTITION pmax VALUES LESS THAN MAXVALUE ENGINE = InnoDB) */;

CREATE TABLE settings (
  k varchar(64) PRIMARY KEY,
  v json,
  scope varchar(20) UNIQUE
);
