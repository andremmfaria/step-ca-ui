/*
 * select.js — auto-wraps <select> elements in a custom UI widget
 * Included from base.html and admin_base.html
 */

// Remove the legacy theme key (now stored server-side)
try{localStorage.removeItem('step-ui-theme');}catch(e){}
var _sidebarOpen = true;
(function(){
  var saved = localStorage.getItem('step-ui-sidebar');
  if(saved === '0'){
    _sidebarOpen = false;
    document.getElementById('sidebar').classList.add('collapsed');
    document.body.classList.add('sb-collapsed');
    var bf = document.getElementById('burgerFloat');
    if(bf) bf.classList.add('visible');
  }
})();
function toggleSidebar(){
  _sidebarOpen = !_sidebarOpen;
  document.getElementById('sidebar').classList.toggle('collapsed', !_sidebarOpen);
  document.body.classList.toggle('sb-collapsed', !_sidebarOpen);
  var bf = document.getElementById('burgerFloat');
  if(bf) bf.classList.toggle('visible', !_sidebarOpen);
  localStorage.setItem('step-ui-sidebar', _sidebarOpen ? '1' : '0');
}

function toggleGroup(id){
  var grp = document.getElementById(id);
  if(!grp) return;
  var toggle = grp.querySelector('.nav-group-toggle');
  var items  = grp.querySelector('.nav-group-items');
  toggle.classList.toggle('open');
  items.classList.toggle('open');
}

function initCustomSelect(wrap){
  var sel = wrap.querySelector('select');
  if(!sel) return;
  var multi = sel.multiple;
  var box   = wrap.querySelector('.sel-box');
  var drop  = wrap.querySelector('.sel-dropdown');
  if(!box || !drop) return;

  /* ── ARIA wiring ── */
  var labelledBy = sel.id ? document.querySelector('label[for="'+sel.id+'"]') : null;
  box.setAttribute('role', 'combobox');
  box.setAttribute('aria-haspopup', 'listbox');
  box.setAttribute('aria-expanded', 'false');
  if(!box.hasAttribute('tabindex')) box.setAttribute('tabindex', '0');
  if(labelledBy) box.setAttribute('aria-labelledby', labelledBy.id || (labelledBy.id = 'lbl-'+sel.id));
  drop.setAttribute('role', 'listbox');
  if(multi) drop.setAttribute('aria-multiselectable', 'true');

  function renderLabel(){
    var chosen = Array.from(sel.selectedOptions).map(function(o){return o.text;});
    var lbl = box.querySelector('.sel-label');
    if(lbl) lbl.textContent = chosen.length ? chosen.join(', ') : (sel.options[0]||{text:''}).text;
    box.setAttribute('aria-label', lbl ? lbl.textContent : '');
  }
  function buildOpts(){
    drop.innerHTML = '';
    Array.from(sel.options).forEach(function(o){
      var div = document.createElement('div');
      div.className = 'sel-opt' + (o.selected ? ' selected' : '');
      div.dataset.val = o.value;
      div.setAttribute('role', 'option');
      div.setAttribute('aria-selected', o.selected ? 'true' : 'false');
      div.setAttribute('tabindex', '-1');
      if(multi){
        div.innerHTML = '<span class="chk" aria-hidden="true">'+(o.selected?'&#10003;':'')+'</span>'+o.text;
      } else {
        div.innerHTML = '<span class="sel-check" aria-hidden="true">'+(o.selected?'&#10003;':'')+'</span>'+o.text;
      }
      div.addEventListener('mousedown', function(e){
        e.preventDefault();
        if(multi){
          o.selected = !o.selected;
          div.classList.toggle('selected', o.selected);
          div.setAttribute('aria-selected', o.selected ? 'true' : 'false');
          var chk = div.querySelector('.chk');
          if(chk) chk.innerHTML = o.selected ? '&#10003;' : '';
        } else {
          Array.from(drop.querySelectorAll('.sel-opt')).forEach(function(d){
            d.classList.remove('selected');
            d.setAttribute('aria-selected', 'false');
            var sc = d.querySelector('.sel-check'); if(sc) sc.innerHTML='';
          });
          div.classList.add('selected');
          div.setAttribute('aria-selected', 'true');
          var sc = div.querySelector('.sel-check'); if(sc) sc.innerHTML='&#10003;';
          sel.value = o.value;
          closeDropdown();
        }
        renderLabel();
        sel.dispatchEvent(new Event('change',{bubbles:true}));
      });
      drop.appendChild(div);
    });
  }
  function openDropdown(){
    buildOpts();
    box.classList.add('open');
    drop.classList.add('open');
    box.setAttribute('aria-expanded', 'true');
  }
  function closeDropdown(){
    box.classList.remove('open');
    drop.classList.remove('open');
    box.setAttribute('aria-expanded', 'false');
  }
  /* keyboard navigation for accessibility */
  box.addEventListener('keydown', function(e){
    var isOpen = drop.classList.contains('open');
    if(e.key === 'Enter' || e.key === ' '){
      e.preventDefault();
      isOpen ? closeDropdown() : openDropdown();
    } else if(e.key === 'Escape' && isOpen){
      e.preventDefault();
      closeDropdown();
      box.focus();
    } else if((e.key === 'ArrowDown' || e.key === 'ArrowUp') && isOpen){
      e.preventDefault();
      var opts = Array.from(drop.querySelectorAll('.sel-opt'));
      var focused = drop.querySelector('.sel-opt:focus');
      var idx = opts.indexOf(focused);
      var next = e.key === 'ArrowDown' ? Math.min(idx+1, opts.length-1) : Math.max(idx-1, 0);
      if(opts[next]) opts[next].focus();
    }
  });
  box.addEventListener('click', function(){ drop.classList.contains('open') ? closeDropdown() : openDropdown(); });
  document.addEventListener('click', function(e){ if(!wrap.contains(e.target)) closeDropdown(); });
  renderLabel();
}
// Auto-wrap all <select> elements on the page into a custom select-wrap
function autoWrapSelects(){
  document.querySelectorAll('select').forEach(function(sel){
    if(sel.closest('.select-wrap')) return; // already wrapped
    if(sel.hasAttribute('data-native')) return; // marked as "leave alone"
    var wrap = document.createElement('div');
    wrap.className = 'select-wrap';
    // Inherit width from the original select
    var w = sel.style.width || sel.getAttribute('width');
    if(w) wrap.style.width = w;
    else if(sel.offsetWidth) wrap.style.width = sel.offsetWidth + 'px';
    sel.parentNode.insertBefore(wrap, sel);
    wrap.appendChild(sel);
    var box = document.createElement('div');
    box.className = 'sel-box';
    box.innerHTML = '<span class="sel-label"></span><span class="sel-arrow"></span>';
    wrap.appendChild(box);
    var drop = document.createElement('div');
    drop.className = 'sel-dropdown';
    wrap.appendChild(drop);
    initCustomSelect(wrap);
  });
}
autoWrapSelects();


