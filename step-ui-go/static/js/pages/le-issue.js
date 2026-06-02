/*
 * le-issue.js — show/hide provider-specific help panels on the LE issue page.
 * The initial provider value is read from data-initial on #providerSelect.
 */
function showProviderHelp(v) {
  document.querySelectorAll('.le-help').forEach(function (el) { el.style.display = 'none'; });
  var map = { http01: 'helpHttp01', cloudflare: 'helpCloudflare', route53: 'helpRoute53' };
  if (map[v]) {
    var el = document.getElementById(map[v]);
    if (el) { el.style.display = 'block'; }
  }
}

(function () {
  var sel = document.getElementById('providerSelect');
  if (!sel) { return; }
  sel.addEventListener('change', function () { showProviderHelp(this.value); });
  var initial = sel.getAttribute('data-initial') || sel.value;
  showProviderHelp(initial);
})();
