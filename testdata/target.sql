mysql> SHOW CREATE TABLE customers\G
*************************** 1. row ***************************
       Table: customers
Create Table: CREATE TABLE `customers` (
  `id` int NOT NULL AUTO_INCREMENT,
  `email` varchar(255) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci NOT NULL,
  `name` varchar(200) NOT NULL DEFAULT '',
  `status` enum('active','paused','closed') NOT NULL DEFAULT 'active',
  `balance` decimal(10,2) NOT NULL DEFAULT '0.00',
  `notes` mediumtext,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `legacy_flag` tinyint(1) DEFAULT NULL,
  `country` char(2) CHARACTER SET ascii NOT NULL DEFAULT 'US',
  PRIMARY KEY (`id`),
  KEY `email_idx` (`email`),
  KEY `idx_status` (`status`),
  KEY `idx_name` (`name`(50))
) ENGINE=InnoDB AUTO_INCREMENT=55 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
1 row in set (0.00 sec)

CREATE TABLE `orders` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` int NOT NULL,
  `amount` decimal(10,2) NOT NULL,
  `state` varchar(10) NULL,
  `ref_code` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `customer_id` (`customer_id`),
  KEY `idx_ref_old` (`ref_code`),
  CONSTRAINT `orders_ibfk_1` FOREIGN KEY (`customer_id`) REFERENCES `customers` (`id`)
) ENGINE=MyISAM DEFAULT CHARSET=latin1;

CREATE TABLE `events` (
  `happened` date NOT NULL,
  `id` int NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE `old_stuff` (
  `id` int NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE settings (
  k varchar(64) PRIMARY KEY,
  v json
);
