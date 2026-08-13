/*
 * dashboard.js — confirm dialogs for the renew links on the dashboard.
 */
document.querySelectorAll('.confirm-renew').forEach(function (el) {
  el.addEventListener('click', function (e) {
    e.preventDefault();
    var href = this.getAttribute('href');
    var name = this.getAttribute('data-name') || 'certificate';
    var f = document.createElement('form');
    f.setAttribute('data-confirm', 'Renew certificate "' + name + '"? The current file will be replaced.');
    f.setAttribute('data-confirm-btn', 'Renew');
    f.setAttribute('data-confirm-class', 'warning');
    f.setAttribute('data-confirm-title', 'Renew certificate');
    f.submit = function () { window.location.href = href; };
    document.body.appendChild(f);
    f.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  });
});
