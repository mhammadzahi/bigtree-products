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
    ["pa_color", "pa_size", "pa_composition", "pa_application"].forEach((key) => {
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

  function priceLabel(p) {
    return p && p.price > 0 ? "$" + Number(p.price).toFixed(2) : "Price on request";
  }

  /* ---- render one product card ------------------------------------------- */
  function cardHTML(p) {
    const slug = encodeURIComponent(p.slug);
    const media = p.image_url
      ? `<img src="${esc(p.image_url)}" alt="${esc(p.title)}" loading="lazy">`
      : `<span class="media-placeholder">No image</span>`;
    const stock = p.stock_status !== "in_stock"
      ? `<span class="badge-stock out">Out of stock</span>` : "";
    const sku = p.sku ? `<span class="sku">SKU ${esc(p.sku)}</span>` : "";
    const tags = (p.collections || []).length
      ? `<div class="tags">${p.collections.map((t) => `<span class="tag">${esc(t.name)}</span>`).join("")}</div>`
      : "";

    return `<article class="card">
      <a class="card-media" href="/product/${slug}">${media}${stock}</a>
      <div class="card-body">
        ${sku}
        <h3 class="card-title"><a href="/product/${slug}">${esc(p.title)}</a></h3>
        ${tags}
        <div class="card-footer">
          <span class="price">${priceLabel(p)}</span>
          <a class="btn btn-sm btn-primary" href="/product/${slug}">Request sample</a>
        </div>
      </div>
    </article>`;
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
