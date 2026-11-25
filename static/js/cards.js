/* ============================================================================
   Product card extras, shared by every page that shows .bt-card cards.
   - Hover image: the first gallery image, preloaded for cards near the
     viewport (max 3 at a time, low priority) and cross-faded on hover. No
     swap on touch screens, Save-Data or 2G.
   - Download button: saves the card's photo as BigTree_<name>_<id>.<ext>.
   Call window.btObserveCards() after inserting new cards.
   ========================================================================== */
(function () {
  "use strict";

  const canHover = window.matchMedia("(hover: hover) and (pointer: fine)").matches;
  const conn = navigator.connection || {};
  const lowData = conn.saveData || /2g/.test(conn.effectiveType || "");
  const queue = [];
  let active = 0;
  const MAX_ACTIVE = 3;

  function loadHover(media) {
    if (media.dataset.hvState) return;
    media.dataset.hvState = "loading";
    active++;
    const img = new Image();
    img.className = "bt-hover-img";
    img.alt = "";
    img.decoding = "async";
    if ("fetchPriority" in img) img.fetchPriority = "low";
    img.onload = () => { media.appendChild(img); media.classList.add("bt-hv-ready"); done(); };
    img.onerror = done;
    img.src = media.dataset.hoverSrc;
    function done() { media.dataset.hvState = "done"; active--; pump(); }
  }

  function pump() {
    while (active < MAX_ACTIVE && queue.length && !document.hidden) loadHover(queue.shift());
  }
  document.addEventListener("visibilitychange", pump);

  const io = canHover && !lowData && "IntersectionObserver" in window
    ? new IntersectionObserver((entries) => {
        entries.forEach((en) => {
          if (!en.isIntersecting) return;
          io.unobserve(en.target);
          const own = en.target.querySelector(".bt-card-img");
          const enqueue = () => { queue.push(en.target); pump(); };
          if (!own || own.complete) enqueue(); else own.addEventListener("load", enqueue, { once: true });
        });
      }, { rootMargin: "300px" })
    : null;

  function observeCards() {
    if (!canHover) return;
    document.querySelectorAll(".bt-card-media[data-hover-src]:not([data-hv-watch])").forEach((m) => {
      m.dataset.hvWatch = "1";
      m.addEventListener("mouseenter", () => loadHover(m), { once: true });
      if (io) io.observe(m);
    });
  }
  window.btObserveCards = observeCards;
  if (document.readyState === "complete") observeCards();
  else window.addEventListener("load", observeCards, { once: true });

  document.addEventListener("click", async (e) => {
    const btn = e.target.closest(".bt-card-download");
    if (!btn) return;
    e.preventDefault();
    const src = btn.dataset.src;
    const m = /\.([a-z0-9]{2,4})$/i.exec(new URL(src, location.href).pathname);
    const name = btn.dataset.name + "." + (m ? m[1].toLowerCase() : "jpg");
    btn.classList.add("is-loading");
    try {
      const res = await fetch(src, { mode: "cors" });
      if (!res.ok) throw new Error("HTTP " + res.status);
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 2000);
    } catch (_) {
      window.open(src, "_blank", "noopener"); // cross-origin blocked: open instead
    } finally {
      btn.classList.remove("is-loading");
    }
  });
})();
