/* ============================================================================
   Category leaf page: filters, paging, card extras.
   Port of the live Leaf Grid v14 behaviour. Filter state lives in the URL
   (f_* params): every change pushes history, Back restores it. Options are
   real links, so the page also works without this script.
   ========================================================================== */
(function () {
  "use strict";

  const root = document.getElementById("bt-lg");
  if (!root) return;
  const slug = root.dataset.slug;
  const paged = root.dataset.paged === "true";
  const barEl = document.getElementById("bt-lg-bar");
  const gridEl = document.getElementById("bt-lg-grid");
  const countEl = document.getElementById("bt-lg-count");
  const moreEl = document.getElementById("bt-lg-more");
  const statusEl = document.getElementById("bt-lg-status");
  const moreBtn = document.getElementById("bt-lg-morebtn");
  const api = "/api/v1/category/" + encodeURIComponent(slug);

  let page = parseInt(root.dataset.page, 10) || 1;
  let hasMore = root.dataset.more === "true";
  let total = parseInt((countEl && countEl.textContent) || "0", 10) || 0;
  let query = location.search.replace(/^\?/, "");
  let gen = 0;            // bumps on every filter change; stale pages are dropped
  let controller = null;  // aborts the previous filter request
  let paging = false;

  const plural = (n) => n + (n === 1 ? " product" : " products");

  /* ---- filter changes ---------------------------------------------------- */
  function pageUrl(q) { return location.pathname + (q ? "?" + q : ""); }

  async function load(q, push) {
    const openFacet = currentOpen();
    const openScroll = openFacet ? openFacet.querySelector(".bt-lg-dd-panel").scrollTop : 0;
    const openKey = openFacet ? openFacet.dataset.facet : null;

    if (controller) controller.abort();
    controller = new AbortController();
    gen++;
    root.classList.add("is-loading");
    try {
      const res = await fetch(api + (q ? "?" + q : ""), {
        headers: { Accept: "application/json" }, credentials: "same-origin", signal: controller.signal,
      });
      if (res.status === 401) { location.href = "/login"; return; }
      if (!res.ok) throw new Error("HTTP " + res.status);
      const data = await res.json();
      barEl.innerHTML = data.bar || "";
      gridEl.innerHTML = data.grid || "";
      total = data.count;
      if (countEl) countEl.textContent = plural(total);
      page = data.page || 1;
      hasMore = !!data.more;
      query = q;
      updateMore();
      if (push) history.pushState({ q }, "", pageUrl(q));
      if (openKey) reopen(openKey, openScroll);
      observeCards();
    } catch (err) {
      if (err.name === "AbortError") return;
      location.href = pageUrl(q); // fall back to a full page load
    } finally {
      root.classList.remove("is-loading");
    }
  }

  barEl.addEventListener("click", (e) => {
    const link = e.target.closest("a[data-q]");
    if (link) {
      e.preventDefault();
      load(link.dataset.q, true);
      return;
    }
    const btn = e.target.closest(".bt-lg-dd-btn");
    if (btn) toggleDropdown(btn.closest(".bt-lg-dd"));
  });

  window.addEventListener("popstate", () => load(location.search.replace(/^\?/, ""), false));

  /* ---- dropdowns: one open at a time; outside click / Esc closes --------- */
  function currentOpen() { return barEl.querySelector(".bt-lg-dd.is-open"); }

  function closeAll() {
    barEl.querySelectorAll(".bt-lg-dd.is-open").forEach((dd) => {
      dd.classList.remove("is-open");
      dd.querySelector(".bt-lg-dd-panel").hidden = true;
      dd.querySelector(".bt-lg-dd-btn").setAttribute("aria-expanded", "false");
    });
  }

  function openDropdown(dd) {
    const panel = dd.querySelector(".bt-lg-dd-panel");
    dd.classList.add("is-open");
    panel.hidden = false;
    dd.querySelector(".bt-lg-dd-btn").setAttribute("aria-expanded", "true");
    panel.classList.remove("is-right");
    if (window.innerWidth > 640 && panel.getBoundingClientRect().right > window.innerWidth - 8) {
      panel.classList.add("is-right");
    }
  }

  function toggleDropdown(dd) {
    const wasOpen = dd.classList.contains("is-open");
    closeAll();
    if (!wasOpen) openDropdown(dd);
  }

  function reopen(key, scroll) {
    const dd = barEl.querySelector('.bt-lg-dd[data-facet="' + key + '"]');
    if (!dd) return;
    openDropdown(dd);
    dd.querySelector(".bt-lg-dd-panel").scrollTop = scroll;
  }

  document.addEventListener("click", (e) => { if (!e.target.closest(".bt-lg-dd")) closeAll(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeAll(); });

  /* ---- grid density (small / big), remembered per browser ---------------- */
  const viewBtns = root.querySelectorAll(".bt-lg-viewbtn");
  function setView(v) {
    root.classList.toggle("is-big", v === "big");
    viewBtns.forEach((b) => b.classList.toggle("is-on", b.dataset.view === v));
    try { localStorage.setItem("btLgView", v); } catch (_) { /* storage blocked */ }
  }
  viewBtns.forEach((b) => b.addEventListener("click", () => setView(b.dataset.view)));
  try { if (localStorage.getItem("btLgView") === "big") setView("big"); } catch (_) { /* ignore */ }

  /* ---- paging: 60 at a time on very large leaves ------------------------- */
  function updateMore() {
    if (!moreEl) return;
    moreEl.hidden = !hasMore;
    const shown = gridEl.querySelectorAll(".bt-card").length;
    if (statusEl) statusEl.textContent = hasMore ? "Showing " + shown + " of " + total : "";
  }

  async function nextPage() {
    if (!paged || !hasMore || paging) return;
    paging = true;
    const myGen = gen;
    if (statusEl) statusEl.textContent += " - loading";
    try {
      const sep = query ? "&" : "";
      const res = await fetch(api + "?" + query + sep + "page=" + (page + 1), {
        headers: { Accept: "application/json" }, credentials: "same-origin",
      });
      if (!res.ok) throw new Error("HTTP " + res.status);
      const data = await res.json();
      if (myGen !== gen) return; // filters changed meanwhile
      gridEl.insertAdjacentHTML("beforeend", data.grid || "");
      page = data.page;
      hasMore = !!data.more;
      if (moreBtn) moreBtn.hidden = true;
      observeCards();
    } catch (_) {
      if (moreBtn) moreBtn.hidden = false; // manual fallback
    } finally {
      paging = false;
      updateMore();
      checkSentinel();
    }
  }

  let sentinelIO = null;
  function checkSentinel() {
    if (!moreEl || !hasMore) return;
    const r = moreEl.getBoundingClientRect();
    if (r.top < window.innerHeight + 1200) nextPage();
  }
  if (paged && moreEl) {
    if ("IntersectionObserver" in window) {
      sentinelIO = new IntersectionObserver((es) => { if (es[0].isIntersecting) nextPage(); }, { rootMargin: "1200px" });
      sentinelIO.observe(moreEl);
    } else if (moreBtn) {
      moreBtn.hidden = false;
    }
    if (moreBtn) moreBtn.addEventListener("click", nextPage);
    updateMore();
    setTimeout(checkSentinel, 300);
  }

  /* ---- Back restores how deep the visitor had scrolled (paged leaves) ---- */
  const DEPTH_KEY = "btLgDepth";
  window.addEventListener("pagehide", () => {
    if (!paged || page < 2) return;
    try {
      sessionStorage.setItem(DEPTH_KEY, JSON.stringify({
        k: location.pathname + location.search, page, y: window.scrollY, t: Date.now(),
      }));
    } catch (_) { /* storage blocked */ }
  });

  (async function restoreDepth() {
    if (!paged) return;
    let saved = null;
    try { saved = JSON.parse(sessionStorage.getItem(DEPTH_KEY) || "null"); } catch (_) { return; }
    const nav = performance.getEntriesByType && performance.getEntriesByType("navigation")[0];
    if (!saved || !nav || nav.type !== "back_forward") return;
    try { sessionStorage.removeItem(DEPTH_KEY); } catch (_) { /* ignore */ }
    if (saved.k !== location.pathname + location.search || saved.page < 2 || Date.now() - saved.t > 3 * 3600 * 1000) return;
    history.scrollRestoration = "manual";
    try {
      const sep = query ? "&" : "";
      const res = await fetch(api + "?" + query + sep + "upto=" + Math.min(saved.page, 30), {
        headers: { Accept: "application/json" }, credentials: "same-origin",
      });
      if (!res.ok) return;
      const data = await res.json();
      gridEl.innerHTML = data.grid || "";
      page = data.page;
      hasMore = !!data.more;
      updateMore();
      observeCards();
      window.scrollTo({ top: saved.y, behavior: "instant" });
    } finally {
      history.scrollRestoration = "auto";
    }
  })();

  function observeCards() { if (window.btObserveCards) window.btObserveCards(); }
})();
