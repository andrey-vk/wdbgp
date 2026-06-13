package web

const pageStart = `<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>{{.Title}}</title><style>
*{box-sizing:border-box} body{font:16px/1.45 system-ui,-apple-system,Segoe UI,sans-serif;max-width:1100px;margin:0 auto;padding:1.5rem 1rem 3rem;color:#18212b;background:#f6f8fb}
header{display:flex;gap:1rem;justify-content:space-between;align-items:center;margin:0 0 1rem} a{color:#2457a6} h1,h2,h3{margin:.4rem 0 1rem} code{font-size:.9em}
form{margin:1rem 0} label{display:block;margin:.55rem 0;font-weight:600}
input:not([type]),input[type=text],input[type=password],input[type=number],input[type=url],textarea{width:100%;max-width:42rem;padding:.6rem .7rem;border:1px solid #c8d2df;border-radius:.55rem;background:white}
textarea{min-height:10rem;font:14px/1.4 ui-monospace,monospace;resize:vertical}
button,.button{display:inline-block;padding:.65rem 1rem;border:0;border-radius:.6rem;background:#2457a6;color:white;font-weight:700;text-decoration:none;cursor:pointer}
button.danger{background:#b42318} button.secondary{background:#667} table{border-collapse:separate;border-spacing:0;width:100%;background:white;border:1px solid #dfe5ee;border-radius:.8rem;overflow:hidden}
td,th{border-bottom:1px solid #e8edf4;padding:.65rem;text-align:left;vertical-align:top} tr:last-child td{border-bottom:0}
.card{background:white;border:1px solid #dfe5ee;border-radius:1rem;padding:1rem;margin:1rem 0;box-shadow:0 8px 24px #16233a0d}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(16rem,1fr));gap:1rem}.row-actions{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}
.feed-actions{flex-wrap:nowrap}.feed-actions form{margin:0}.feed-actions button{padding:.45rem .65rem;border-radius:.5rem;white-space:nowrap}
.muted{color:#667}.error{color:#a00}.ok{color:#075}.pill{display:inline-block;padding:.15rem .5rem;border-radius:999px;background:#edf2f8;color:#445}
.error-output{white-space:pre-wrap;overflow:auto;padding:1rem;border-radius:.6rem;background:#fff1f0;color:#8a1c13;border:1px solid #f2b8b5}
.selection-form{padding-bottom:5.5rem}.save-bar{position:sticky;top:.5rem;z-index:2;display:flex;gap:1rem;align-items:center;justify-content:space-between;background:#10294f;color:white;border-radius:1rem;padding:.8rem 1rem;box-shadow:0 12px 28px #10294f40}
.save-bar .muted{color:#d7e4f5}.save-bar button{background:#33a36f}.save-bar button:disabled{background:#71829b;cursor:not-allowed}
.catalog-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(18rem,1fr));gap:1rem;margin-top:1rem}fieldset.category-card{border:1px solid #dfe5ee;border-radius:1rem;background:white;margin:0;padding:0;overflow:hidden}
.category-card legend{float:left;width:100%;padding:.85rem 1rem;background:#eef4fb;border-bottom:1px solid #dfe5ee}.category-card legend+*{clear:both}
.category-title{display:flex;gap:.55rem;align-items:center;margin:0;font-size:1.05rem}.service-list{padding:.75rem 1rem 1rem}.service-list label{font-weight:500;margin:.4rem 0}
.empty{background:white;border:1px dashed #b9c5d4;border-radius:1rem;padding:1rem}.status{font:12px ui-monospace,monospace;padding:.2rem .45rem;border-radius:999px;background:#edf2f8}
.language-switcher{display:flex;justify-content:flex-end;gap:.5rem;margin-bottom:.75rem}.language-switcher a[aria-current=page]{font-weight:700;text-decoration:none;color:#18212b}
dialog{width:min(52rem,calc(100% - 2rem));max-height:calc(100% - 2rem);border:0;border-radius:1rem;padding:0;box-shadow:0 24px 80px #10294f66}dialog::backdrop{background:#10294f99}
.dialog-body{padding:1.25rem}.dialog-header{display:flex;align-items:center;justify-content:space-between;gap:1rem}.dialog-header button{background:#667;padding:.45rem .7rem}
.debug-list{margin:.5rem 0 1rem;padding-left:1.25rem}.debug-list li{margin:.3rem 0}
.community-tag{font-size:.8em;color:#667;font-family:ui-monospace,monospace;margin-left:.3em}
.community-value{color:#06c;cursor:pointer;text-decoration:underline;font-family:ui-monospace,monospace}.community-value:hover{color:#049}
.community-cell{white-space:nowrap;display:inline-flex;align-items:center;gap:2px}
.community-input{width:7ch;padding:1px 3px;border:1px solid #aaa;border-radius:2px}
.edit-actions{display:none;align-items:center;gap:2px}
.apply-btn{color:#0a0;background:0 0;border:1px solid #0a0;cursor:pointer;padding:1px 5px;margin-left:2px;border-radius:2px}
.cancel-btn{color:#c00;background:0 0;border:1px solid #c00;cursor:pointer;padding:1px 5px;margin-left:1px;border-radius:2px}
.revert-btn{color:#c90;background:0 0;border:none;cursor:pointer;font-size:1.1em;margin-left:4px;padding:0 2px}
.communities-table th:last-child,.communities-table td:last-child{min-width:180px}
</style></head><body><nav class=language-switcher aria-label="{{tr "language.label"}}">
<a href="{{.EnglishURL}}" title="{{tr "language.english"}}" aria-current="{{if eq .Lang "en"}}page{{else}}false{{end}}">EN</a>
<a href="{{.RussianURL}}" title="{{tr "language.russian"}}" aria-current="{{if eq .Lang "ru"}}page{{else}}false{{end}}">RU</a>
</nav>`

