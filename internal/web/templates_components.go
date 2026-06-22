package web

const sharedCSS = `
:root,[data-theme=light]{--bg:#f0f2f5;--text:#1a1a2e;--sidebar-bg:#e8ecf1;--sidebar-text:#1a1a2e;--sidebar-active-bg:var(--accent);--sidebar-active-text:#fff;--sidebar-hover:#d0d7e0;--card-bg:#fff;--border:#d0d7e0;--accent:#06c;--muted:#667;--danger:#c00;--ok:#0a0;--group-row-bg:#eef2f7;--save-bar-bg:#e8ecf1}
[data-theme=dark]{--bg:#0d1117;--text:#c9d1d9;--sidebar-bg:#161b22;--sidebar-text:#c9d1d9;--sidebar-active-bg:var(--accent);--sidebar-active-text:#fff;--sidebar-hover:#21262d;--card-bg:#161b22;--border:#30363d;--accent:#58a6ff;--muted:#8b949e;--danger:#f85149;--ok:#3fb950;--group-row-bg:#1c2433;--save-bar-bg:#1c2433}
@media(prefers-color-scheme:dark){:root:not([data-theme]){--bg:#0d1117;--text:#c9d1d9;--sidebar-bg:#161b22;--sidebar-text:#c9d1d9;--sidebar-active-bg:var(--accent);--sidebar-active-text:#fff;--sidebar-hover:#21262d;--card-bg:#161b22;--border:#30363d;--accent:#58a6ff;--muted:#8b949e;--danger:#f85149;--ok:#3fb950;--group-row-bg:#1c2433;--save-bar-bg:#1c2433}}

*{box-sizing:border-box;margin:0;padding:0}
body{font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--text);min-height:100vh}
a{color:var(--accent)}
h1{font-size:1.3em;margin-bottom:.75rem}
.card{background:var(--card-bg);border-radius:8px;padding:1.25rem;margin-bottom:1rem;border:1px solid var(--border)}
.card h2{font-size:1.1em;margin-bottom:.75rem;padding-bottom:.5rem;border-bottom:1px solid var(--border)}
.muted{color:var(--muted)}
.error{color:var(--danger)}.ok{color:var(--ok)}

table{width:100%;border-collapse:collapse}
th{text-align:left;padding:8px;border-bottom:2px solid var(--border);font-weight:600;font-size:.85em;color:var(--muted);text-transform:uppercase;letter-spacing:.03em}
td{padding:8px;border-bottom:1px solid var(--border)}
tr:hover td:not(.group-row td){background:var(--group-row-bg)}

label{display:block;margin-bottom:12px;font-weight:500}
label input,label select{margin-top:4px;display:block;width:100%}
label input[type=checkbox]{display:inline;width:auto;margin-top:0;margin-right:6px}
input,select,textarea{padding:8px 10px;border:1px solid var(--border);border-radius:6px;background:var(--card-bg);color:var(--text);font-size:.9em;font-family:inherit}
input:focus,select:focus,textarea:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 2px color-mix(in srgb,var(--accent) 30%,transparent)}

button,.button{padding:8px 16px;border-radius:6px;cursor:pointer;border:1px solid var(--border);background:var(--card-bg);color:var(--text);font-size:.9em;font-family:inherit;font-weight:500;transition:all .15s}
button:hover,.button:hover{opacity:.9}
button.primary,.button.primary{background:var(--accent);color:#fff;border-color:var(--accent)}
button.danger,.button.danger{background:var(--danger);color:#fff;border-color:var(--danger)}
button:disabled{opacity:.5;cursor:not-allowed}

.form-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.form-grid label{margin-bottom:0}
.checkbox-row{display:flex;align-items:center;gap:8px;margin-bottom:8px}
.checkbox-row label{margin-bottom:0;display:flex;align-items:center;gap:6px;font-weight:400}
.checkbox-row label input[type=checkbox]{margin:0}

.stats-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:1rem;margin-bottom:1rem}
.stat-card{background:var(--card-bg);border-radius:8px;padding:1.25rem;border:1px solid var(--border);text-align:center}
.stat-card .value{font-size:2em;font-weight:bold;color:var(--accent)}
.stat-card .label{font-size:.85em;color:var(--muted);margin-top:.3rem}
.status-dot{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:6px}
.status-dot.up{background:var(--ok)}.status-dot.down{background:var(--danger)}

.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(250px,1fr));gap:1rem}
.empty{text-align:center;padding:2rem;color:var(--muted)}
.pill{font-size:.75em;color:var(--muted);border:1px solid var(--border);padding:1px 6px;border-radius:8px;white-space:nowrap}

.community-tag{font-size:.75em;color:var(--muted);margin-left:4px;font-family:ui-monospace,monospace}
.htmx-indicator{opacity:.5}
.prefix-count{font-size:.8em;color:var(--muted);margin-left:.3em;white-space:nowrap}
.loading-dots span{animation:wave 1.2s infinite;font-size:1.2em;font-weight:bold;color:var(--muted)}
.loading-dots span:nth-child(1){animation-delay:0s}
.loading-dots span:nth-child(2){animation-delay:.2s}
.loading-dots span:nth-child(3){animation-delay:.4s}
@keyframes wave{0%,60%,100%{opacity:.2;transform:translateY(0)}30%{opacity:1;transform:translateY(-3px)}}

.community-value{color:var(--accent);cursor:pointer;text-decoration:underline;font-family:ui-monospace,monospace}
.community-value:hover{opacity:.8}
.community-cell{white-space:nowrap;display:inline-flex;align-items:center;gap:2px}
.community-input{width:7ch;padding:2px 4px;border:1px solid var(--border);border-radius:3px;background:var(--card-bg);color:var(--text);font-family:ui-monospace,monospace;font-size:.9em}
.community-input::-webkit-inner-spin-button,.community-input::-webkit-outer-spin-button{-webkit-appearance:none;margin:0}
.community-input{-moz-appearance:textfield}
.edit-actions{display:none;align-items:center;gap:2px}
.apply-btn{color:var(--ok);background:0 0;border:1px solid var(--ok);cursor:pointer;padding:2px 6px;margin-left:4px;border-radius:3px;font-size:.85em}
.cancel-btn{color:var(--danger);background:0 0;border:1px solid var(--danger);cursor:pointer;padding:2px 6px;margin-left:2px;border-radius:3px;font-size:.85em}
.revert-btn{color:#c90;background:0 0;border:none;cursor:pointer;font-size:1.1em;margin-left:4px;padding:0 2px}
.group-row td{background:var(--group-row-bg);font-weight:600}
.communities-table th:last-child,.communities-table td:last-child{min-width:180px}
.save-bar{position:sticky;top:0;z-index:10;display:flex;gap:1rem;align-items:center;justify-content:space-between;background:var(--save-bar-bg);padding:.8rem 1.25rem;box-shadow:0 4px 16px #00000018;margin-bottom:1rem}
.save-bar .muted{color:var(--text);opacity:.7}.save-bar button{background:var(--accent);color:#fff}.save-bar button:disabled{background:var(--muted);cursor:not-allowed}

.selection-form{padding-bottom:5.5rem}
.catalog-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:1rem;margin-top:1rem}
.category-card{background:var(--card-bg);border:1px solid var(--border);border-radius:8px;padding:1rem;margin-top:1rem}
.category-card legend{background:var(--card-bg);padding:0 .5rem;font-size:1em;font-weight:600;line-height:1.4}
.category-title{font-weight:600;display:inline-flex;align-items:center;gap:6px;cursor:pointer;flex-wrap:wrap;margin-bottom:0}
.category-title input[type=checkbox]{margin:0}
.service-list{display:flex;flex-direction:column;gap:4px;margin-top:6px;padding-left:1.5rem}
.service-list label{display:flex;align-items:center;gap:4px;font-size:.9em;margin-bottom:0}
.service-list label input[type=checkbox]{margin:0}

.cred-row{display:flex;gap:1rem;align-items:flex-end;margin-bottom:8px}
.cred-row label{margin-bottom:0;flex:1}
.error-output{background:var(--card-bg);border:1px solid var(--danger);border-radius:6px;padding:.75rem;margin-top:.5rem;font-family:ui-monospace,monospace;font-size:.85em;white-space:pre-wrap;max-height:300px;overflow-y:auto}

.row-actions{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap;margin-top:.5rem}

.language-switcher{display:flex;justify-content:flex-end;gap:.5rem;margin-bottom:.75rem}
.language-switcher a[aria-current=page]{font-weight:700;text-decoration:none;color:var(--text)}

textarea.large{width:100%;min-height:6em;font-family:ui-monospace,monospace;font-size:.85em;padding:8px;resize:vertical}

.back-link{display:inline-block;color:var(--muted);text-decoration:none;margin-bottom:.75rem;font-size:.9em}
.back-link:hover{color:var(--accent)}

button.secondary{background:var(--muted);color:#fff;border-color:var(--muted)}

form{margin:1rem 0}
code{font-size:.9em}
header{display:flex;gap:1rem;justify-content:space-between;align-items:center;margin:0 0 1rem}
.tab-bar{display:flex;gap:0;border-bottom:2px solid var(--border);margin-bottom:1.25rem}
.tab{padding:8px 16px;border:none;background:none;color:var(--muted);cursor:pointer;font-size:.9em;font-weight:500;border-bottom:2px solid transparent;margin-bottom:-2px;transition:all .15s}
.tab.active{color:var(--accent);border-bottom-color:var(--accent)}
.tab:hover:not(.active){color:var(--text)}
.settings-grid{display:grid;grid-template-columns:max-content 1fr;align-items:stretch}
.section-head{grid-column:1/-1;padding:2rem 0 .25rem}
.section-head:first-child{padding-top:.25rem}
.section-head h2{font-size:1.2em;font-weight:700;margin:0}
.setting-row{display:contents}
.setting-label{white-space:nowrap;padding:.5rem .75rem .5rem 0;font-weight:500;display:flex;align-items:center;gap:.4rem;border-bottom:1px solid var(--border);line-height:1.5}
.setting-label label{margin:0}
.setting-field{padding:.5rem 0;display:flex;align-items:center;gap:.5rem;flex-wrap:wrap;border-bottom:1px solid var(--border);position:relative}
.setting-field input,.setting-field select{width:100%;max-width:300px}
.setting-field input[type=checkbox]{width:auto}
select:disabled,input:disabled{opacity:.5;cursor:not-allowed;background:var(--bg)}
.setting-row:last-of-type .setting-label,.setting-row:last-of-type .setting-field{border-bottom:none}
.hint-btn{background:var(--border);border:none;border-radius:50%;width:20px;height:20px;line-height:1;font-size:.75em;font-weight:700;cursor:pointer;color:var(--muted);display:inline-flex;align-items:center;justify-content:center;flex-shrink:0;vertical-align:middle;margin-left:4px}
.hint-btn:hover{background:var(--accent);color:#fff}
.hint-popup{position:absolute;left:0;top:100%;z-index:20;background:var(--card-bg);border:1px solid var(--border);border-radius:8px;padding:.75rem 1rem;box-shadow:0 4px 16px #00000030;max-width:420px;font-size:.85em;line-height:1.5;margin-top:4px}
.hint-popup p{margin:0 0 .5rem}
.hint-popup code{font-size:.8em;color:var(--muted)}
[x-cloak]{display:none!important}
.card form + form{margin-top:1rem}
`

