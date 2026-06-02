/*
 * import.js — drag-and-drop file zones and manual-tab path auto-fill.
 */
function fmtSize(bytes) {
  if (bytes < 1024) { return bytes + ' B'; }
  if (bytes < 1024 * 1024) { return (bytes / 1024).toFixed(1) + ' KB'; }
  return (bytes / (1024 * 1024)).toFixed(2) + ' MB';
}

function setupDropZone(zone) {
  if (!zone) { return; }
  var input = zone.querySelector('input[type="file"]');
  var emptyEl = zone.querySelector('.drop-empty');
  var loaded = zone.querySelector('.drop-loaded');
  var nameEl = loaded ? loaded.querySelector('.file-name') : null;
  var sizeEl = loaded ? loaded.querySelector('.file-size') : null;
  var removeB = loaded ? loaded.querySelector('.file-remove') : null;

  function showLoaded(file) {
    if (nameEl) { nameEl.textContent = file.name; }
    if (sizeEl) { sizeEl.textContent = fmtSize(file.size); }
    if (emptyEl) { emptyEl.style.display = 'none'; }
    if (loaded) { loaded.style.display = 'block'; }
    zone.classList.add('has-file');
    if (zone.id === 'dropCrt') {
      var nameField = document.getElementById('certName');
      if (nameField && !nameField.value) {
        nameField.value = file.name.replace(/\.(crt|pem|cer)$/i, '');
      }
    }
  }

  function showEmpty() {
    input.value = '';
    if (emptyEl) { emptyEl.style.display = 'flex'; }
    if (loaded) { loaded.style.display = 'none'; }
    zone.classList.remove('has-file');
  }

  zone.addEventListener('click', function (e) {
    if (zone.classList.contains('has-file')) { return; }
    if (e.target.closest('.file-remove')) { return; }
    input.click();
  });
  input.addEventListener('change', function () {
    if (this.files && this.files[0]) { showLoaded(this.files[0]); }
  });
  if (removeB) {
    removeB.addEventListener('click', function (e) {
      e.stopPropagation();
      showEmpty();
    });
  }
  zone.addEventListener('dragover', function (e) {
    e.preventDefault();
    if (!zone.classList.contains('has-file')) { zone.classList.add('drag-over'); }
  });
  zone.addEventListener('dragleave', function () { zone.classList.remove('drag-over'); });
  zone.addEventListener('drop', function (e) {
    e.preventDefault();
    zone.classList.remove('drag-over');
    if (zone.classList.contains('has-file')) { return; }
    var file = e.dataTransfer.files[0];
    if (!file) { return; }
    var dt = new DataTransfer();
    dt.items.add(file);
    input.files = dt.files;
    showLoaded(file);
  });
}

document.querySelectorAll('.drop-zone').forEach(setupDropZone);

/* auto-fill cert/key paths from the name in the manual tab */
(function () {
  var manName = document.getElementById('manName');
  var crt = document.getElementById('manCrtPath');
  var key = document.getElementById('manKeyPath');
  if (!manName || !crt) { return; }
  var crtTouched = false;
  var keyTouched = false;
  if (crt) { crt.addEventListener('input', function () { crtTouched = true; }); }
  if (key) { key.addEventListener('input', function () { keyTouched = true; }); }
  manName.addEventListener('input', function () {
    var v = this.value.trim();
    if (!v) { return; }
    if (!crtTouched) { crt.value = 'opt/step-ui/certs/' + v + '/certificate.crt'; }
    if (key && !keyTouched) { key.value = 'opt/step-ui/certs/' + v + '/private.key'; }
  });
})();
