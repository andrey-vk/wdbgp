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
button.danger{background:#b42318} table{border-collapse:separate;border-spacing:0;width:100%;background:white;border:1px solid #dfe5ee;border-radius:.8rem;overflow:hidden}
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
{{if .Categories}}<div class=catalog-grid>{{range .Categories}}
<fieldset class=category-card><legend><label class=category-title><input type=checkbox name=category value="{{.Name}}" {{if .Selected}}checked{{end}} {{if not $selection.Editable}}disabled{{end}}> <strong>{{.Name}}</strong> <span class=pill>{{tr "selection.whole_category"}}</span></label></legend>
<div class=service-list>{{range .Services}}<label><input type=checkbox name=service value="{{.Value}}" {{if .Selected}}checked{{end}} {{if or (not $selection.Editable) .Disabled}}disabled{{end}}> {{.Name}}</label>{{end}}</div>
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
