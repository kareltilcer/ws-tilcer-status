/*
 * status-report.js — copy-in browser crash reporter for status.tilcer.cz.
 *
 * Hooks window.onerror and unhandledrejection and posts to the ingest API with
 * fetch({ keepalive: true }) so reports survive a page unload. Fail-safe: an
 * ingest error is swallowed and never affects the host page.
 *
 * The browser ingest key is PUBLIC by design (like a Sentry DSN): anyone viewing
 * the page can read it. That is an accepted trade-off — the key can only POST
 * crashes for one site, and the endpoint is rate-limited and size-capped.
 *
 * Usage (plain script):
 *   <script src="/status-report.js"></script>
 *   <script>
 *     StatusReport.init({
 *       url: "https://status.tilcer.cz/api/ingest/yarnlog",
 *       key: "ik_your_public_browser_key",
 *       environment: "prod",
 *       release: "yarnlog@2026.31.2",
 *     });
 *   </script>
 *
 * Usage (ES module):
 *   import { init, report } from "./status-report.js";
 *   init({ url, key, environment, release });
 *   try { risky(); } catch (e) { report(e, { context: { where: "checkout" } }); }
 */
(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api; // CommonJS
  root.StatusReport = api; // browser global
})(typeof self !== "undefined" ? self : this, function () {
  let cfg = null;

  function init(options) {
    if (!options || !options.url || !options.key) {
      // Misconfiguration must not throw in the host page.
      console.warn("StatusReport.init: url and key are required");
      return;
    }
    cfg = {
      url: options.url,
      key: options.key,
      environment: options.environment || "",
      release: options.release || "",
    };

    window.addEventListener("error", function (e) {
      const err = e.error || e.message || "unknown error";
      report(err, {
        level: "error",
        context: { source: e.filename, line: e.lineno, col: e.colno },
      });
    });

    window.addEventListener("unhandledrejection", function (e) {
      const reason = e.reason;
      report(reason instanceof Error ? reason : String(reason), {
        level: "error",
        context: { kind: "unhandledrejection" },
      });
    });
  }

  function report(err, opts) {
    if (!cfg) return; // not initialized — no-op
    opts = opts || {};
    let message, stack;
    if (err instanceof Error) {
      message = err.message || err.name || "Error";
      stack = err.stack || "";
    } else {
      message = String(err);
      stack = "";
    }
    const payload = {
      message: message,
      level: opts.level || "error",
      stack: opts.stack || stack || undefined,
      environment: cfg.environment || undefined,
      release: cfg.release || undefined,
      fingerprint: opts.fingerprint || undefined,
      context: opts.context || undefined,
    };
    try {
      fetch(cfg.url, {
        method: "POST",
        keepalive: true, // survive page unload
        headers: { "Content-Type": "application/json", "X-Ingest-Key": cfg.key },
        body: JSON.stringify(payload),
      }).catch(function () {
        /* fail safe: drop ingest errors */
      });
    } catch (_) {
      /* fail safe: never throw from the reporter */
    }
  }

  return { init: init, report: report };
});