const formFieldComponent = `{{define "form-field"}}
<label>{{tr .Label}} <input{{if .Type}} type="{{.Type}}"{{end}} name={{.Name}} value="{{.Value}}"{{if .Placeholder}} placeholder="{{.Placeholder}}"{{end}}{{if .Required}} required{{end}}{{.Attrs}}></label>
{{if .Hint}}<p class=muted>{{tr .Hint}}</p>{{end}}
{{end}}`

const formCheckboxComponent = `{{define "form-checkbox"}}
<label class=checkbox-row><input type=checkbox name={{.Name}} {{if .Checked}}checked {{end}}{{.Attrs}}> {{tr .Label}}</label>
{{if .Hint}}<p class=muted>{{tr .Hint}}</p>{{end}}
{{end}}`

const formSelectComponent = `{{define "form-select"}}
<label>{{tr .Label}} <select name={{.Name}}{{.Attrs}}>{{range .Options}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Text}}</option>{{end}}</select></label>
{{if .Hint}}<p class=muted>{{tr .Hint}}</p>{{end}}
{{end}}`

const sharedComponents = formFieldComponent + formCheckboxComponent + formSelectComponent + `{{define "error-message"}}{{if .}}<p class=error>{{.}}</p>{{end}}{{end}}
{{define "section-header"}}<h3>{{.}}</h3>{{end}}
{{define "back-link"}}<a href="{{.URL}}" class=back-link>← {{.Label}}</a>{{end}}
{{define "checkbox-row"}}<label class=checkbox-row><input type=checkbox name="{{.Name}}" {{if .Checked}}checked{{end}} {{if .Disabled}}disabled{{end}}> {{.Label}}</label>{{if .Hint}}<p class=muted>{{.Hint}}</p>{{end}}{{end}}
`

