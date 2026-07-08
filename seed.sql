-- =============================================================================
-- seed.sql — realistic sample data for the Bigtree B2B fabric storefront.
--
-- Demonstrates the full model: hierarchical categories, B2B collections, four
-- WooCommerce-style product attributes, variable products with parent→child
-- variations, per-product technical metadata, and a test administrator.
--
-- Run AFTER schema.sql:   mysql bigtree < schema.sql && mysql bigtree < seed.sql
-- =============================================================================

SET FOREIGN_KEY_CHECKS = 0;
TRUNCATE TABLE product_meta;
TRUNCATE TABLE product_taxonomy;
TRUNCATE TABLE products;
TRUNCATE TABLE taxonomies;
TRUNCATE TABLE sessions;
TRUNCATE TABLE users;
SET FOREIGN_KEY_CHECKS = 1;

-- -----------------------------------------------------------------------------
-- Users — test administrator.
--   email:    admin@bigtree-group.com
--   password: password
--   (bcrypt hash below is the canonical hash for the string "password";
--    change it immediately in any real deployment.)
-- -----------------------------------------------------------------------------
INSERT INTO users (id, email, password_hash, role) VALUES
  ('430394e6-39e8-4c29-af84-8567dced996b', 'admin@bigtree-group.com', '$2a$10$jNwvtQHoYlzSrvlHi0rbB.9vxjLBM8p7dtOwt8ZmnlCUdteCLPq8O', 'admin'),
  ('00000000-0000-4000-8000-000000000002', 'buyer@example.com',
   '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', 'buyer');

-- -----------------------------------------------------------------------------
-- Taxonomies
-- -----------------------------------------------------------------------------
-- Categories (id 10-13; 13 is a child of 10 to exercise the hierarchy)
INSERT INTO taxonomies (id, name, slug, type, parent_id) VALUES
  (10, 'Upholstery Fabrics', 'upholstery-fabrics', 'category', NULL),
  (11, 'Curtain Fabrics',    'curtain-fabrics',    'category', NULL),
  (12, 'Wallcoverings',      'wallcoverings',      'category', NULL),
  (13, 'Outdoor & Marine',   'outdoor-marine',     'category', 10);

-- B2B Collections (id 20-23)
INSERT INTO taxonomies (id, name, slug, type) VALUES
  (20, 'Heritage',      'heritage',      'collection'),
  (21, 'Coastal',       'coastal',       'collection'),
  (22, 'Urban Loft',    'urban-loft',    'collection'),
  (23, 'Contract Grade','contract-grade','collection');

-- pa_color / finishes (id 30-34)
INSERT INTO taxonomies (id, name, slug, type) VALUES
  (30, 'Ivory',      'ivory',      'pa_color'),
  (31, 'Charcoal',   'charcoal',   'pa_color'),
  (32, 'Sage',       'sage',       'pa_color'),
  (33, 'Terracotta', 'terracotta', 'pa_color'),
  (34, 'Navy',       'navy',       'pa_color');

-- pa_composition (id 40-43)
INSERT INTO taxonomies (id, name, slug, type) VALUES
  (40, '100% Linen',         'linen',              'pa_composition'),
  (41, 'Cotton Blend',       'cotton-blend',       'pa_composition'),
  (42, 'Wool',               'wool',               'pa_composition'),
  (43, 'Recycled Polyester', 'recycled-polyester', 'pa_composition');

-- pa_application (id 50-53)
INSERT INTO taxonomies (id, name, slug, type) VALUES
  (50, 'Residential', 'residential', 'pa_application'),
  (51, 'Commercial',  'commercial',  'pa_application'),
  (52, 'Outdoor',     'outdoor',     'pa_application'),
  (53, 'Marine',      'marine',      'pa_application');

-- pa_size (id 60-62)
INSERT INTO taxonomies (id, name, slug, type) VALUES
  (60, 'Sample Swatch', 'sample-swatch', 'pa_size'),
  (61, 'Half Metre',    'half-metre',    'pa_size'),
  (62, 'Full Roll',     'full-roll',     'pa_size');