const pageEnd = `</body></html>`

const accessDeniedTemplate = `<h1>{{tr "access_denied.heading"}}</h1><p>IP: <code>{{.Data}}</code></p>`

const loginTemplate = `<h1>{{tr "admin.heading"}}</h1>{{if .Data}}<p class=error>{{.Data}}</p>{{end}}
<form method=post><label>{{tr "login.password"}} <input type=password name=password autofocus required></label>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button>{{tr "login.submit"}}</button></form>`

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
 <label>{{tr "adapters.source"}} <textarea name=source rows=30 required>{{.Adapter.Source}}</textarea></label>
 <label>{{tr "adapters.test_feed"}} <select name=feed_id>
 <option value="">{{tr "adapters.select_feed"}}</option>
 {{range .Feeds}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
 </select></label>
 <div class=row-actions><button>{{tr "common.save"}}</button>
 <button type=submit formaction="/admin/adapter/{{.Adapter.ID}}/test">{{tr "adapters.test"}}</button></div>
 </form>
{{if .Adapter.BuiltIn}}<form method=post action="/admin/adapter/{{.Adapter.ID}}/reset" onsubmit="return confirm('{{tr "adapters.reset_confirm"}}');">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button class=danger>{{tr "adapters.reset"}}</button></form>{{end}}
</section>{{end}}`

const selectionBody = `{{$selection := .}}
<header><h1>{{.User.Name}}</h1>{{if not .Admin}}<a href="/admin">{{tr "admin.link"}}</a>{{end}}</header>
<section class=card><h2>{{tr "selection.heading"}}</h2>
<p class=muted>{{tr "selection.category_hint"}}</p>
<label>{{tr "catalog.mode"}}
<select id=catalog-mode-select {{if not .CanChangeMode}}disabled{{end}}>
{{range .Modes}}<option value="{{.ID}}" {{if eq .ID $selection.User.CatalogModeID}}selected{{end}}>{{.Name}}{{if not .Enabled}} ({{tr "catalog.disabled"}}){{end}}</option>{{end}}
</select></label>
{{if not .CanChangeMode}}<p class=muted>{{tr "catalog.managed"}}</p>{{end}}
{{if eq .Saved "1"}}<p class=ok>{{tr "selection.saved"}}</p>{{else if eq .Saved "0"}}<p class=error>{{tr "selection.save_failed"}}</p>{{end}}
{{if not .Editable}}<p class=muted>{{tr "selection.locked"}}</p>{{end}}
<form class=selection-form method=post action="{{if .Admin}}/admin/user/{{.User.ID}}{{else}}/selection{{end}}">
{{if .Admin}}<input type=hidden name=action value=selection>{{end}}
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=catalog_mode_id value="{{.User.CatalogModeID}}">
<div class=save-bar><div><strong>{{.User.Name}}</strong><br><span class=muted>{{tr "selection.selected"}}
<span id=selected-category-count>{{.SelectedCategoryCount}}</span>
<span id=selected-category-label data-one="{{tr "selection.category_one"}}" data-few="{{tr "selection.category_few"}}" data-many="{{tr "selection.category_many"}}">{{plural .SelectedCategoryCount "selection.category_one" "selection.category_few" "selection.category_many"}}</span>
(<span id=selected-covered-service-count>{{.SelectedCoveredServices}}</span>
<span id=selected-covered-service-label data-one="{{tr "selection.service_one"}}" data-few="{{tr "selection.service_few"}}" data-many="{{tr "selection.service_many"}}">{{plural .SelectedCoveredServices "selection.service_one" "selection.service_few" "selection.service_many"}}</span> {{tr "selection.in_categories"}}).
<span id=selected-service-count>{{.SelectedServiceCount}}</span>
<span id=selected-service-label data-one="{{tr "selection.standalone_one"}}" data-few="{{tr "selection.standalone_few"}}" data-many="{{tr "selection.standalone_many"}}">{{plural .SelectedServiceCount "selection.standalone_one" "selection.standalone_few" "selection.standalone_many"}}</span>.
{{tr "selection.apply_hint"}}</span></div>
<button {{if and (not .Editable) (not .CanChangeMode)}}disabled{{end}}>{{tr "selection.save"}}</button></div>
{{if .Categories}}<div class=catalog-grid>{{range .Categories}}{{$cat := .Name}}
<fieldset class=category-card><legend><label class=category-title><input type=checkbox name=category value="{{.Name}}" {{if .Selected}}checked{{end}} {{if not $selection.Editable}}disabled{{end}}> <strong>{{.Name}}</strong> <span class=pill>{{tr "selection.whole_category"}}</span>{{if index $selection.Communities .Name}} <span class=community-tag>{{index $selection.Communities .Name}}</span>{{end}}</label></legend>
<div class=service-list>{{range .Services}}<label><input type=checkbox name=service value="{{.Value}}" {{if .Selected}}checked{{end}} {{if or (not $selection.Editable) .Disabled}}disabled{{end}}> {{.Name}}{{if index $selection.Communities (printf "%s|%s" $cat .Name)}} <span class=community-tag>{{index $selection.Communities (printf "%s|%s" $cat .Name)}}</span>{{end}}</label>{{end}}</div>
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
<div class=grid><label>{{tr "filters.allow"}} <textarea name=filter_allow placeholder="{{tr "filters.allow_placeholder"}}">{{.AllowText}}</textarea></label>
<label>{{tr "filters.deny"}} <textarea name=filter_deny placeholder="1.1.1.1/32">{{.DenyText}}</textarea></label></div>
<button>{{tr "filters.save"}}</button></form>
{{else}}<p class=muted>{{if eq .Mode "override"}}{{tr "filters.managed_override"}}{{else if eq .Mode "extend"}}{{tr "filters.managed_extend"}}{{else}}{{tr "filters.managed_global"}}{{end}}</p>{{end}}
</section>{{end}}`

const selectionTemplate = `{{with .Data}}` + selectionBody + `{{end}}`

const adminTemplate = `{{with .Data}}
<header><h1>{{tr "admin.heading"}}</h1>
<div style="display: flex; gap: 1rem; align-items: center;">
<a href="/">{{tr "user_interface.link"}}</a>
<a href="/admin/communities">{{tr "communities.link"}}</a>
<form method=post action="/admin/logout" style="margin: 0;">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button type=submit class="button danger" style="padding: 0.4rem 0.8rem; font-size: 0.9rem;">{{tr "admin.logout"}}</button>
</form>
</div>
</header>
<section class=card><h2>{{tr "catalog.modes"}}</h2>
<p class=muted>{{tr "catalog.modes_hint"}}</p>
<table><tr><th>{{tr "feeds.name"}}</th><th>{{tr "catalog.key"}}</th><th>{{tr "feeds.enabled"}}</th><th>{{tr "feeds.actions"}}</th></tr>
{{range .Modes}}<tr>
<td><input form="mode-{{.ID}}" name=name value="{{.Name}}" required></td>
<td><code>{{.Key}}</code></td>
<td><input form="mode-{{.ID}}" type=checkbox name=enabled {{if .Enabled}}checked{{end}}></td>
<td><form id="mode-{{.ID}}" method=post action="/admin/mode/{{.ID}}"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button>{{tr "common.save"}}</button></form></td>
</tr>{{end}}</table></section>
<section class=card><h2>{{tr "feeds.heading"}}</h2><table><tr><th>{{tr "feeds.name"}}</th><th>URL</th><th>{{tr "adapters.adapter"}}</th><th>{{tr "catalog.mode"}}</th><th>{{tr "feeds.enabled"}}</th><th>{{tr "feeds.last_download"}}</th><th>{{tr "feeds.error"}}</th><th>{{tr "feeds.actions"}}</th></tr>
{{range .Feeds}}{{$feed := .}}<tr>
<td><input form="feed-{{.ID}}" name=name value="{{.Name}}" required></td>
<td><input form="feed-{{.ID}}" type=url name=url value="{{.URL}}" required></td>
<td><select form="feed-{{$feed.ID}}" name=adapter_id>{{range $.Data.Adapters}}<option value="{{.ID}}" {{if eq .ID $feed.AdapterID}}selected{{end}}>{{.Name}}</option>{{end}}</select></td>
<td><select form="feed-{{$feed.ID}}" name=catalog_mode_id>{{range $.Data.Modes}}<option value="{{.ID}}" {{if eq .ID $feed.ModeID}}selected{{end}}>{{.Name}}</option>{{end}}</select></td>
<td><input form="feed-{{.ID}}" type=checkbox name=enabled {{if .Enabled}}checked{{end}} aria-label="{{tr "feeds.enabled"}}"></td>
<td>{{.LastSuccess}}</td><td class=error>{{.LastError}}</td><td>
<div class="row-actions feed-actions">
<form id="feed-{{.ID}}" method=post action="/admin/feed/{{.ID}}"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button>{{tr "common.save"}}</button></form>
<form method=post action="/admin/feed/{{.ID}}/delete" onsubmit="return confirm('{{tr "feeds.delete_confirm"}}');"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button class=danger>{{tr "common.delete"}}</button></form>
</div></td></tr>{{end}}</table>
<form method=post action=/admin/feed><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><h3>{{tr "feeds.add"}}</h3><label>{{tr "feeds.name"}} <input name=name required></label><label>URL <input type=url name=url required></label>
<label>{{tr "adapters.adapter"}} <select name=adapter_id>{{range .Adapters}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
<label>{{tr "catalog.mode"}} <select name=catalog_mode_id>{{range .Modes}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
<label><input type=checkbox name=enabled checked> {{tr "feeds.enabled"}}</label><button>{{tr "common.add"}}</button></form>
<form method=post action=/admin/sync><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button>{{tr "feeds.download_now"}}</button></form></section>
<section class=card><h2>{{tr "adapters.heading"}}</h2>
<p class=muted>{{tr "adapters.hint"}}</p>
<table><tr><th>{{tr "feeds.name"}}</th><th>{{tr "catalog.key"}}</th><th>{{tr "adapters.revision"}}</th><th>{{tr "feeds.actions"}}</th></tr>
{{range .Adapters}}<tr><td>{{.Name}}</td><td><code>{{.Key}}</code></td><td>{{.Revision}}</td>
<td><a class=button href="/admin/adapter/{{.ID}}">{{tr "adapters.edit"}}</a></td></tr>{{end}}</table>
<details><summary><strong>{{tr "adapters.add"}}</strong></summary>
<form method=post action=/admin/adapter>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<label>{{tr "catalog.key"}} <input name=key pattern="[a-z0-9._-]+" required></label>
<label>{{tr "feeds.name"}} <input name=name required></label>
<label>{{tr "adapters.allowed_hosts"}} <input name=allowed_hosts></label>
<label>{{tr "adapters.source"}} <textarea name=source rows=20 required>function sync(feed, api) {
    return [];
}
</textarea></label>
<button>{{tr "common.add"}}</button></form></details></section>
<section class=card><h2>{{tr "global_filters.heading"}}</h2>
<p class=muted>{{tr "global_filters.explanation"}}</p>
<form method=post action=/admin/filters><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><div class=grid>
<label>{{tr "filters.allow"}} <textarea name=filter_allow placeholder="{{tr "filters.allow_placeholder"}}">{{.GlobalFilters.AllowText}}</textarea></label>
<label>{{tr "filters.deny"}} <textarea name=filter_deny>{{.GlobalFilters.DenyText}}</textarea></label></div>
<button>{{tr "global_filters.save"}}</button></form></section>
<section class=card><h2>{{tr "users.heading"}}</h2><table><tr><th>{{tr "feeds.name"}}</th><th>{{tr "users.cidr"}}</th><th>{{tr "catalog.mode"}}</th><th>{{tr "users.peer"}}</th><th>{{tr "users.asn"}}</th><th>{{tr "users.status"}}</th></tr>
{{range .Users}}<tr><td><a href="/admin/user/{{.ID}}">{{.Name}}</a></td><td><code>{{join .Networks ", "}}</code></td><td>{{.CatalogModeName}}</td><td><code>{{.PeerIP}}</code></td><td>{{.PeerASN}}</td><td><span class=status>{{state $.Data.PeerStates .PeerIP}}</span></td></tr>{{end}}</table></section>
<section class=card><form method=post action=/admin/user><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><h3>{{tr "users.add"}}</h3><div class=grid>
<label>{{tr "feeds.name"}} <input name=name required></label><label>{{tr "users.networks"}} <input name=networks required></label>
<label>{{tr "users.peer_ip"}} <input name=peer_ip required></label><label>{{tr "users.peer_asn"}} <input type=number min=1 name=peer_asn required></label>
<label>{{tr "users.next_hop"}} <input name=next_hop></label><label>{{tr "users.bgp_password"}} <input type=password name=bgp_password></label>
<label>{{tr "catalog.mode"}} <select name=catalog_mode_id>{{range .Modes}}{{if .Enabled}}<option value="{{.ID}}">{{.Name}}</option>{{end}}{{end}}</select></label></div>
<label><input type=checkbox name=filter_editable> {{tr "users.allow_filter_editing"}}</label>
<label><input type=checkbox name=catalog_mode_editable> {{tr "users.allow_mode_editing"}}</label>
<button>{{tr "common.add"}}</button></form></section>
<section class=card><h2>{{tr "debug.heading"}}</h2><p class=muted>{{tr "debug.description"}}</p>
<form id=cidr-debug-form><label>{{tr "catalog.mode"}} <select name=mode required>{{range .Modes}}{{if .Enabled}}<option value="{{.ID}}">{{.Name}}</option>{{end}}{{end}}</select></label>
<label>{{tr "debug.input"}} <input name=cidr placeholder="8.8.8.8 or 8.8.8.0/24" required></label><button>{{tr "debug.submit"}}</button></form></section>
<dialog id=cidr-debug-dialog><div class=dialog-body>
<div class=dialog-header><h2>{{tr "debug.results"}}</h2><button type=button id=cidr-debug-close aria-label="{{tr "debug.close"}}">×</button></div>
<p><code id=cidr-debug-query></code></p><p class=error id=cidr-debug-error hidden></p>
<div id=cidr-debug-content>
<h3>{{tr "debug.full_services"}}</h3><ul class=debug-list id=cidr-debug-full></ul>
<h3>{{tr "debug.partial_services"}}</h3><ul class=debug-list id=cidr-debug-partial></ul>
<h3>{{tr "debug.combined"}}</h3><p id=cidr-debug-combined></p>
<h3>{{tr "debug.users"}}</h3><ul class=debug-list id=cidr-debug-users></ul>
<p id=cidr-debug-empty hidden>{{tr "debug.no_matches"}}</p></div>
</div></dialog>
<script>
(function() {
  var form = document.getElementById('cidr-debug-form');
  var dialog = document.getElementById('cidr-debug-dialog');
  var error = document.getElementById('cidr-debug-error');
  var content = document.getElementById('cidr-debug-content');
  var empty = document.getElementById('cidr-debug-empty');
  var noItems = {{printf "%q" (tr "debug.no_items")}};
  var coverageLabel = {{printf "%q" (tr "debug.coverage")}};
  var beforeFiltersLabel = {{printf "%q" (tr "debug.before_filters")}};
  var afterFiltersLabel = {{printf "%q" (tr "debug.after_filters")}};
  var requestFailed = {{printf "%q" (tr "debug.request_failed")}};
  function percentage(value) {
    return new Intl.NumberFormat(document.documentElement.lang, {maximumFractionDigits: 2}).format(value) + '%';
  }
  function fillList(id, items, userList) {
    var list = document.getElementById(id);
    list.replaceChildren();
    if (!items.length) {
      var none = document.createElement('li');
      none.textContent = noItems;
      list.appendChild(none);
      return;
    }
    items.forEach(function(item) {
      var row = document.createElement('li');
      if (userList) {
        row.textContent = item.name + ' — ' + beforeFiltersLabel + ': ' +
          percentage(item.before_percentage) + '; ' + afterFiltersLabel + ': ' +
          percentage(item.after_percentage);
        if (item.matches && item.matches.length) row.textContent += ': ' + item.matches.join(', ');
      } else {
        row.textContent = item.category + ' / ' + item.service + ' — ' +
          percentage(item.percentage) + ' ' + coverageLabel;
      }
      list.appendChild(row);
    });
  }
  form.addEventListener('submit', async function(event) {
    event.preventDefault();
    error.hidden = true;
    content.hidden = false;
    var cidr = new FormData(form).get('cidr');
    var mode = new FormData(form).get('mode');
    try {
      var response = await fetch('/admin/debug/cidr?mode=' + encodeURIComponent(mode) + '&cidr=' + encodeURIComponent(cidr));
      if (!response.ok) throw new Error((await response.text()).trim() || requestFailed);
      var result = await response.json();
      document.getElementById('cidr-debug-query').textContent = result.query;
      fillList('cidr-debug-full', result.full_services || [], false);
      fillList('cidr-debug-partial', result.partial_services || [], false);
      fillList('cidr-debug-users', result.users || [], true);
      var combined = document.getElementById('cidr-debug-combined');
      var combinedNames = (result.combined_services || []).map(function(item) {
        return item.category + ' / ' + item.service;
      });
      combined.textContent = combinedNames.length
        ? percentage(result.combined_percentage) + ' ' + coverageLabel + ': ' + combinedNames.join(', ')
        : noItems;
      empty.hidden = Boolean((result.full_services || []).length || (result.partial_services || []).length || (result.users || []).length);
    } catch (failure) {
      document.getElementById('cidr-debug-query').textContent = cidr;
      content.hidden = true;
      error.textContent = failure.message || requestFailed;
      error.hidden = false;
    }
    dialog.showModal();
  });
  document.getElementById('cidr-debug-close').addEventListener('click', function() { dialog.close(); });
  dialog.addEventListener('click', function(event) { if (event.target === dialog) dialog.close(); });
})();
</script>{{end}}`

const userEditTemplate = `{{define "selection"}}` + selectionBody + `{{end}}{{with .Data}}
<header><h1>{{printf (tr "title.user") .User.Name}}</h1><a href=/admin>{{tr "admin.link"}}</a></header>
<section class=card><h2>{{tr "user.settings"}}</h2><form method=post action="/admin/user/{{.User.ID}}">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=action value=settings><div class=grid>
<label>{{tr "feeds.name"}} <input name=name value="{{.User.Name}}" required></label><label>{{tr "users.networks"}} <input name=networks value="{{join .User.Networks ", "}}" required></label>
<label>{{tr "users.peer_ip"}} <input name=peer_ip value="{{.User.PeerIP}}" required></label><label>{{tr "users.peer_asn"}} <input type=number min=1 name=peer_asn value="{{.User.PeerASN}}" required></label>
<label>{{tr "users.next_hop"}} <input name=next_hop value="{{.User.NextHop}}"></label><label>{{tr "users.bgp_password"}} <input type=password name=bgp_password placeholder="{{if .User.BGPPassword}}{{tr "user.password_set"}}{{else}}{{tr "user.password_not_set"}}{{end}}"></label></div>
<label>{{tr "catalog.mode"}} <select name=catalog_mode_id>{{range .Selection.Modes}}<option value="{{.ID}}" {{if eq .ID $.Data.User.CatalogModeID}}selected{{end}}>{{.Name}}</option>{{end}}</select></label>
<label><input type=checkbox name=clear_bgp_password> {{tr "user.clear_password"}}</label>
<label><input type=checkbox name=enabled {{if .User.Enabled}}checked{{end}}> {{tr "user.enabled"}}</label>
<label><input type=checkbox name=locked {{if .User.SelectionLocked}}checked{{end}}> {{tr "user.lock_selection"}}</label>
<label><input type=checkbox name=filter_editable {{if .User.FilterEditable}}checked{{end}}> {{tr "users.allow_filter_editing"}}</label>
<label><input type=checkbox name=catalog_mode_editable {{if .User.CatalogEditable}}checked{{end}}> {{tr "users.allow_mode_editing"}}</label>
<input type=hidden name=filter_mode value="{{.User.FilterMode}}">
<button>{{tr "user.save"}}</button></form>
<form method=post action="/admin/user/{{.User.ID}}/delete" onsubmit="return confirm('{{tr "user.delete_confirm"}}');"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button class=danger>{{tr "user.delete"}}</button></form></section>
{{template "selection" .Selection}}{{end}}`

const communitiesBody = `{{with .Data}}
<header><h1>{{tr "communities.title"}}</h1><a href="/admin">{{tr "admin.link"}}</a></header>
{{if .Saved}}<p class=ok>{{if eq .Saved "reset"}}All communities reset to auto-generated values.{{else if eq .Saved "generated"}}Missing communities auto-generated.{{else}}{{tr "communities.saved"}}{{end}}</p>{{end}}
{{if .Error}}<p class=error>{{.Error}}</p>{{end}}
<section class=card>
<label>{{tr "catalog.mode"}} <select id=mode-select>
{{range .Modes}}<option value="{{.ID}}" {{if eq .ID $.Data.Mode.ID}}selected{{end}}>{{.Name}}</option>{{end}}
</select></label>
<form method=post action="/admin/communities" id=communities-form>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=mode value="{{.Mode.ID}}">
<div class=save-bar>
  <div><strong>{{tr "communities.title"}}</strong><br>
  <span class=muted id=change-summary>{{tr "communities.no_changes"}}</span></div>
  <button type=submit class=primary id=save-btn disabled>{{tr "communities.apply"}}</button>
</div>
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
<div style="margin-top:1rem;display:flex;gap:1rem">
<form method=post action="/admin/communities/generate" style="display:inline" onsubmit="return confirm('Generate community numbers for all categories and services that do not have one yet?')">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=mode value="{{.Mode.ID}}">
<button type=submit class=secondary>{{tr "communities.auto_generate"}}</button>
</form>
<form method=post action="/admin/communities/reset" style="display:inline" onsubmit="return confirm('This will reset ALL community numbers to auto-generated values. Any manual changes will be lost. Continue?')">
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

var activeEdit = null;

function closeEdit(cell, forceApply) {
  var inp = cell.querySelector('.community-input');
  if (inp && inp.dataset.changed === '0' && inp.value !== inp.dataset.original) {
    if (forceApply === undefined) {
      if (!confirm('Apply changes to this community number?')) {
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
  if (edited > 0) parts.push(edited + ' community number' + (edited > 1 ? 's' : '') + ' changed');
  if (reverted > 0) parts.push(reverted + ' reverted to auto');
  el.textContent = parts.length > 0 ? parts.join(', ') : {{printf "%q" (tr "communities.no_changes")}};
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
if(findDuplicate(cell.dataset.name,newVal)){alert('This community number is already used. Please choose a different number.');return}
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
if(newVal!=auto){if(!revert){revert=document.createElement('button');revert.type='button';revert.className='revert-btn';revert.textContent='↺';revert.title='Revert to auto';cell.insertBefore(revert,cell.querySelector('.edit-actions'));setupRevert(revert)}}else{if(revert)revert.style.display='none'}
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

// ↺ Revert to auto
function setupRevert(btn){
btn.addEventListener('click',function(){
var cell=this.closest('.community-cell');
var auto=cell.dataset.auto;
cell.querySelector('.community-value').textContent=auto;
cell.classList.add('reverted');
var hidden=document.createElement('input');
hidden.type='hidden';hidden.name=cell.dataset.name;hidden.value=auto;hidden.className='revert-hidden';
	var oldHidden=cell.querySelector('.revert-hidden');
	oldHidden&&oldHidden.remove();
	cell.appendChild(hidden);
	// Also update the edit input so it doesn't conflict on form submit
	var editInput=cell.querySelector('.community-input');
	if(editInput) editInput.value=auto;
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
if(c>0&&!confirm('Confirm: N communities will be updated. Continue?'.replace('N',c))){e.preventDefault()}
});

updateUI()
})();
</script>
{{end}}`

const communitiesTemplate = communitiesBody