const selectionBody = `{{$selection := .}}
{{if not .Admin}}<header><h1>{{.User.Name}}</h1><span>
{{if .SessionUser}}<a href=/logout>{{tr "user.logout"}}</a>{{else}}<a href=/login>{{tr "title.login"}}</a>{{end}}
 · <a href="/admin">{{tr "admin.link"}}</a></span></header>{{end}}
<div class="tab-content tab-selection">
<section class=card><h2>{{tr "selection.heading"}}</h2>
<p class=muted>{{tr "selection.category_hint"}}</p>
<label>{{tr "catalog.mode"}}
<select id=catalog-mode-select {{if not .CanChangeMode}}disabled{{end}}>
{{range .Modes}}<option value="{{.ID}}" {{if eq .ID $selection.User.CatalogModeID}}selected{{end}}>{{.Name}}{{if not .Enabled}} ({{tr "catalog.disabled"}}){{end}}</option>{{end}}
</select></label>
{{if not .CanChangeMode}}<p class=muted>{{tr "catalog.managed"}}</p>{{end}}
{{if eq .Saved "1"}}<p class=ok>{{tr "selection.saved"}}</p>{{else if eq .Saved "0"}}<p class=error>{{tr "selection.save_failed"}}</p>{{end}}
{{if not .Editable}}<p class=muted>{{tr "selection.locked"}}</p>{{end}}
<div class=save-bar><div><strong>{{.User.Name}}</strong><br><span class=muted>{{tr "selection.selected"}}
<span id=selected-category-count>{{.SelectedCategoryCount}}</span>
<span id=selected-category-label data-one="{{tr "selection.category_one"}}" data-few="{{tr "selection.category_few"}}" data-many="{{tr "selection.category_many"}}">{{plural .SelectedCategoryCount "selection.category_one" "selection.category_few" "selection.category_many"}}</span>
(<span id=selected-covered-service-count>{{.SelectedCoveredServices}}</span>
<span id=selected-covered-service-label data-one="{{tr "selection.service_one"}}" data-few="{{tr "selection.service_few"}}" data-many="{{tr "selection.service_many"}}">{{plural .SelectedCoveredServices "selection.service_one" "selection.service_few" "selection.service_many"}}</span> {{tr "selection.in_categories"}}).
<span id=selected-service-count>{{.SelectedServiceCount}}</span>
<span id=selected-service-label data-one="{{tr "selection.standalone_one"}}" data-few="{{tr "selection.standalone_few"}}" data-many="{{tr "selection.standalone_many"}}">{{plural .SelectedServiceCount "selection.standalone_one" "selection.standalone_few" "selection.standalone_many"}}</span>.
{{tr "selection.apply_hint"}}</span><br><span class=muted id=prefix-count>IPv4: <strong id=total-prefix-v4>{{.TotalPrefixesV4}}</strong> pref. · IPv6: <strong id=total-prefix-v6>{{.TotalPrefixesV6}}</strong> pref.</span></div>
<button {{if and (not .Editable) (not .CanChangeMode)}}disabled{{end}} form=selection-form>{{tr "selection.save"}}</button></div>
<form id=selection-form class=selection-form method=post action="{{if .Admin}}/admin/user/{{.User.ID}}{{else}}/selection{{end}}">
{{if .Admin}}<input type=hidden name=action value=selection>{{end}}
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=catalog_mode_id value="{{.User.CatalogModeID}}">
{{if .Categories}}<div class=catalog-grid>{{range .Categories}}{{$cat := .Name}}
<fieldset class=category-card><legend><label class=category-title><input type=checkbox name=category value="{{.Name}}" data-prefixes="{{.PrefixCountV4}}" {{if .Selected}}checked{{end}} {{if not $selection.Editable}}disabled{{end}}> <strong>{{.Name}}</strong>{{if index $selection.Communities .Name}} <span class=community-tag title="{{tr "communities.group_community"}}">{{index $selection.Communities .Name}}</span>{{end}} <span class=prefix-count title="{{tr "selection.prefix_count"}}">{{index $selection.CategoryCountsV4 .Name}}{{if index $selection.CategoryCountsV6 .Name}}+{{index $selection.CategoryCountsV6 .Name}}{{end}} pref.</span></label></legend>
<div class=service-list>{{range .Services}}<label><input type=checkbox name=service value="{{.Value}}" data-prefixes="{{.PrefixCountV4}}" {{if .Selected}}checked{{end}} {{if or (not $selection.Editable) .Disabled}}disabled{{end}}> {{.Name}}{{if index $selection.Communities (printf "%s|%s" $cat .Name)}} <span class=community-tag title="{{tr "communities.service_community"}}">{{index $selection.Communities (printf "%s|%s" $cat .Name)}}</span>{{end}} <span class=prefix-count title="{{tr "selection.prefix_count"}}">{{.PrefixCountV4}}{{if .PrefixCountV6}}+{{.PrefixCountV6}}{{end}} pref.</span></label>{{end}}</div>
</fieldset>{{end}}</div>{{else}}<p class=empty>{{tr "selection.empty"}}</p>{{end}}</form></section>
<script>
var catalogModeSelect = document.getElementById('catalog-mode-select');
if (catalogModeSelect && !catalogModeSelect.disabled) {
  catalogModeSelect.addEventListener('change', function() {
    var target = new URL(window.location.href);
    target.searchParams.set('mode', catalogModeSelect.value);
    window.location.href = target.toString();
  });
}
function selectionPluralForm(count) {
  if (document.documentElement.lang !== 'ru') {
    return count === 1 ? 'one' : 'many';
  }
  var mod10 = count % 10;
  var mod100 = count % 100;
  if (mod10 === 1 && mod100 !== 11) return 'one';
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return 'few';
  return 'many';
}
function updateSelectionLabel(id, count) {
  var label = document.getElementById(id);
  if (label) label.textContent = label.dataset[selectionPluralForm(count)];
}
function updateSelectionCounts() {
  var categoryCount = 0;
  var coveredServiceCount = 0;
  var standaloneServiceCount = 0;
  document.querySelectorAll('fieldset.category-card').forEach(function(fieldset) {
    var categoryInput = fieldset.querySelector('input[name="category"]');
    var services = fieldset.querySelectorAll('input[name="service"]');
    if (categoryInput && categoryInput.checked) {
      categoryCount++;
      coveredServiceCount += services.length;
    } else {
      services.forEach(function(serviceInput) {
        if (serviceInput.checked) standaloneServiceCount++;
      });
    }
  });
  document.getElementById('selected-category-count').textContent = categoryCount;
  document.getElementById('selected-covered-service-count').textContent = coveredServiceCount;
  document.getElementById('selected-service-count').textContent = standaloneServiceCount;
  updateSelectionLabel('selected-category-label', categoryCount);
  updateSelectionLabel('selected-covered-service-label', coveredServiceCount);
  updateSelectionLabel('selected-service-label', standaloneServiceCount);
  // Trigger exact count from backend
  var form = document.getElementById('selection-form');
  var countEl = document.getElementById('prefix-count');
  if (countEl) {
    countEl.innerHTML = '<span class=loading-dots><span>.</span><span>.</span><span>.</span></span>';
  }
  var xhr = new XMLHttpRequest();
  xhr.open('POST', '/selection/count');
  xhr.onload = function() {
    if (xhr.status === 200) countEl.innerHTML = xhr.responseText;
  };
  xhr.send(new FormData(form));
}
document.querySelectorAll('input[name="category"]').forEach(function(categoryInput) {
  var fieldset = categoryInput.closest('fieldset');
  var services = fieldset ? fieldset.querySelectorAll('input[name="service"]') : [];
  var update = function() {
    services.forEach(function(serviceInput) {
      serviceInput.disabled = categoryInput.checked || categoryInput.disabled;
    });
    updateSelectionCounts();
  };
  categoryInput.addEventListener('change', update);
  services.forEach(function(serviceInput) { serviceInput.addEventListener('change', update); });
  update();
});
updateSelectionCounts();
</script>
</div>
<div class="tab-content tab-filters">
{{with .Filters}}<section class=card><h2>{{tr "filters.heading"}}</h2>
{{if .Editable}}<p class=muted>{{tr "filters.explanation"}}</p>
<form method=post action="{{if .Admin}}/admin/user/{{$selection.User.ID}}{{else}}/filters{{end}}">
{{if .Admin}}<input type=hidden name=action value=filters>{{end}}
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<label>{{tr "filters.mode"}} <select name=filter_mode>
<option value="global" {{if eq .Mode "global"}}selected{{end}}>{{tr "filters.mode_global"}}</option>
<option value="extend" {{if eq .Mode "extend"}}selected{{end}}>{{tr "filters.mode_extend"}}</option>
<option value="override" {{if eq .Mode "override"}}selected{{end}}>{{tr "filters.mode_override"}}</option>
</select></label>
<div class=grid><label>{{tr "filters.allow"}} <textarea class=large name=filter_allow placeholder="{{tr "filters.allow_placeholder"}}">{{.AllowText}}</textarea></label>
<label>{{tr "filters.deny"}} <textarea class=large name=filter_deny placeholder="1.1.1.1/32">{{.DenyText}}</textarea></label></div>
<button class=primary>{{tr "filters.save"}}</button></form>
{{else}}<p class=muted>{{if eq .Mode "override"}}{{tr "filters.managed_override"}}{{else if eq .Mode "extend"}}{{tr "filters.managed_extend"}}{{else}}{{tr "filters.managed_global"}}{{end}}</p>{{end}}
</section>{{end}}</div>`
