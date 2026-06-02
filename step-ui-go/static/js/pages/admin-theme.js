/*
 * admin-theme.js — apply the system-preference theme for admin pages when
 * data-theme="auto".  The server already writes the initial data-theme on
 * <html>; this script only wires the media-query listener for the "auto" case
 * and cleans up the legacy localStorage key.
 */
(function () {
  try { localStorage.removeItem('step-ui-theme'); } catch (e) { /* ignore */ }
  var t = document.documentElement.getAttribute('data-theme');
  if (t === 'auto') {
    var mq = window.matchMedia('(prefers-color-scheme: dark)');
    var apply = function () {
      document.documentElement.setAttribute('data-theme', mq.matches ? 'dark' : 'light');
    };
    apply();
    mq.addEventListener('change', apply);
  }
})();
