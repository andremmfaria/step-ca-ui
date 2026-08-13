/*
 * admin-users-temp.js — clipboard helpers for the temporary-users page.
 */
function flashCheck(btnEl) {
  if (!btnEl) { return; }
  var old = btnEl.innerHTML;
  btnEl.innerHTML = '<span style="color:#4caf50;font-weight:700">&#10003;</span>';
  btnEl.disabled = true;
  setTimeout(function () { btnEl.innerHTML = old; btnEl.disabled = false; }, 1500);
}

function copyText(text, btnEl) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text)
      .then(function () { flashCheck(btnEl); })
      .catch(function (e) { console.warn('copy failed:', e); }); // eslint-disable-line no-console
  } else {
    /* fallback for HTTP contexts without Clipboard API */
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); flashCheck(btnEl); } catch (e) { /* ignore */ }
    document.body.removeChild(ta);
  }
}

function copyPassword(btnEl) {
  var el = document.getElementById('credPassword');
  if (!el) { return; }
  copyText(el.innerText.trim(), btnEl);
}

function copyShareLine(btnEl) {
  var el = document.getElementById('credShareLine');
  if (!el) { return; }
  copyText(el.innerText.trim(), btnEl);
}

/* wire the copy button via event listener instead of onclick */
(function () {
  var btn = document.querySelector('[data-action="copy-share-line"]');
  if (btn) {
    btn.addEventListener('click', function () { copyShareLine(btn); });
  }
})();
