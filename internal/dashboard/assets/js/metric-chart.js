// metric-chart.js — <metric-chart> custom element.
//
// A self-contained, dependency-free live sparkline. Datastar feeds it numbers by
// patching plain attributes (via data-attr); the component keeps its own canvas
// ring-buffer history and redraws, so chart state survives across signal patches
// (no DOM morphing of the chart). This is the "props in, renders out" pattern.
//
// Reactive attributes (set by Datastar each tick):
//   va    primary series value
//   vb    optional secondary series value (renders a 2nd line + "a / b" header)
//   cap   optional ceiling (bytes mode: "used / cap" + y-scale)
//   tick  monotonic counter; ALWAYS changes each push, so the chart advances one
//         sample per second even when va/vb are flat (and coalesces multiple
//         attribute writes from one signal patch into a single sample).
//
// Static config attributes:
//   label, mode ("percent"|"bytes"|"rate"|"value"), unit, color, color2
//
// modes:
//   percent  va is 0..100; y-axis >= 100.
//   bytes    va is a byte gauge; header shows humanBytes(va)[ / cap].
//   rate     va/vb are CUMULATIVE byte counters; the component differentiates
//            them (delta / dt) into per-second rates and charts/labels those.
(() => {
  const MAXPTS = 90;
  const HEIGHT = 46;
  const dpr = () => window.devicePixelRatio || 1;

  function humanBytes(n) {
    if (!isFinite(n) || n < 0) n = 0;
    const u = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) {
      n /= 1024;
      i++;
    }
    return (i === 0 ? n.toFixed(0) : n.toFixed(1)) + " " + u[i];
  }

  function injectStyles() {
    if (document.getElementById("metric-chart-style")) return;
    const s = document.createElement("style");
    s.id = "metric-chart-style";
    s.textContent = `
      metric-chart { display:block; }
      metric-chart .mc-head { display:flex; align-items:baseline; justify-content:space-between; gap:8px; margin-bottom:4px; }
      metric-chart .mc-label { font-size:11px; color:var(--muted-foreground); }
      metric-chart .mc-val { font-size:13px; font-weight:600; color:var(--foreground); font-variant-numeric:tabular-nums; }
      metric-chart .mc-sep { color:var(--muted-foreground); font-weight:400; }
      metric-chart canvas { display:block; width:100%; }
    `;
    document.head.appendChild(s);
  }

  class MetricChart extends HTMLElement {
    static get observedAttributes() {
      return ["va", "vb", "cap", "tick", "mode", "label", "unit", "color", "color2"];
    }

    constructor() {
      super();
      this._a = []; // primary series, in chart units
      this._b = []; // secondary series
      this._prev1 = null; // {t,c} last counter sample (rate mode)
      this._prev2 = null;
      this._raf = null;
      this._ingestQueued = false;
    }

    connectedCallback() {
      injectStyles();
      this.innerHTML =
        '<div class="mc-head"><span class="mc-label"></span><span class="mc-val">—</span></div><canvas></canvas>';
      this._label = this.querySelector(".mc-label");
      this._valEl = this.querySelector(".mc-val");
      this._canvas = this.querySelector("canvas");
      this._ctx = this._canvas.getContext("2d");
      this._ro = new ResizeObserver(() => this._resize());
      this._ro.observe(this);
      this._renderHead();
      this._resize();
    }

    disconnectedCallback() {
      if (this._ro) this._ro.disconnect();
      if (this._raf) cancelAnimationFrame(this._raf);
    }

    attributeChangedCallback(name) {
      // Any data attribute change schedules ONE coalesced ingest after the
      // current synchronous batch (a signal patch may set va, vb, cap and tick
      // back-to-back). queueMicrotask runs once all of them are applied, so we
      // sample the final values exactly once per tick. Config attrs just redraw.
      if (name === "va" || name === "vb" || name === "cap" || name === "tick") {
        this._scheduleIngest();
      } else {
        this._renderHead();
        this._scheduleDraw();
      }
    }

    get _mode() {
      return this.getAttribute("mode") || "value";
    }

    _color(i) {
      return this.getAttribute(i === 2 ? "color2" : "color") || (i === 2 ? "#60a5fa" : "#34d399");
    }

    _num(attr) {
      const v = parseFloat(this.getAttribute(attr));
      return isFinite(v) ? v : 0;
    }

    _scheduleIngest() {
      if (this._ingestQueued || !this.isConnected) return;
      this._ingestQueued = true;
      queueMicrotask(() => {
        this._ingestQueued = false;
        this._ingest();
      });
    }

    _ingest() {
      if (!this._ctx) return;
      const now = performance.now();
      const mode = this._mode;
      const push = (arr, raw, prevKey) => {
        let v = raw;
        if (mode === "rate") {
          const prev = this[prevKey];
          this[prevKey] = { t: now, c: raw };
          if (!prev) return null;
          const dt = (now - prev.t) / 1000;
          if (dt <= 0) return null;
          v = Math.max(0, (raw - prev.c) / dt);
        }
        arr.push(v);
        while (arr.length > MAXPTS) arr.shift();
        return v;
      };
      const v1 = push(this._a, this._num("va"), "_prev1");
      let v2 = null;
      if (this.hasAttribute("vb")) v2 = push(this._b, this._num("vb"), "_prev2");
      this._renderHead(v1, v2);
      this._scheduleDraw();
    }

    _fmt(v) {
      if (v == null) return "—";
      const mode = this._mode;
      const unit = this.getAttribute("unit") || "";
      if (mode === "percent") return v.toFixed(1) + (unit || "%");
      if (mode === "bytes") {
        const cap = this._num("cap");
        return humanBytes(v) + (cap > 0 ? " / " + humanBytes(cap) : "");
      }
      if (mode === "rate") return humanBytes(v) + "/s";
      return v.toFixed(1) + (unit ? " " + unit : "");
    }

    _renderHead(v1, v2) {
      if (!this._label) return;
      this._label.textContent = this.getAttribute("label") || "";
      const cur1 = v1 != null ? v1 : this._a[this._a.length - 1];
      const cur2 = v2 != null ? v2 : this._b[this._b.length - 1];
      if (this.hasAttribute("vb")) {
        this._valEl.innerHTML =
          '<span style="color:' + this._color(1) + '">' + this._fmt(cur1) + "</span>" +
          ' <span class="mc-sep">/</span> ' +
          '<span style="color:' + this._color(2) + '">' + this._fmt(cur2) + "</span>";
      } else {
        this._valEl.textContent = this._fmt(cur1);
      }
    }

    _resize() {
      if (!this._canvas) return;
      const w = this.clientWidth || 1;
      this._canvas.width = Math.max(1, Math.floor(w * dpr()));
      this._canvas.height = Math.floor(HEIGHT * dpr());
      this._canvas.style.height = HEIGHT + "px";
      this._scheduleDraw();
    }

    _scheduleDraw() {
      if (this._raf || !this._ctx) return;
      this._raf = requestAnimationFrame(() => {
        this._raf = null;
        this._draw();
      });
    }

    _drawSeries(ctx, arr, color, w, h, maxv) {
      if (arr.length < 2) return;
      const n = arr.length;
      const stepX = w / (MAXPTS - 1);
      const x0 = w - (n - 1) * stepX;
      const pad = dpr() * 2;
      const y = (v) => h - pad - (Math.min(v, maxv) / maxv) * (h - 2 * pad);

      // Filled area.
      ctx.beginPath();
      arr.forEach((v, i) => {
        const x = x0 + i * stepX;
        i === 0 ? ctx.moveTo(x, y(v)) : ctx.lineTo(x, y(v));
      });
      ctx.lineTo(x0 + (n - 1) * stepX, h);
      ctx.lineTo(x0, h);
      ctx.closePath();
      const grad = ctx.createLinearGradient(0, 0, 0, h);
      grad.addColorStop(0, color + "55");
      grad.addColorStop(1, color + "00");
      ctx.fillStyle = grad;
      ctx.fill();

      // Line.
      ctx.beginPath();
      arr.forEach((v, i) => {
        const x = x0 + i * stepX;
        i === 0 ? ctx.moveTo(x, y(v)) : ctx.lineTo(x, y(v));
      });
      ctx.strokeStyle = color;
      ctx.lineWidth = dpr() * 1.5;
      ctx.lineJoin = "round";
      ctx.stroke();
    }

    _draw() {
      const ctx = this._ctx;
      if (!ctx) return;
      const w = this._canvas.width;
      const h = this._canvas.height;
      ctx.clearRect(0, 0, w, h);

      let maxv;
      if (this._mode === "percent") {
        maxv = Math.max(100, ...this._a);
      } else if (this._mode === "bytes") {
        const c = this._num("cap");
        maxv = c > 0 ? c : Math.max(1, ...this._a);
      } else {
        maxv = Math.max(1, ...this._a, ...this._b);
      }

      this._drawSeries(ctx, this._a, this._color(1), w, h, maxv);
      if (this.hasAttribute("vb")) {
        this._drawSeries(ctx, this._b, this._color(2), w, h, maxv);
      }
    }
  }

  if (!customElements.get("metric-chart")) {
    customElements.define("metric-chart", MetricChart);
  }
})();
