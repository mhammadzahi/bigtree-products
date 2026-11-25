/* ============================================================================
   Selection / Collection pages: pills and the Outdoor switch filter the cards
   already on the page (port of the live initGridFilter / initMultiSelectFilter).
   - All Selections: pills combine with AND, state in ?filters=a,b.
   - Selection grid and Collection page: one pill at a time.
   - Outdoor switch (f_out=1) is kept in the URL and added to every card link,
     so it stays on while the visitor drills down.
   ========================================================================== */
(function () {
  "use strict";

  const root = document.getElementById("bt-sel");
  if (!root) return;
  const multi = root.dataset.mode === "all";
  const items = Array.from(root.querySelectorAll(".bt-sel-item"));
  const pills = Array.from(root.querySelectorAll(".bt-sel-pill"));
  const allPill = pills.find((p) => p.dataset.filter === "");
  const outdoorBtn = root.querySelector(".bt-sel-outdoor");
  const empty = root.querySelector(".bt-sel-empty");

  const params = new URLSearchParams(location.search);
  const active = new Set();                         // AND mode: selected slugs
  let single = null;                                // single mode: selected pill
  let outdoor = /^(1|true|yes)$/.test(params.get("f_out") || "");

  if (multi) {
    (params.get("filters") || "").split(",").map((s) => s.trim()).filter(Boolean).forEach((s) => {
      if (s === "outdoor") outdoor = true;          // legacy value
      else active.add(s);
    });
  }

  // No switch on this page (leaf Collection): f_out passes through untouched.
  const hasSwitch = !!outdoorBtn;
  if (!hasSwitch) outdoor = false;

  const cats = (el) => (el.dataset.cats || "").split(" ").filter(Boolean);

  function apply() {
    let shown = 0;
    items.forEach((el) => {
      const c = cats(el);
      let ok = true;
      if (multi) ok = Array.from(active).every((s) => c.includes(s));
      else if (single) ok = single.dataset.filter.split(" ").some((s) => c.includes(s));
      if (outdoor && el.dataset.outdoor !== "1") ok = false;
      el.hidden = !ok;
      if (ok) shown++;
    });
    if (empty) empty.hidden = shown > 0;

    pills.forEach((p) => {
      const on = p === allPill
        ? (multi ? active.size === 0 : !single)
        : (multi ? p.dataset.filter.split(" ").some((s) => active.has(s)) : p === single);
      p.classList.toggle("is-on", on);
      p.setAttribute("aria-pressed", String(on));
    });
    if (outdoorBtn) {
      outdoorBtn.classList.toggle("is-on", outdoor);
      outdoorBtn.setAttribute("aria-pressed", String(outdoor));
      outdoorBtn.setAttribute("aria-checked", String(outdoor));
    }
    syncUrl();
    syncLinks();
  }

  function syncUrl() {
    const p = new URLSearchParams(location.search);
    p.delete("filters");
    if (multi && active.size) p.set("filters", Array.from(active).join(","));
    if (hasSwitch) {
      p.delete("f_out");
      if (outdoor) p.set("f_out", "1");
    }
    const qs = p.toString().replace(/%2C/g, ",");
    history.replaceState(null, "", location.pathname + (qs ? "?" + qs : ""));
  }

  // Carry the Outdoor switch into every card link (Selection -> Collection
  // -> category leaf pages all read f_out=1).
  function syncLinks() {
    if (!hasSwitch) return;
    root.querySelectorAll("a[href^='/collection/'], a[href^='/category/']").forEach((a) => {
      const u = new URL(a.getAttribute("href"), location.origin);
      if (outdoor) u.searchParams.set("f_out", "1"); else u.searchParams.delete("f_out");
      a.setAttribute("href", u.pathname + u.search);
    });
  }

  pills.forEach((p) => p.addEventListener("click", () => {
    if (p === allPill) {
      active.clear();
      single = null;
    } else if (multi) {
      const slugs = p.dataset.filter.split(" ");
      const on = slugs.some((s) => active.has(s));
      slugs.forEach((s) => (on ? active.delete(s) : active.add(s)));
    } else {
      single = single === p ? null : p;
    }
    apply();
  }));
  if (outdoorBtn) outdoorBtn.addEventListener("click", () => { outdoor = !outdoor; apply(); });

  apply();
})();