/* ── burger buttons ── */
(function(){
  var bf=document.getElementById('burgerFloat');
  var bb=document.getElementById('burgerBtn');
  if(bf) bf.addEventListener('click', toggleSidebar);
  if(bb) bb.addEventListener('click', toggleSidebar);
})();

/* ── nav-group toggles (onclick replaced with data-group) ── */
document.querySelectorAll('.nav-group-toggle[data-group]').forEach(function(el){
  el.addEventListener('click', function(){ toggleGroup(el.getAttribute('data-group')); });
});

var _mf=null;
function showConfirm(form,msg,btn,cls,title){
  _mf=form;
  document.getElementById('modalMsg').textContent=msg;
  document.getElementById('modalTitle').textContent=title||'Confirm action';
  var b=document.getElementById('modalConfirmBtn');
  b.textContent=btn||'Confirm';
  b.className='modal-btn-confirm '+(cls||'danger');
  document.getElementById('confirmModal').classList.add('active');
}
function closeModal(){document.getElementById('confirmModal').classList.remove('active');_mf=null;}
function modalConfirm(){if(_mf){var s=_mf.submit.bind(_mf);_mf.removeAttribute('data-confirm');s();}closeModal();}
(function(){
  var m=document.getElementById('confirmModal');
  if(m){m.addEventListener('click',function(e){if(e.target===this)closeModal();});}
  var cancel=document.getElementById('modalCancelBtn');
  if(cancel) cancel.addEventListener('click', closeModal);
  var confirm=document.getElementById('modalConfirmBtn');
  if(confirm) confirm.addEventListener('click', modalConfirm);
})();
document.addEventListener('submit',function(e){
  var f=e.target,msg=f.getAttribute('data-confirm');
  if(msg){e.preventDefault();showConfirm(f,msg,f.getAttribute('data-confirm-btn'),f.getAttribute('data-confirm-class'),f.getAttribute('data-confirm-title'));}
});

// Auto-open the nav group for the currently active page
(function(){
  var activeLink = document.querySelector('.nav-group-items a.active');
  if(activeLink){
    var items = activeLink.closest('.nav-group-items');
    var grp   = activeLink.closest('.nav-group');
    if(items && grp){
      items.classList.add('open');
      var toggle = grp.querySelector('.nav-group-toggle');
      if(toggle) toggle.classList.add('open');
    }
  }
})();
