/*
 * le-settings.js — show/hide provider-specific settings panels on the
 * Let's Encrypt settings page.
 * The initially selected provider is read from the data-provider attribute
 * on the <select id="provSelect"> element.
 */
function showSettings(v) {
  var cfEl = document.getElementById('cfSettings');
  var r53El = document.getElementById('r53Settings');
  if (cfEl) { cfEl.style.display = v === 'cloudflare' ? 'block' : 'none'; }
  if (r53El) { r53El.style.display = v === 'route53' ? 'block' : 'none'; }
}

(function () {
  var sel = document.getElementById('provSelect');
  if (!sel) { return; }
  sel.addEventListener('change', function () { showSettings(this.value); });
  /* read initial value from data attribute written by the template */
  var initial = sel.getAttribute('data-initial') || sel.value;
  showSettings(initial);
})();
