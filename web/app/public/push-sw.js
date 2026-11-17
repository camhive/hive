// Web Push for the Camhive PWA (ADR-0021 §7). The generated service worker
// loads this file with importScripts (vite.config.ts, workbox.importScripts).
// The cloud sends {kind, title, body, path, tag}; no picture, no address.
(function (sw) {
  function inApp(path) {
    // Only paths inside the app: "/hives/…", never "//host" or a full URL.
    return typeof path === "string" && path.charAt(0) === "/" && path.charAt(1) !== "/" ? path : "/";
  }

  sw.addEventListener("push", function (event) {
    var p = {};
    try {
      p = event.data ? event.data.json() : {};
    } catch (e) {
      p = {};
    }
    var title = p.title || "Camhive";
    var options = { body: p.body || "", tag: p.tag || undefined, icon: "/icons/icon-192.v2.png", badge: "/icons/icon-192.v2.png", data: { path: inApp(p.path) } };
    event.waitUntil(sw.registration.showNotification(title, options));
  });

  sw.addEventListener("notificationclick", function (event) {
    event.notification.close();
    var url = sw.location.origin + inApp(event.notification.data && event.notification.data.path);
    event.waitUntil(
      sw.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (windows) {
        for (var i = 0; i < windows.length; i++) {
          if (windows[i].url.indexOf(sw.location.origin) === 0) {
            var w = windows[i];
            // navigate() rejects for a window this worker does not control.
            return w.navigate(url).then(function () { return w.focus(); }, function () { return sw.clients.openWindow(url); });
          }
        }
        return sw.clients.openWindow(url);
      }),
    );
  });
})(self);