-- -----------------------------------------------------------------------------
-- Products (top-level: parent_id IS NULL)
-- -----------------------------------------------------------------------------
INSERT INTO products
  (id, sku, title, slug, short_description, description, price, stock_status, product_type, parent_id) VALUES
  (101, 'HWT-100', 'Highland Wool Tweed', 'highland-wool-tweed',
   'Dense wool tweed with a heritage herringbone hand.',
   'A robust, dry-finished wool tweed engineered for high-traffic seating. Martindale rated for contract use, available in three colourways.',
   48.00, 'in_stock', 'variable', NULL),

  (102, 'CLS-200', 'Coastal Linen Sheer', 'coastal-linen-sheer',
   'Airy pure-linen sheer that diffuses coastal light.',
   'Loosely woven 100% linen voile for relaxed drapery. Sold by the half metre or as a full roll.',
   32.00, 'in_stock', 'variable', NULL),

  (103, 'MRV-303', 'Meridian Velvet', 'meridian-velvet',
   'Plush cotton-blend velvet with a matte sheen.',
   'A short-pile velvet with excellent recovery and rich terracotta depth. Suited to feature upholstery.',
   54.50, 'in_stock', 'simple', NULL),

  (104, 'SMB-104', 'Saltmarsh Boucle', 'saltmarsh-boucle',
   'Chunky wool boucle in a soft sage tone.',
   'Textural looped boucle woven from lambswool. Adds tactile warmth to occasional chairs.',
   61.00, 'in_stock', 'simple', NULL),

  (105, 'DSC-105', 'Dockside Canvas', 'dockside-canvas',
   'Heavyweight recycled canvas for hard use.',
   'A 400gsm recycled-polyester canvas built for commercial and light outdoor duty. Currently between dye lots.',
   28.00, 'out_of_stock', 'simple', NULL),

  (106, 'IHL-106', 'Ivory Herringbone Linen', 'ivory-herringbone-linen',
   'Crisp ivory linen in a fine herringbone.',
   'Mid-weight 100% linen with a subtle self-herringbone. A clean neutral for curtains and cushions.',
   36.00, 'in_stock', 'simple', NULL),

  (107, 'HSW-107', 'Harbor Stripe Weave', 'harbor-stripe-weave',
   'Navy-and-ecru yarn-dyed stripe.',
   'A cotton-blend awning stripe with coastal character, equally at home on drapery or scatter cushions.',
   30.00, 'in_stock', 'simple', NULL),

  (108, 'LCW-108', 'Loft Concrete Wallcover', 'loft-concrete-wallcover',
   'Textured concrete-effect commercial wallcovering.',
   'A scrubbable recycled-polyester wallcovering with an industrial concrete texture for loft and hospitality interiors.',
   22.00, 'in_stock', 'simple', NULL),

  (109, 'TGC-109', 'Terracotta Grasscloth', 'terracotta-grasscloth',
   'Natural grasscloth in warm terracotta.',
   'Hand-laid natural grasscloth wallcovering. Each panel varies subtly — a hallmark of the natural fibre.',
   44.00, 'in_stock', 'simple', NULL),

  (110, 'MVC-110', 'Marine Vinyl Contract', 'marine-vinyl-contract',
   'UV- and mildew-resistant marine upholstery vinyl.',
   'A navy performance vinyl certified for marine and outdoor seating. Wipe-clean and salt-spray tested.',
   39.50, 'in_stock', 'simple', NULL),

  (111, 'SWF-111', 'Sage Wool Flannel', 'sage-wool-flannel',
   'Soft brushed wool flannel in muted sage.',
   'A brushed wool flannel with a gentle nap, ideal for residential upholstery and soft furnishings.',
   52.00, 'in_stock', 'simple', NULL),

  (112, 'NPT-112', 'Navy Performance Twill', 'navy-performance-twill',
   'Stain-resistant recycled twill for contract seating.',
   'A recycled-polyester twill with a factory stain-resist finish. Rated for commercial and covered-outdoor use.',
   34.00, 'in_stock', 'simple', NULL),

  (113, 'ICM-113', 'Ivory Cotton Muslin', 'ivory-cotton-muslin',
   'Lightweight ivory cotton muslin.',
   'An economical cotton-blend muslin for linings, mock-ups and light residential drapery.',
   18.00, 'in_stock', 'simple', NULL),

  (114, 'CBD-114', 'Charcoal Blackout Drape', 'charcoal-blackout-drape',
   'Three-pass blackout drapery in charcoal.',
   'A recycled-polyester blackout cloth with a soft face, blocking light for residential and hospitality rooms.',
   41.00, 'in_stock', 'simple', NULL);

