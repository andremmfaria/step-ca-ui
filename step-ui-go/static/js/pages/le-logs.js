/*
 * le-logs.js — debounce domain filter on the LE logs page.
 */
(function () {
  var inp = document.getElementById('leDomainFilter');
  var form = document.getElementById('leLogsForm');
  if (!inp || !form) { return; }
  var timer = null;
  inp.addEventListener('input', function () {
    clearTimeout(timer);
    timer = setTimeout(function () { form.submit(); }, 400);
  });
})();
