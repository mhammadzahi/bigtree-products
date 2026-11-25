-- 002_filters.sql — data needed by the category pages, filters and cards.
--
--   go run ./cmd/migrate migrations/002_filters.sql
--
-- Safe to re-run (MySQL 8 has no ADD COLUMN IF NOT EXISTS, so the ALTER runs
-- only when the column is missing). Run it BEFORE deploying the matching
-- fill_prodcut.go, then run a `full` sync so every product gets the new
-- fields and its collections.

-- WooCommerce post status (publish | private), catalog visibility
-- (visible | catalog | search | hidden) and gallery images (JSON array of the
-- image URLs after the first; the first one is the card's hover image).
SET @need := (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
              WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'products' AND COLUMN_NAME = 'status');
SET @sql := IF(@need,
  'ALTER TABLE `products`
     ADD COLUMN `status`             VARCHAR(20) NOT NULL DEFAULT ''publish'' AFTER `parent_id`,
     ADD COLUMN `catalog_visibility` VARCHAR(20) NOT NULL DEFAULT ''visible'' AFTER `status`,
     ADD COLUMN `gallery`            TEXT        DEFAULT NULL               AFTER `catalog_visibility`',
  'SELECT 1');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- Until now WooCommerce product tags were stored as type 'collection'. That
-- type now holds the real WordPress `collection` taxonomy (Selection >
-- Collection tree), so the tag rows become type 'tag'. This runs only while
-- no 'tag' row exists yet, so a re-run never retypes real collections.
SET @have_tags := (SELECT COUNT(*) FROM `taxonomies` WHERE `type` = 'tag');
UPDATE `taxonomies` SET `type` = 'tag' WHERE `type` = 'collection' AND @have_tags = 0;