-- Variations (parent_id points at the variable parent; hidden from the archive)
INSERT INTO products
  (id, sku, title, slug, price, stock_status, product_type, parent_id) VALUES
  (10101, 'HWT-CHR', 'Highland Wool Tweed — Charcoal', 'highland-wool-tweed-charcoal', 48.00, 'in_stock',  'simple', 101),
  (10102, 'HWT-SAG', 'Highland Wool Tweed — Sage',     'highland-wool-tweed-sage',     48.00, 'in_stock',  'simple', 101),
  (10103, 'HWT-NVY', 'Highland Wool Tweed — Navy',     'highland-wool-tweed-navy',     48.00, 'out_of_stock','simple', 101),
  (10201, 'CLS-HM',  'Coastal Linen Sheer — Half Metre','coastal-linen-sheer-half-metre', 32.00, 'in_stock', 'simple', 102),
  (10202, 'CLS-FR',  'Coastal Linen Sheer — Full Roll', 'coastal-linen-sheer-full-roll', 210.00, 'in_stock', 'simple', 102);

-- -----------------------------------------------------------------------------
-- product_taxonomy — category / collection / attribute assignments
-- -----------------------------------------------------------------------------
INSERT INTO product_taxonomy (product_id, taxonomy_id) VALUES
  -- 101 Highland Wool Tweed
  (101,10),(101,20),(101,42),(101,50),(101,51),(101,31),(101,32),(101,34),
  -- 102 Coastal Linen Sheer
  (102,11),(102,21),(102,40),(102,50),(102,30),(102,61),(102,62),
  -- 103 Meridian Velvet
  (103,10),(103,22),(103,41),(103,50),(103,51),(103,33),
  -- 104 Saltmarsh Boucle
  (104,10),(104,21),(104,42),(104,50),(104,32),
  -- 105 Dockside Canvas
  (105,10),(105,13),(105,23),(105,43),(105,51),(105,52),(105,31),
  -- 106 Ivory Herringbone Linen
  (106,11),(106,20),(106,40),(106,50),(106,30),
  -- 107 Harbor Stripe Weave
  (107,11),(107,21),(107,41),(107,50),(107,34),
  -- 108 Loft Concrete Wallcover
  (108,12),(108,22),(108,43),(108,51),(108,31),
  -- 109 Terracotta Grasscloth
  (109,12),(109,20),(109,40),(109,50),(109,33),
  -- 110 Marine Vinyl Contract
  (110,10),(110,13),(110,23),(110,43),(110,51),(110,53),(110,52),(110,34),
  -- 111 Sage Wool Flannel
  (111,10),(111,20),(111,42),(111,50),(111,32),
  -- 112 Navy Performance Twill
  (112,10),(112,23),(112,43),(112,51),(112,52),(112,34),
  -- 113 Ivory Cotton Muslin
  (113,11),(113,21),(113,41),(113,50),(113,30),
  -- 114 Charcoal Blackout Drape
  (114,11),(114,22),(114,43),(114,50),(114,51),(114,31),
  -- Variation colour/size tags (inherit for completeness)
  (10101,31),(10102,32),(10103,34),(10201,61),(10202,62);

-- -----------------------------------------------------------------------------
-- product_meta — non-relational technical attributes
-- -----------------------------------------------------------------------------
INSERT INTO product_meta (product_id, meta_key, meta_value) VALUES
  (101, '_weight_gsm', '480'), (101, '_width_cm', '140'), (101, '_martindale', '50000'), (101, '_min_order_qty', '2'),
  (102, '_weight_gsm', '110'), (102, '_width_cm', '150'), (102, '_min_order_qty', '1'),
  (103, '_weight_gsm', '320'), (103, '_width_cm', '140'), (103, '_martindale', '40000'),
  (104, '_weight_gsm', '520'), (104, '_width_cm', '138'),
  (105, '_weight_gsm', '400'), (105, '_width_cm', '160'), (105, '_datasheet_url', 'https://example.com/ds/dockside-canvas.pdf'),
  (108, '_weight_gsm', '260'), (108, '_width_cm', '90'),  (108, '_fire_rating', 'EN 13501-1 B-s1,d0'),
  (110, '_weight_gsm', '640'), (110, '_width_cm', '137'), (110, '_uv_rating', 'UPF 50+'), (110, '_min_order_qty', '5'),
  (114, '_weight_gsm', '300'), (114, '_width_cm', '280'), (114, '_opacity', 'blackout');

-- -----------------------------------------------------------------------------
-- Recompute cached taxonomy counts from the (top-level) product assignments.
-- -----------------------------------------------------------------------------
UPDATE taxonomies t SET count = (
  SELECT COUNT(*) FROM product_taxonomy pt
  JOIN products p ON p.id = pt.product_id
  WHERE pt.taxonomy_id = t.id AND p.parent_id IS NULL
);
