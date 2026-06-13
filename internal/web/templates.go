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
`

const pageStart = `<!DOCTYPE html>
<html lang="{{.Lang}}">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} - wdbgp</title>
<style>` + sharedCSS + `
body{max-width:1100px;margin:0 auto;padding:1.5rem 1rem 3rem}
</style>
</head>
<body>
<nav class=language-switcher aria-label="{{tr "language.label"}}">
<a href="{{.EnglishURL}}" title="{{tr "language.english"}}" aria-current="{{if eq .Lang "en"}}page{{else}}false{{end}}">EN</a>
<a href="{{.RussianURL}}" title="{{tr "language.russian"}}" aria-current="{{if eq .Lang "ru"}}page{{else}}false{{end}}">RU</a>
</nav>`

const pageEnd = `</body></html>`

const accessDeniedTemplate = `<h1>{{tr "access_denied.heading"}}</h1><p>IP: <code>{{.Data}}</code></p>`

const userLoginTemplate = `{{with .Data}}
<header><h1>{{tr "title.login"}}</h1></header>
<section class=card>
{{if .Error}}<p class=error>{{.Error}}</p>{{end}}
<form method=post action=/login>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<label>{{tr "user.login"}} <input name=login required autofocus></label>
<label>{{tr "user.password"}} <input type=password name=password required></label>
<button class=primary>{{tr "title.login"}}</button>
</form>
</section>
{{end}}`

const loginTemplate = `<h1>{{tr "admin.heading"}}</h1>{{if .Data}}<p class=error>{{.Data}}</p>{{end}}
<form method=post><label>{{tr "login.password"}} <input type=password name=password autofocus required></label>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button class=primary>{{tr "login.submit"}}</button></form>`

const adapterTestTemplate = `{{with .Data}}
<header><h1>{{tr "adapters.test_result"}}</h1><a href="/admin/adapter/{{.Adapter.ID}}">{{tr "adapters.back_to_editor"}}</a></header>
<section class=card>
<p><strong>{{tr "adapters.adapter"}}:</strong> {{.Adapter.Name}}</p>
<p><strong>{{tr "adapters.feed"}}:</strong> {{.Feed.Name}} <code>{{.Feed.URL}}</code></p>
<p class=ok>{{printf (tr "adapters.test_success") .TotalEntries}}</p>
{{if .Truncated}}<p class=muted>{{tr "adapters.preview_truncated"}}</p>{{end}}
{{if .Entries}}<table><tr><th>{{tr "catalog.category"}}</th><th>{{tr "catalog.service"}}</th><th>CIDR</th></tr>
{{range .Entries}}<tr><td>{{.Category}}</td><td>{{.Service}}</td><td><code>{{.CIDR}}</code></td></tr>{{end}}</table>
{{else}}<p class=muted>{{tr "adapters.preview_empty"}}</p>{{end}}
</section>{{end}}`

const adapterEditTemplate = `{{with .Data}}
<header><h1>{{.Adapter.Name}}</h1><a href="/admin">{{tr "admin.link"}}</a></header>
<section class=card>
<p><code>{{.Adapter.Key}}</code> · rev. {{.Adapter.Revision}}{{if .Adapter.BuiltIn}} · {{tr "adapters.built_in"}}{{end}}</p>
{{if .Error}}<h2>{{tr "adapters.error"}}</h2><pre class=error-output>{{.Error}}</pre>{{end}}
<form method=post action="/admin/adapter/{{.Adapter.ID}}">
 <input type=hidden name=csrf_token value="{{$.CSRFToken}}">
 <label>{{tr "feeds.name"}} <input name=name value="{{.Adapter.Name}}" required></label>
 <label>{{tr "adapters.allowed_hosts"}} <input name=allowed_hosts value="{{.Adapter.AllowedHosts}}"></label>
 <label>{{tr "adapters.source"}} <textarea name=source class=large rows=30 required>{{.Adapter.Source}}</textarea></label>
 <label>{{tr "adapters.test_feed"}} <select name=feed_id>
 <option value="">{{tr "adapters.select_feed"}}</option>
 {{range .Feeds}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
 </select></label>
 <div class=row-actions><button class=primary>{{tr "common.save"}}</button>
 <button type=submit class=secondary formaction="/admin/adapter/{{.Adapter.ID}}/test">{{tr "adapters.test"}}</button></div>
 </form>
{{if .Adapter.BuiltIn}}<form method=post action="/admin/adapter/{{.Adapter.ID}}/reset" onsubmit="return confirm('{{tr "adapters.reset_confirm"}}');">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button class=danger>{{tr "adapters.reset"}}</button></form>{{end}}
</section>{{end}}`

const selectionBody = `{{$selection := .}}
{{if not .Admin}}<header><h1>{{.User.Name}}</h1><a href="/admin">{{tr "admin.link"}}</a></header>{{end}}
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

const selectionTemplate = `{{with .Data}}` + selectionBody + `{{end}}`

const userEditTemplate = `{{define "selection"}}` + selectionBody + `{{end}}{{with .Data}}
<header><h1>{{printf (tr "title.user") .User.Name}}</h1></header>
<a href="/admin/users" class=back-link>← {{tr "nav.users"}}</a>
<div class=tab-bar>
  <button class="tab active" data-tab=settings>{{tr "user.settings"}}</button>
  <button class=tab data-tab=selection>{{tr "selection.heading"}}</button>
  <button class=tab data-tab=filters>{{tr "filters.heading"}}</button>
</div>
<div class="tab-content tab-settings">
<section class=card><h2>{{tr "user.settings"}}</h2><form method=post action="/admin/user/{{.User.ID}}">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=action value=settings><div class=grid>
<label>{{tr "feeds.name"}} <input name=name value="{{.User.Name}}" required></label><label>{{tr "users.networks"}} <input name=networks value="{{join .User.Networks ", "}}" required></label>
<label>{{tr "users.peer_ip"}} <input name=peer_ip value="{{.User.PeerIP}}" required></label><label>{{tr "users.peer_asn"}} <input type=number min=1 name=peer_asn value="{{.User.PeerASN}}" required></label>
<label>{{tr "users.next_hop"}} <input name=next_hop value="{{.User.NextHop}}"></label><label>{{tr "users.bgp_password"}} <input type=password name=bgp_password placeholder="{{if .User.BGPPassword}}{{tr "user.password_set"}}{{else}}{{tr "user.password_not_set"}}{{end}}"></label></div>
<label>{{tr "catalog.mode"}} <select name=catalog_mode_id>{{range .Selection.Modes}}<option value="{{.ID}}" {{if eq .ID $.Data.User.CatalogModeID}}selected{{end}}>{{.Name}}</option>{{end}}</select></label>
<label><input type=checkbox name=clear_bgp_password> {{tr "user.clear_password"}}</label>
<label><input type=checkbox name=enabled {{if .User.Enabled}}checked{{end}}> {{tr "user.enabled"}}</label>
<label>{{tr "users.web_auth"}} <select name=web_auth>
<option value=network {{if eq .User.WebAuth "network"}}selected{{end}}>{{tr "users.web_auth_network"}}</option>
<option value=login {{if eq .User.WebAuth "login"}}selected{{end}}>{{tr "users.web_auth_login"}}</option>
<option value=both {{if eq .User.WebAuth "both"}}selected{{end}}>{{tr "users.web_auth_both"}}</option>
</select></label>
<p class=muted>{{tr "users.web_auth_hint"}}</p>
<section class=card id=credentials-section>
<h2>{{tr "users.credentials"}}</h2>
{{range $i, $cred := .Credentials}}
<div class=cred-row>
<label>{{tr "user.login"}} <input name="cred_login_{{$i}}" value="{{$cred.Login}}"></label>
<label>{{tr "user.password"}} <input type=password name="cred_password_{{$i}}" placeholder="{{tr "user.password_not_set"}}"></label>
<label><input type=checkbox name="cred_delete_{{$i}}"> {{tr "common.delete"}}</label>
</div>
{{end}}
<div class=cred-row>
<label>{{tr "user.login"}} <input name=cred_login_new placeholder="{{tr "users.new_credential"}}"></label>
<label>{{tr "user.password"}} <input type=password name=cred_password_new></label>
</div>
</section>
<label><input type=checkbox name=locked {{if .User.SelectionLocked}}checked{{end}}> {{tr "user.lock_selection"}}</label>
<label><input type=checkbox name=filter_editable {{if .User.FilterEditable}}checked{{end}}> {{tr "users.allow_filter_editing"}}</label>
<label><input type=checkbox name=catalog_mode_editable {{if .User.CatalogEditable}}checked{{end}}> {{tr "users.allow_mode_editing"}}</label>
<input type=hidden name=filter_mode value="{{.User.FilterMode}}">
<button class=primary>{{tr "user.save"}}</button></form>
<form method=post action="/admin/user/{{.User.ID}}/delete" onsubmit="return confirm('{{tr "user.delete_confirm"}}');"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button class=danger>{{tr "user.delete"}}</button></form></section></div>
{{template "selection" .Selection}}
<script>
(function(){
  var authSelect = document.querySelector('select[name=web_auth]');
  var credSection = document.getElementById('credentials-section');
  function toggleCredentials() {
    var v = authSelect.value;
    credSection.style.display = (v === 'login' || v === 'both') ? '' : 'none';
  }
  if (authSelect) {
    authSelect.addEventListener('change', toggleCredentials);
    toggleCredentials();
  }
})();
</script>
<script>
document.querySelectorAll('.tab-content').forEach(function(c){c.style.display='none'});
document.querySelector('.tab-content.tab-settings').style.display='';
document.querySelectorAll('.tab').forEach(function(t){t.addEventListener('click',function(){document.querySelectorAll('.tab').forEach(function(x){x.classList.remove('active')});this.classList.add('active');document.querySelectorAll('.tab-content').forEach(function(c){c.style.display='none'});document.querySelector('.tab-content.tab-'+this.dataset.tab).style.display=''})});
</script>
{{end}}`

const communitiesBody = `{{with .Data}}
<header><h1>{{tr "communities.title"}}</h1><a href="/admin">{{tr "admin.link"}}</a></header>
{{if .Saved}}<p class=ok>{{if eq .Saved "reset"}}All communities reset to auto-generated values.{{else}}{{tr "communities.saved"}}{{end}}</p>{{end}}
{{if .Error}}<p class=error>{{.Error}}</p>{{end}}
<section class=card>
<label>{{tr "catalog.mode"}} <select id=mode-select>
{{range .Modes}}<option value="{{.ID}}" {{if eq .ID $.Data.Mode.ID}}selected{{end}}>{{.Name}}</option>{{end}}
</select></label>
<div class="save-bar" style="top:0;border-radius:0;margin-bottom:0">
  <div><strong>{{tr "communities.title"}}</strong><br>
  <span class=muted id=change-summary>{{tr "communities.no_changes"}}</span></div>
  <button type=submit class=primary id=save-btn disabled form=communities-form>{{tr "communities.apply"}}</button>
</div>
<form method=post action="/admin/communities" id=communities-form>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=mode value="{{.Mode.ID}}">
<table class=communities-table><tr><th>{{tr "catalog.category"}}</th><th>{{tr "catalog.service"}}</th><th>{{tr "communities.group_community"}}</th></tr>
{{range .Groups}}{{$group := .}}
<tr class=group-row>
<td><strong>{{.Category}}</strong></td><td></td>
<td><span class=community-cell data-name="cat_{{.Category}}" data-value="{{.Community}}" data-auto="{{.AutoGroup}}">
<span class=community-value>{{.Community}}</span>
{{if ne .Community .AutoGroup}}<button type=button class=revert-btn title="{{tr "communities.revert"}}">↺</button>{{end}}
<span class=edit-actions><input type=number class=community-input name="cat_{{.Category}}" min=1 max=4294967295 value="{{.Community}}"><button type=button class=apply-btn title="{{tr "communities.apply"}}">✓</button><button type=button class=cancel-btn title="{{tr "communities.cancel"}}">✗</button></span>
</span></td>
</tr>
{{range .Services}}
<tr>
<td></td><td>{{.Name}}</td>
<td><span class=community-cell data-name="svc_{{$group.Category}}|{{.Name}}" data-value="{{.Community}}" data-auto="{{.AutoSvc}}">
<span class=community-value>{{.Community}}</span>
{{if ne .Community .AutoSvc}}<button type=button class=revert-btn title="{{tr "communities.revert"}}">↺</button>{{end}}
<span class=edit-actions><input type=number class=community-input name="svc_{{$group.Category}}|{{.Name}}" min=1 max=4294967295 value="{{.Community}}"><button type=button class=apply-btn title="{{tr "communities.apply"}}">✓</button><button type=button class=cancel-btn title="{{tr "communities.cancel"}}">✗</button></span>
</span></td>
</tr>{{end}}
{{end}}</table>
<div class=row-actions>
<form method=post action="/admin/communities/reset" onsubmit="return confirm('{{tr "communities.reset_confirm"}}')">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=mode value="{{.Mode.ID}}">
<button type=submit class=danger>{{tr "communities.reset_all"}}</button>
</form>
</div>
</form>
</section>
<script>
(function(){
var modeSelect=document.getElementById('mode-select');
modeSelect.addEventListener('change',function(){window.location.href='/admin/communities?mode='+this.value});
var i18nChangedOne = '{{tr "communities.changed_one"}}';
var i18nChangedMany = '{{tr "communities.changed_many"}}';
var i18nReverted = '{{tr "communities.reverted"}}';

var activeEdit = null;

function closeEdit(cell, forceApply) {
  var inp = cell.querySelector('.community-input');
  if (inp && inp.dataset.changed === '0' && inp.value !== inp.dataset.original) {
    if (forceApply === undefined) {
      if (!confirm('{{tr "communities.apply_edit_confirm"}}')) {
        cell.querySelector('.cancel-btn').click();
        return;
      }
    }
    if (forceApply !== false) {
      cell.querySelector('.apply-btn').click();
      return;
    }
  }
  cell.querySelector('.community-value').style.display = '';
  var revert = cell.querySelector('.revert-btn');
  if (revert) {
    var val = cell.querySelector('.community-value').textContent;
    var auto = cell.dataset.auto;
    revert.style.display = val != auto ? '' : 'none';
  }
  cell.querySelector('.edit-actions').style.display = 'none';
}

function findDuplicate(name,value){var cells=document.querySelectorAll('.community-cell');for(var i=0;i<cells.length;i++){if(cells[i].dataset.name===name)continue;var cellVal=cells[i].querySelector('.community-value').textContent;var inp=cells[i].querySelector('.community-input');if(inp&&inp.dataset.changed==='1')cellVal=inp.value;if(cellVal===value)return true}var hiddens=document.querySelectorAll('.revert-hidden');for(var j=0;j<hiddens.length;j++){if(hiddens[j].value===value)return true}return false}
function countChanged(){return document.querySelectorAll('.community-input[data-changed="1"]').length}
function countReverted(){return document.querySelectorAll('.community-cell.reverted').length}
function updateUI(){
  var edited = countChanged();
  var reverted = countReverted();
  var total = edited + reverted;
  var el = document.getElementById('change-summary');
  var parts = [];
  if (edited > 0) parts.push(edited + ' ' + (edited > 1 ? i18nChangedMany : i18nChangedOne));
  if (reverted > 0) parts.push(reverted + ' ' + i18nReverted);
  el.textContent = parts.length > 0 ? parts.join(', ') : '{{tr "communities.no_changes"}}';
  document.getElementById('save-btn').disabled = total === 0;
}

// Click number → edit mode
document.querySelectorAll('.community-value').forEach(function(v){
v.addEventListener('click',function(){
var cell=this.parentElement;
if (activeEdit && activeEdit !== cell) {
  closeEdit(activeEdit);
}
activeEdit = cell;
cell.querySelector('.community-value').style.display='none';
cell.querySelector('.revert-btn')&&(cell.querySelector('.revert-btn').style.display='none');
cell.querySelector('.edit-actions').style.display='inline-flex';
var inp=cell.querySelector('.community-input');
inp.dataset.original=inp.value;
inp.dataset.changed='0';
inp.focus();inp.select();
});
});

// ✓ Apply
document.querySelectorAll('.apply-btn').forEach(function(b){
b.addEventListener('click',function(){
var cell=this.closest('.community-cell');
var inp=cell.querySelector('.community-input');
var newVal=inp.value;
if(findDuplicate(cell.dataset.name,newVal)){alert('{{tr "communities.duplicate_error"}}');return}
inp.value=newVal;
	// Clean up any revert-hidden input since we're accepting a new manual value
	var oldHidden=cell.querySelector('.revert-hidden');
	if(oldHidden) oldHidden.remove();
	if (newVal!=inp.dataset.original||cell.classList.contains('reverted')){
	inp.dataset.changed='1';cell.classList.remove('reverted')
	}
cell.querySelector('.community-value').textContent=newVal;
cell.querySelector('.community-value').style.display='';
var auto=cell.dataset.auto;
var revert=cell.querySelector('.revert-btn');
if(newVal!=auto){if(!revert){revert=document.createElement('button');revert.type='button';revert.className='revert-btn';revert.textContent='↺';revert.title='{{tr "communities.revert"}}';cell.insertBefore(revert,cell.querySelector('.edit-actions'));setupRevert(revert)}}else{if(revert)revert.style.display='none'}
revert&&(revert.style.display=newVal!=auto?'':'none');
cell.querySelector('.edit-actions').style.display='none';
updateUI();
activeEdit = null;
})
});

// ✗ Cancel
document.querySelectorAll('.cancel-btn').forEach(function(b){
b.addEventListener('click',function(){
var cell=this.closest('.community-cell');
cell.querySelector('.community-value').style.display='';
var revert=cell.querySelector('.revert-btn');
var auto=cell.dataset.auto;
var val=cell.querySelector('.community-value').textContent;
revert&&(revert.style.display=val!=auto?'':'none');
cell.querySelector('.edit-actions').style.display='none';
updateUI();
activeEdit = null;
})
});

// ↺ Revert
function setupRevert(btn){
btn.addEventListener('click',function(){
  var cell=this.closest('.community-cell');
  var inp = cell.querySelector('.community-input');
  var editActions = cell.querySelector('.edit-actions');
  
  // If in edit mode with un-applied changes, just cancel
  if (editActions && editActions.style.display !== 'none' && inp && inp.dataset.changed === '0') {
    cell.querySelector('.cancel-btn').click();
    return;
  }
  
  // If user applied a change THIS session (✓ was clicked, not saved to server),
  // revert means UNDO the session edit — go back to original, no counting
  if (inp && inp.dataset.changed === '1') {
    var original = inp.dataset.original || cell.dataset.value; // fallback to server value
    cell.querySelector('.community-value').textContent = original;
    inp.value = original;
    inp.dataset.changed = '0';
    cell.classList.remove('reverted');
    // Remove any hidden input from a previous revert
    var oldHidden = cell.querySelector('.revert-hidden');
    if (oldHidden) oldHidden.remove();
    // Exit edit mode if active
    editActions.style.display = 'none';
    cell.querySelector('.community-value').style.display = '';
    var revert = cell.querySelector('.revert-btn');
    var auto = cell.dataset.auto;
    if (revert) revert.style.display = original != auto ? '' : 'none';
    updateUI();
    return;
  }
  
  // Otherwise: revert to auto (the cell has a saved manual value, not from this session)
  var auto=cell.dataset.auto;
  cell.querySelector('.community-value').textContent=auto;
  cell.classList.add('reverted');
  var hidden=document.createElement('input');
  hidden.type='hidden';hidden.name=cell.dataset.name;hidden.value=auto;hidden.className='revert-hidden';
  var oldHidden=cell.querySelector('.revert-hidden');
  oldHidden&&oldHidden.remove();
  cell.appendChild(hidden);
  // Update edit input too
  if (inp) inp.value = auto;
  if (inp) inp.dataset.changed = '0';
  this.style.display='none';
  cell.querySelector('.edit-actions').style.display='none';
  cell.querySelector('.community-value').style.display='';
  updateUI()
})}
document.querySelectorAll('.revert-btn').forEach(setupRevert);

// Enter submits apply
document.querySelectorAll('.community-input').forEach(function(inp){
inp.addEventListener('keydown',function(e){if(e.key==='Enter'){e.preventDefault();this.parentElement.querySelector('.apply-btn').click()}})
});

// Form submit confirm
document.getElementById('communities-form').addEventListener('submit',function(e){
var c=countChanged()+countReverted();
if(c>0&&!confirm('{{tr "communities.confirm"}}'.replace('N',c))){e.preventDefault()}
});

updateUI()
})();
</script>
{{end}}`

const communitiesTemplate = communitiesBody

const adminShellTemplate = `<!DOCTYPE html>
<html lang="{{.Lang}}">
<head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>{{.Title}} - wdbgp</title>
<style>` + sharedCSS + `
body{display:flex;height:100vh;overflow:hidden}
.sidebar{width:220px;background:var(--sidebar-bg);color:var(--sidebar-text);padding:1.25rem 0;flex-shrink:0;display:flex;flex-direction:column;border-right:1px solid var(--border)}
.sidebar h2{padding:0 1.25rem .75rem;font-size:1.1em;border-bottom:1px solid var(--border);margin-bottom:.5rem;font-weight:700}
.sidebar a{color:var(--sidebar-text);text-decoration:none;padding:.6rem 1.25rem;display:block;font-size:.95em;transition:background .15s}
.sidebar a:hover{background:var(--sidebar-hover)}
.sidebar a.active{background:var(--sidebar-active-bg);color:var(--sidebar-active-text)}
.sidebar-spacer{flex:1}
.sidebar-subtle{font-size:.85em;opacity:.7}
.main{flex:1;padding:1.5rem;overflow-y:auto}
.topbar{display:flex;justify-content:flex-end;align-items:center;gap:.5rem;margin-bottom:1.5rem}
.topbar button,.topbar a{background:none;border:1px solid var(--border);color:var(--text);padding:6px 12px;border-radius:6px;cursor:pointer;font-size:.85em;text-decoration:none}
.topbar button:hover{background:var(--accent);color:#fff}
.topbar .theme-btn{font-size:1.1em;padding:2px 8px}
</style>
<script src="https://unpkg.com/htmx.org@2.0.4" defer></script>
<script src="https://unpkg.com/alpinejs@3.14.9" defer></script>
</head>
<body>
<nav class=sidebar>
<h2>wdbgp</h2>
<a href=/admin/dashboard hx-get=/admin/dashboard hx-target=#main hx-push-url=true class=active>{{tr "nav.dashboard"}}</a>
<a href=/admin/users hx-get=/admin/users hx-target=#main hx-push-url=true>{{tr "nav.users"}}</a>
<a href=/admin/feeds hx-get=/admin/feeds hx-target=#main hx-push-url=true>{{tr "nav.feeds"}}</a>
<a href=/admin/communities hx-get=/admin/communities hx-target=#main hx-push-url=true>{{tr "nav.communities"}}</a>
<a href=/admin/adapters hx-get=/admin/adapters hx-target=#main hx-push-url=true>{{tr "nav.adapters"}}</a>
<a href=/admin/settings hx-get=/admin/settings hx-target=#main hx-push-url=true>{{tr "nav.settings"}}</a>
<div class=sidebar-spacer></div>
<a href=/ hx-get=/ hx-target=#main hx-push-url=true class=sidebar-subtle>{{tr "nav.user_page"}}</a>
</nav>
<div class=main>
<header class=topbar id=topbar>
<button onclick="cycleTheme()" title="Theme" class=theme-btn>🌙</button>
<a href="#" data-lang="en" class="lang-switch">EN</a>
<a href="#" data-lang="ru" class="lang-switch">RU</a>
</header>
<main id=main hx-history-elt>
{{.ContentHTML}}
</main>
</div>
<script>
function cycleTheme(){var t=document.documentElement;var c=t.getAttribute('data-theme');if(!c){c=window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light'}var n=c==='dark'?'light':'dark';t.setAttribute('data-theme',n);localStorage.setItem('wdbgp-theme',n);updateThemeIcon()}
function updateThemeIcon(){var t=document.documentElement.getAttribute('data-theme')||(window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light');var m=document.querySelector('#topbar button');var i={'light':'☀️','dark':'🌙'};if(m)m.textContent=i[t]||'🌙'}
(function(){var s=localStorage.getItem('wdbgp-theme');if(!s){s=window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light'}if(s==='auto')document.documentElement.removeAttribute('data-theme');else document.documentElement.setAttribute('data-theme',s);updateThemeIcon()})();
// Active nav link
document.querySelectorAll('.sidebar a').forEach(function(a){a.addEventListener('click',function(){document.querySelectorAll('.sidebar a').forEach(function(x){x.classList.remove('active')});this.classList.add('active')})});
// Set active nav based on current location (for direct page loads)
function setActiveNav(){var p=window.location.pathname;document.querySelectorAll('.sidebar a').forEach(function(a){var href=a.getAttribute('href');if(href===p||(p.startsWith('/admin/user/')&&href==='/admin/users')||(p.startsWith('/admin/adapter/')&&href==='/admin/adapters')){a.classList.add('active')}else{a.classList.remove('active')}})}
setActiveNav();
// htmx after-swap: update active nav
document.body.addEventListener('htmx:afterSettle',function(evt){if(!evt.detail || !evt.detail.requestConfig)return;var path=evt.detail.requestConfig.path;document.querySelectorAll('.sidebar a').forEach(function(a){var href=a.getAttribute('href');if(href===path){a.classList.add('active')}else{a.classList.remove('active')}})});
function switchLang(lang) {
  var search = window.location.search.replace(/[?&]lang=[^&]*/g, '');
  search = search ? search + '&' : '?';
  window.location.href = window.location.pathname + search + 'lang=' + lang;
}
document.querySelectorAll('.lang-switch').forEach(function(a) {
  a.addEventListener('click', function(e) { e.preventDefault(); switchLang(this.dataset.lang); });
});
</script>
</body></html>`

const dashboardTemplate = `{{with .Data}}
<h1>{{tr "nav.dashboard"}}</h1>
<div class=stats-grid>
<div class=stat-card><div class=value>{{.TotalPrefixes}}</div><div class=label>{{tr "stats.prefixes"}}</div></div>
<div class=stat-card><div class=value>{{.TotalUsers}}</div><div class=label>{{tr "stats.users"}}</div></div>
<div class=stat-card><div class=value><span class="status-dot {{if gt .ConnectedPeers 0}}up{{else}}down{{end}}"></span>{{.ConnectedPeers}}/{{.TotalPeers}}</div><div class=label>{{tr "stats.bgp_peers"}}</div></div>
<div class=stat-card><div class=value>{{.EnabledFeeds}}/{{.TotalFeeds}}</div><div class=label>{{tr "stats.feeds"}}</div></div>
<div class=stat-card><div class=value>{{.Categories}}</div><div class=label>{{tr "stats.categories"}}</div></div>
<div class=stat-card><div class=value>{{.Services}}</div><div class=label>{{tr "stats.services"}}</div></div>
</div>
{{end}}`

const usersListTemplate = `{{with .Data}}
<h1>{{tr "nav.users"}}</h1>
<div class=card>
<table>
<tr><th>{{tr "feeds.name"}}</th><th>{{tr "users.networks"}}</th><th>{{tr "catalog.mode"}}</th><th>BGP</th><th>{{tr "users.peer_asn"}}</th><th>{{tr "users.web_auth"}}</th><th></th></tr>
{{range .Users}}
<tr>
<td><a href="/admin/user/{{.User.ID}}">{{.User.Name}}</a></td>
<td><code>{{.Networks}}</code></td>
<td>{{.User.CatalogModeName}}</td>
<td><span class="status-dot {{if eq .PeerState "ESTABLISHED"}}up{{else}}down{{end}}" title="{{if eq .PeerState "ESTABLISHED"}}{{tr "bgp.established"}}{{else if eq .PeerState "UNKNOWN"}}{{tr "bgp.unknown"}}{{else}}{{.PeerState}}{{end}}"></span> <code>{{.User.PeerIP}}</code></td>
<td>{{.User.PeerASN}}</td>
<td>{{.User.WebAuth}}</td>
<td>{{if .User.Enabled}}<span class=ok>enabled</span>{{else}}<span class=error>disabled</span>{{end}}</td>
</tr>{{end}}
</table>
</div>
<p><a href="/admin/user/0" class=button>{{tr "common.add"}}</a></p>
{{end}}`

const feedsListTemplate = `{{with .Data}}
<h1>{{tr "nav.feeds"}}</h1>
<div class=card>
<table>
<tr><th>{{tr "feeds.name"}}</th><th>{{tr "catalog.mode"}}</th><th>{{tr "feeds.status"}}</th><th>{{tr "feeds.last_sync"}}</th><th></th></tr>
{{range .Feeds}}
<tr>
<td>{{.Feed.Name}}</td>
<td>{{.ModeName}}</td>
<td>{{if .Feed.Enabled}}<span class=ok>enabled</span>{{else}}<span class=error>disabled</span>{{end}}</td>
<td>{{if .LastSync}}{{.LastSync}}{{else}}—{{end}}</td>
<td><a href="/admin/feed/{{.Feed.ID}}" class=button>{{tr "common.edit"}}</a></td>
</tr>{{end}}
</table>
</div>
<p><a href="/admin/feed" class="button primary">{{tr "feeds.add"}}</a></p>
{{end}}`

const feedEditTemplate = `{{with .Data}}
<a href="/admin/feeds" class=back-link>← {{tr "nav.feeds"}}</a>
<h1>{{if .IsNew}}{{tr "feeds.add"}}{{else}}{{tr "feeds.edit"}}{{end}}</h1>
<div class=card>
<form method=post action="{{if .IsNew}}/admin/feed{{else}}/admin/feed/{{.Feed.ID}}{{end}}">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<label>{{tr "feeds.name"}} <input name=name value="{{.Feed.Name}}" required></label>
<label>{{tr "feeds.url"}} <input name=url value="{{.Feed.URL}}" required></label>
<div class=form-grid>
<label>{{tr "catalog.mode"}}
<select name=catalog_mode_id>
{{range .Modes}}<option value="{{.ID}}" {{if eq .ID $.Data.Feed.ModeID}}selected{{end}}>{{.Name}}</option>{{end}}
</select></label>
<label>{{tr "feeds.adapter"}}
<select name=adapter_id>
{{range .Adapters}}<option value="{{.ID}}" {{if eq .ID $.Data.Feed.AdapterID}}selected{{end}}>{{.Name}}</option>{{end}}
</select></label>
</div>
<label>{{tr "feeds.sync_interval"}} <input type=number name=sync_interval value="{{.Feed.SyncInterval}}" placeholder="{{tr "feeds.default_interval"}}"></label>
<div class=checkbox-row><label><input type=checkbox name=enabled {{if .Feed.Enabled}}checked{{end}}> {{tr "feeds.enabled"}}</label></div>
<button type=submit class=primary>{{tr "common.save"}}</button>
</form>
{{if not .IsNew}}<form method=post action="/admin/feed/{{.Feed.ID}}/delete" style="margin-top:1rem" onsubmit="return confirm('{{tr "feeds.delete_confirm"}}')">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button type=submit class=danger>{{tr "common.delete"}}</button></form>{{end}}
</div>
{{end}}`

const adaptersListTemplate = `{{with .Data}}
<h1>{{tr "nav.adapters"}}</h1>
<div class=card>
<table>
<tr><th>{{tr "adapters.key"}}</th><th>{{tr "feeds.name"}}</th><th>{{tr "adapters.revision"}}</th><th></th></tr>
{{range .Adapters}}
<tr>
<td><code>{{.Key}}</code></td>
<td>{{.Name}}</td>
<td>{{.Revision}}</td>
<td><a href="/admin/adapter/{{.ID}}" class=button>Edit</a></td>
</tr>{{end}}
</table>
</div>
{{end}}`
