/* ============================================================================
   Bigtree storefront — async catalog filtering.
   Intercepts the sidebar form + sort dropdown, queries /api/v1/products, and
   re-renders the product grid in place without a full page reload.
   ========================================================================== */
(function () {
  "use strict";

  const form     = document.getElementById("filter-form");
  const grid     = document.getElementById("product-grid");
  const orderby  = document.getElementById("orderby");
  const count    = document.getElementById("result-count");
  const pager    = document.getElementById("pagination");
  const clearBtn = document.getElementById("clear-filters");
  if (!form || !grid) return;

  let currentPage = 1;

  /* ---- exclusive accordions: opening one collapses the others ------------- */
  const accordions = form.querySelectorAll("details.accordion");
  accordions.forEach((d) => {
    d.addEventListener("toggle", () => {
      if (!d.open) return;
      accordions.forEach((other) => { if (other !== d) other.open = false; });
    });
  });

  /* ---- build the query string from the current form + sort state --------- */
  function buildParams() {
    const params = new URLSearchParams();
    const data = new FormData(form);

    // single-value fields
    const s = (data.get("s") || "").trim();
    if (s) params.set("s", s);
    const category = data.get("category");
    if (category) params.set("category", category);
    const collection = data.get("collection");
    if (collection) params.set("collection", collection);

    // repeatable checkbox facets
    ["brand", "pa_application", "pa_color", "pa_composition", "pa_features"].forEach((key) => {
      data.getAll(key).forEach((v) => v && params.append(key, v));
    });

    if (orderby && orderby.value) params.set("orderby", orderby.value);
    if (currentPage > 1) params.set("page", String(currentPage));
    return params;
  }

  /* ---- escape untrusted strings before injecting into innerHTML ---------- */
  function esc(str) {
    return String(str == null ? "" : str)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  /* ---- render one product card ------------------------------------------- */
  function cardHTML(p) {
    const slug = encodeURIComponent(p.slug);
    const media = p.image_url
      ? `<img src="${esc(p.image_url)}" alt="${esc(p.title)}" loading="lazy">`
      : `<span class="media-placeholder">No image</span>`;
    const inStock = p.stock_status === "in_stock";
    const stockBadge = !inStock
      ? `<span class="badge-stock out">Out of stock</span>` : "";
    const stockPill = inStock
      ? `<span class="stock-pill in">In stock</span>`
      : `<span class="stock-pill out">Out of stock</span>`;
    const sku = p.sku ? `<span class="sku">SKU ${esc(p.sku)}</span>` : "";
    const cats = p.categories || [];
    const leafCat = cats.length ? cats[cats.length - 1] : null;
    const tags = leafCat
      ? `<div class="tags"><span class="tag">${esc(leafCat.name)}</span></div>`
      : "";

    return `<article class="card">
      <a class="card-media" href="/product/${slug}">${media}${stockBadge}</a>
      <div class="card-body">
        ${sku}
        <h3 class="card-title"><a href="/product/${slug}">${esc(p.title)}</a></h3>
        ${tags}
        <div class="card-footer">
          ${stockPill}
          <a class="btn btn-sm btn-primary" href="/product/${slug}">View details</a>
        </div>
      </div>
    </article>`;
  }

  /* ---- dynamically re-render the sidebar facets -------------------------- */
  // Each facet's options come from the server, computed over the CURRENT result
  // set, so anything shown always yields products (no dead-ends), counts stay
  // live, and empty facets collapse. Order matches the sidebar markup.
  const FACET_DEFS = [
    { param: "brand",          key: "brands",         multi: true },
    { param: "collection",     key: "collections",    multi: false, all: "All collections" },
    { param: "category",       key: "categories",     multi: false, all: "All categories" },
    { param: "pa_application", key: "pa_application", multi: true },
    { param: "pa_color",       key: "pa_color",       multi: true },
    { param: "pa_composition", key: "pa_composition", multi: true },
    { param: "pa_features",    key: "pa_features",    multi: true },
  ];

  function renderFacets(facets, params) {
    FACET_DEFS.forEach((def) => {
      const ul = document.getElementById("facet-" + def.param);
      if (!ul) return;
      const terms = facets[def.key] || [];
      const details = ul.closest("details.accordion");
      if (details) details.hidden = terms.length === 0;

      const selected = new Set(params.getAll(def.param));
      let html = "";
      if (!def.multi) {
        const checked = selected.size === 0 ? " checked" : "";
        html += `<li><label class="facet"><input type="radio" name="${def.param}" value=""${checked}> <span>${esc(def.all)}</span></label></li>`;
      }
      terms.forEach((t) => {
        const type = def.multi ? "checkbox" : "radio";
        const on = selected.has(t.name) ? " checked" : "";
        html += `<li><label class="facet"><input type="${type}" name="${def.param}" value="${esc(t.name)}"${on}> <span>${esc(t.name)}</span><em class="count">${t.count}</em></label></li>`;
      });
      ul.innerHTML = html;
    });
  }

  /* ---- render the pagination control ------------------------------------- */
  function renderPager(res) {
    if (!pager) return;
    const total = res.total_pages > 0 ? res.total_pages : 1;
    let html = "";
    if (res.has_prev) html += `<a class="page-link" data-page="${res.page - 1}" href="#">← Prev</a>`;
    html += `<span class="page-status">Page ${res.page} of ${total}</span>`;
    if (res.has_next) html += `<a class="page-link" data-page="${res.page + 1}" href="#">Next →</a>`;
    pager.innerHTML = html;
  }

  /* ---- fetch + re-render -------------------------------------------------- */
  async function load(pushHistory) {
    const params = buildParams();
    grid.classList.add("loading");
    try {
      const res = await fetch("/api/v1/products?" + params.toString(), {
        headers: { "Accept": "application/json" },
        credentials: "same-origin",
      });
      if (res.status === 401) { window.location.href = "/login"; return; }
      if (!res.ok) throw new Error("request failed: " + res.status);

      const data = await res.json();
      const products = data.products || [];

      grid.innerHTML = products.length
        ? products.map(cardHTML).join("")
        : `<p class="empty">No products match these filters.</p>`;

      if (count) count.innerHTML = `<strong>${data.total}</strong> products`;
      renderPager(data);
      if (data.facets) renderFacets(data.facets, params);

      if (pushHistory !== false) {
        const qs = params.toString();
        window.history.replaceState(null, "", qs ? "/products?" + qs : "/products");
      }
    } catch (err) {
      grid.innerHTML = `<p class="empty">Something went wrong loading products.</p>`;
      console.error(err);
    } finally {
      grid.classList.remove("loading");
    }
  }

  /* ---- events ------------------------------------------------------------ */
  // Apply button / Enter key
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    currentPage = 1;
    load();
  });

  // Auto-apply when a radio/checkbox changes.
  form.addEventListener("change", (e) => {
    if (e.target.matches('input[type="radio"], input[type="checkbox"]')) {
      currentPage = 1;
      load();
    }
  });

  // Debounced live search on the text input.
  let debounce;
  form.addEventListener("input", (e) => {
    if (e.target.name !== "s") return;
    clearTimeout(debounce);
    debounce = setTimeout(() => { currentPage = 1; load(); }, 300);
  });

  // Sort dropdown.
  if (orderby) orderby.addEventListener("change", () => { currentPage = 1; load(); });

  // Clear all.
  if (clearBtn) clearBtn.addEventListener("click", () => {
    form.reset();
    // form.reset() restores default-checked radios ("All"); force page 1.
    currentPage = 1;
    load();
  });

  // Pagination (delegated — links are re-rendered on every load).
  if (pager) pager.addEventListener("click", (e) => {
    const link = e.target.closest(".page-link");
    if (!link) return;
    e.preventDefault();
    currentPage = parseInt(link.dataset.page, 10) || 1;
    load();
    window.scrollTo({ top: 0, behavior: "smooth" });
  });
})();
