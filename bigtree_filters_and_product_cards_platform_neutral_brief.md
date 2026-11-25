# BigTree Group: Filters and Product Cards
## Platform Neutral Brief

What a custom-coded Big Tree website needs in order to offer the same category pages, collection pages, filters and product cards. Basic requirements only, for any stack.

| Field | Details |
| :--- | :--- |
| **Audience** | BigTree Group developers (staff only) |
| **Source** | Behaviour proven on bigtree-group.com, written without reference to any CMS, plugin or page builder |
| **Date** | 25 November 2026 |

**How to use this.** Each section says what must exist and what it must do, not how to code it. Any framework, database and front end can meet it. Details such as exact labels, colors and lists can be taken from the live site.

---

## 1. The idea in one page

Visitors browse products by category (drill down to a leaf, then see products) and by brand collection (Selection, then Collection, then products). On every list they can narrow with several filters at once. All lists use the same product card.

* **One product index.** Build a precomputed index of every visible product with its categories, collections and filter values. Tile counts, filter options and option counts all come from it, never from live queries.
* **Filters are state in the address.** A filtered page can be shared, bookmarked and reopened, and the Back button restores it.
* **Counts are honest.** An option count equals the number of cards the visitor would see if they picked it. Options with zero results are hidden.
* **One card, everywhere.** Category lists and collection lists use the identical card component.

---

## 2. Data you need

| Entity | Needs |
| :--- | :--- |
| **Product** | Title, SKU, images (main plus gallery), status (published, private, unpublished), visible-in-catalog flag, category links, collection link, filter values (below). |
| **Category** | Tree with parent and children. Top levels such as Furniture, Floor Coverings, Wallcoverings, Leathers, Lighting, Fabrics. Optional cover image. Only the last level (leaf) lists products. |
| **Collection** | Two levels: Selection (brand) as parent, Collection as child. Optional cover image. |
| **Filter values** | Per product: Material, Color, Type, Application, Outdoor yes or no, Flame retardant yes or no, Project (for example Residential, Hospitality). Fabrics only: Martindale, lightfastness, fire standard. |
| **Color versions** | A product can link to its color-version products. A color version is a product record that carries its own color and image. |
| **Swatches** | A material or color sample of a product. It has its own record but is information only: it never appears as a card, and its color counts toward the parent in the Color filter. |

**Rules for the data:** read values by name, not by spreadsheet column position. Group raw material terms into families (for example aluminium under Metal) with an Other bucket. Use 13 color families: black, white, grey, red, yellow, blue, green, brown, pink, orange, beige, purple, plus Metallic, Mixed and Other. Outdoor comes from the product's own environment value, not from a category or brand tag. Clean duplicate and misspelt terms at the source before building.

---

## 3. Pages to build

| Page | What it shows |
| :--- | :--- |
| **Category tile page** | Child categories as tiles with exact product counts. A parent never shows a product grid. A category with a single child skips that level. Optional rule-based tiles (for example Fabrics: Drapery, Upholstery, Linens, Trimmings, Decorative Accessories), where a product can sit in more than one tile. |
| **Leaf product page** | Filter bar plus product grid for one leaf category. Some categories list all products directly with Type as a filter (Leathers, Lighting, Sofas). |
| **Selection page** | Collection cards for one brand, a filter bar built from the cards shown, and the Outdoor switch. |
| **Collection page** | Product cards of one collection, with a child-category filter bar. |
| **All Selections page** | Every selection in one grid. |

---

## 4. Filter behaviour

* **Switches:** Outdoor and Flame retardant. Off shows everything (outdoor products also work indoors). On shows only matching products.
* **Dropdowns:** Selection, Type, Material, Color, Application, Project (only where relevant), and on Fabrics Martindale, lightfastness and fire standard. Show only the filters a category has data for.
* Several filters combine. Counts and options update to what remains.
* State lives in the page address (one parameter per filter). The Outdoor switch stays on while the visitor drills down and enters cards.
* Selection and Collection pages build their filter buttons from the cards actually shown, and remove a button when no shown card carries it.
* Under a Color filter, each matching color version is its own card. Swatches never become cards.
* Fabrics blends count under every fibre they contain.

---

## 5. The product card

* Whole photo shown at its true proportion: no stretch, no crop, no grey frame.
* Two grid densities (small and big) switched by icon buttons.
* Hover swaps to the first gallery image. Preload the hover image for cards near the viewport. No swap on touch screens or when there is no gallery image.
* **OUTDOOR** tag on outdoor products (small black rounded tag, top left, on hover).
* **PRIVATE** tag on private products, shown only to staff roles. Visitors never see private products.
* Actions on hover: download image, add to enquiry list.
* Round rug leaves show the round-size photo instead of the first image.

---

## 6. Business rules

| Rule | Detail |
| :--- | :--- |
| **Visibility** | Published products only for visitors. Unpublished means discontinued or not wanted. Hidden-from-catalog records exist only as color versions or swatches. |
| **Country limits** | Products restricted for a visitor's country must be hidden entirely (this was set aside on the current site). |
| **Collections** | Collection cards that hold only finishes are hidden. Mixed collections stay. |
| **Sorting** | By collection name A to Z, symbol or number names last, then title. |
| **Loading** | Leaf pages load all cards at once. Very large lists (Fabrics Drapery, Upholstery) load 60 at a time with scroll loading and restore scroll depth on Back. |
| **Swatch sale** | A fabric swatch is sold as fabric only if the product is also in the Fabrics category. |

---

## 7. Performance and freshness

* Rebuild the product index after any import or edit, in the background, a few minutes after the last change. Serve the old index until the new one is ready.
* Build very large categories (about 17,000 products) in chunks, not in one pass.
* Cache list counts. Never count live on category pages.
* Generate card-size images ahead of time. If a size is missing, create it on demand rather than serving the full-size original.

---

## 8. Suggested build order

1. Clean the data: categories, terms, colors, color-version links.
2. Build the product index and a debug report that lists products, categories and filter values.
3. Category tile page with exact counts.
4. Leaf page: cards, filters, address state, Back button.
5. Selection and Collection pages, Outdoor switch.
6. Card extras: hover swap, tags, actions, grid toggle.
7. Add categories one at a time, each with its own list of filters that apply.

---

## 9. Acceptance checks

* Every count and option number matches the cards shown after selecting it.
* Two filters together give the right result. The address reopens the same view in a new tab.
* Back button restores filters and scroll position.
* A visitor sees no private products, staff do.
* Outdoor tag, switch and carry-over work. Filter buttons match the cards shown.
* A category that is not ready keeps its old page.
* Performance holds on the largest category.

---

*Staff only. Internal BigTree Group document. Do not share outside the company.*