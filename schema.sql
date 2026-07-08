-- =============================================================================
-- schema.sql — MariaDB 10.11+ DDL for the Bigtree B2B product catalog
--
-- Design goal: hold a de-normalized, de-duplicated mirror of WooCommerce data
-- (wp_posts / wp_postmeta / wp_terms / wp_term_taxonomy / wp_term_relationships)
-- without the runtime cost of WordPress' EAV model. Relational attributes that
-- drive filtering (categories, collections, product attributes) are promoted to
-- a real join table + covering indexes, while free-form/rare meta stays in a
-- narrow key/value side table.
-- =============================================================================

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- -----------------------------------------------------------------------------
-- users — application accounts (independent of the WooCommerce customer table)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS `users`;
CREATE TABLE `users` (
  `id`            VARCHAR(36)  NOT NULL,                    -- UUID v4
  `email`         VARCHAR(255) NOT NULL,
  `password_hash` VARCHAR(255) NOT NULL,                    -- bcrypt
  `role`          ENUM('admin','buyer') NOT NULL DEFAULT 'buyer',
  `created_at`    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_email` (`email`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- sessions — server-side session store backing HTTP-only session cookies
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS `sessions`;
CREATE TABLE `sessions` (
  `token`      CHAR(64)     NOT NULL,                       -- 32 random bytes, hex
  `user_id`    VARCHAR(36)  NOT NULL,
  `created_at` TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `expires_at` TIMESTAMP    NOT NULL,
  PRIMARY KEY (`token`),
  KEY `idx_sessions_user` (`user_id`),
  KEY `idx_sessions_expiry` (`expires_at`),
  CONSTRAINT `fk_sessions_user` FOREIGN KEY (`user_id`)
    REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- products — flat mirror of wp_posts (post_type IN ('product','product_variation'))
--   parent_id mirrors wp_posts.post_parent to preserve variable→variation trees.
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS `products`;
CREATE TABLE `products` (
  `id`                BIGINT UNSIGNED NOT NULL,             -- = wp_posts.ID
  `sku`               VARCHAR(100)    DEFAULT NULL,
  `title`             VARCHAR(255)    NOT NULL,
  `slug`              VARCHAR(255)    NOT NULL,
  `description`       TEXT            DEFAULT NULL,
  `short_description` TEXT            DEFAULT NULL,
  `price`             DECIMAL(10,2)   NOT NULL DEFAULT 0.00,
  `image_url`         VARCHAR(512)    DEFAULT NULL,
  `stock_status`      ENUM('in_stock','out_of_stock') NOT NULL DEFAULT 'in_stock',
  `product_type`      ENUM('simple','variable')       NOT NULL DEFAULT 'simple',
  `parent_id`         BIGINT UNSIGNED DEFAULT NULL,
  `created_at`        TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_products_slug` (`slug`),
  KEY `idx_products_sku` (`sku`),
  KEY `idx_products_parent` (`parent_id`),
  KEY `idx_products_type` (`product_type`),
  KEY `idx_products_price` (`price`),
  -- covers the default archive: top-level products, sorted / filtered by stock
  KEY `idx_products_archive` (`parent_id`, `stock_status`, `price`),
  FULLTEXT KEY `ft_products_search` (`title`, `short_description`),
  CONSTRAINT `fk_products_parent` FOREIGN KEY (`parent_id`)
    REFERENCES `products` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- taxonomies — merges wp_terms + wp_term_taxonomy into one addressable row.
--   `type` names the WooCommerce taxonomy/attribute a term belongs to.
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS `taxonomies`;
CREATE TABLE `taxonomies` (
  `id`          BIGINT UNSIGNED NOT NULL,                   -- = wp_term_taxonomy.term_taxonomy_id
  `name`        VARCHAR(100)    NOT NULL,
  `slug`        VARCHAR(100)    NOT NULL,
  `type`        ENUM('category','tag','collection',
                     'pa_color','pa_size','pa_composition','pa_application') NOT NULL,
  `parent_id`   BIGINT UNSIGNED DEFAULT NULL,               -- hierarchical categories
  `count`       INT UNSIGNED    NOT NULL DEFAULT 0,          -- cached product count
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_taxonomies_type_slug` (`type`, `slug`),
  KEY `idx_taxonomies_type` (`type`),
  KEY `idx_taxonomies_parent` (`parent_id`),
  CONSTRAINT `fk_taxonomies_parent` FOREIGN KEY (`parent_id`)
    REFERENCES `taxonomies` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- product_taxonomy — mirror of wp_term_relationships (many-to-many)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS `product_taxonomy`;
CREATE TABLE `product_taxonomy` (
  `product_id`  BIGINT UNSIGNED NOT NULL,
  `taxonomy_id` BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (`product_id`, `taxonomy_id`),
  -- reverse lookup: "all products in taxonomy X" (the hot filtering path)
  KEY `idx_pt_taxonomy` (`taxonomy_id`, `product_id`),
  CONSTRAINT `fk_pt_product` FOREIGN KEY (`product_id`)
    REFERENCES `products` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_pt_taxonomy` FOREIGN KEY (`taxonomy_id`)
    REFERENCES `taxonomies` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- product_meta — narrow mirror of wp_postmeta for non-relational attributes
--   (weight, dimensions, gsm, minimum order qty, datasheet url, ...)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS `product_meta`;
CREATE TABLE `product_meta` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `product_id` BIGINT UNSIGNED NOT NULL,
  `meta_key`   VARCHAR(255)    NOT NULL,
  `meta_value` LONGTEXT        DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_meta_product` (`product_id`),
  KEY `idx_meta_key` (`meta_key`(191)),
  KEY `idx_meta_lookup` (`product_id`, `meta_key`(191)),
  CONSTRAINT `fk_meta_product` FOREIGN KEY (`product_id`)
    REFERENCES `products` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET FOREIGN_KEY_CHECKS = 1;
