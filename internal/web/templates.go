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
.muted{color:#667}.error{color:#a00}.ok{color:#075}.pill{display:inline-block;padding:.15rem .5rem;border-radius:999px;background:#edf2f8;color:#445}
.selection-form{padding-bottom:5.5rem}.save-bar{position:sticky;top:.5rem;z-index:2;display:flex;gap:1rem;align-items:center;justify-content:space-between;background:#10294f;color:white;border-radius:1rem;padding:.8rem 1rem;box-shadow:0 12px 28px #10294f40}
.save-bar .muted{color:#d7e4f5}.save-bar button{background:#33a36f}.save-bar button:disabled{background:#71829b;cursor:not-allowed}
.catalog-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(18rem,1fr));gap:1rem;margin-top:1rem}fieldset.category-card{border:1px solid #dfe5ee;border-radius:1rem;background:white;margin:0;padding:0;overflow:hidden}
.category-card legend{float:left;width:100%;padding:.85rem 1rem;background:#eef4fb;border-bottom:1px solid #dfe5ee}.category-card legend+*{clear:both}
.category-title{display:flex;gap:.55rem;align-items:center;margin:0;font-size:1.05rem}.service-list{padding:.75rem 1rem 1rem}.service-list label{font-weight:500;margin:.4rem 0}
.empty{background:white;border:1px dashed #b9c5d4;border-radius:1rem;padding:1rem}.status{font:12px ui-monospace,monospace;padding:.2rem .45rem;border-radius:999px;background:#edf2f8}
.language-switcher{display:flex;justify-content:flex-end;gap:.5rem;margin-bottom:.75rem}.language-switcher a[aria-current=page]{font-weight:700;text-decoration:none;color:#18212b}
</style></head><body><nav class=language-switcher aria-label="{{tr "language.label"}}">
<a href="{{.EnglishURL}}" title="{{tr "language.english"}}" aria-current="{{if eq .Lang "en"}}page{{else}}false{{end}}">EN</a>
<a href="{{.RussianURL}}" title="{{tr "language.russian"}}" aria-current="{{if eq .Lang "ru"}}page{{else}}false{{end}}">RU</a>
</nav>`

const pageEnd = `</body></html>`

const accessDeniedTemplate = `<h1>{{tr "access_denied.heading"}}</h1><p>IP: <code>{{.Data}}</code></p>`

const loginTemplate = `<h1>{{tr "admin.heading"}}</h1>{{if .Data}}<p class=error>{{.Data}}</p>{{end}}
<form method=post><label>{{tr "login.password"}} <input type=password name=password autofocus required></label><button>{{tr "login.submit"}}</button></form>`

const selectionBody = `{{$selection := .}}
<header><h1>{{.User.Name}}</h1>{{if not .Admin}}<a href="/admin">{{tr "admin.link"}}</a>{{end}}</header>
<section class=card><h2>{{tr "selection.heading"}}</h2>
<p class=muted>{{tr "selection.category_hint"}}</p>
{{if eq .Saved "1"}}<p class=ok>{{tr "selection.saved"}}</p>{{else if eq .Saved "0"}}<p class=error>{{tr "selection.save_failed"}}</p>{{end}}
{{if not .Editable}}<p class=muted>{{tr "selection.locked"}}</p>{{end}}
<form class=selection-form method=post action="{{if .Admin}}/admin/user/{{.User.ID}}{{else}}/selection{{end}}">
{{if .Admin}}<input type=hidden name=action value=selection>{{end}}
<div class=save-bar><div><strong>{{.User.Name}}</strong><br><span class=muted>{{tr "selection.selected"}} <span id=selected-count>{{.SelectedCount}}</span>. {{tr "selection.apply_hint"}}</span></div>
<button {{if not .Editable}}disabled{{end}}>{{tr "selection.save"}}</button></div>
{{if .Categories}}<div class=catalog-grid>{{range .Categories}}
<fieldset class=category-card><legend><label class=category-title><input type=checkbox name=category value="{{.Name}}" {{if .Selected}}checked{{end}} {{if not $selection.Editable}}disabled{{end}}> <strong>{{.Name}}</strong> <span class=pill>{{tr "selection.whole_category"}}</span></label></legend>
<div class=service-list>{{range .Services}}<label><input type=checkbox name=service value="{{.Value}}" {{if .Selected}}checked{{end}} {{if or (not $selection.Editable) .Disabled}}disabled{{end}}> {{.Name}}</label>{{end}}</div>
</fieldset>{{end}}</div>{{else}}<p class=empty>{{tr "selection.empty"}}</p>{{end}}</form></section>
<script>
document.querySelectorAll('input[name="category"]').forEach(function(categoryInput) {
  var fieldset = categoryInput.closest('fieldset');
  var services = fieldset ? fieldset.querySelectorAll('input[name="service"]') : [];
  var update = function() {
    services.forEach(function(serviceInput) {
      serviceInput.disabled = categoryInput.checked || categoryInput.disabled;
    });
    var count = document.getElementById('selected-count');
    if (count) {
      count.textContent = document.querySelectorAll('input[name="category"]:checked,input[name="service"]:checked:not(:disabled)').length;
    }
  };
  categoryInput.addEventListener('change', update);
  services.forEach(function(serviceInput) { serviceInput.addEventListener('change', update); });
  update();
});
</script>
{{with .Filters}}<section class=card><h2>{{tr "filters.heading"}}</h2>
{{if .Editable}}<p class=muted>{{tr "filters.explanation"}}</p>
<form method=post action="{{if .Admin}}/admin/user/{{$selection.User.ID}}{{else}}/filters{{end}}">
{{if .Admin}}<input type=hidden name=action value=filters>{{end}}
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
<header><h1>{{tr "admin.heading"}}</h1><a href="/">{{tr "user_interface.link"}}</a></header>
<section class=card><h2>{{tr "feeds.heading"}}</h2><table><tr><th>{{tr "feeds.name"}}</th><th>URL</th><th>{{tr "feeds.last_download"}}</th><th>{{tr "feeds.error"}}</th></tr>
{{range .Feeds}}<tr><td>{{.Name}}</td><td><code>{{.URL}}</code></td><td>{{.LastSuccess}}</td><td class=error>{{.LastError}}</td></tr>{{end}}</table>
<form method=post action=/admin/feed><h3>{{tr "feeds.add"}}</h3><label>{{tr "feeds.name"}} <input name=name required></label><label>URL <input type=url name=url required></label><button>{{tr "common.add"}}</button></form>
<form method=post action=/admin/sync><button>{{tr "feeds.download_now"}}</button></form></section>
<section class=card><h2>{{tr "global_filters.heading"}}</h2>
<p class=muted>{{tr "global_filters.explanation"}}</p>
<form method=post action=/admin/filters><div class=grid>
<label>{{tr "filters.allow"}} <textarea name=filter_allow placeholder="{{tr "filters.allow_placeholder"}}">{{.GlobalFilters.AllowText}}</textarea></label>
<label>{{tr "filters.deny"}} <textarea name=filter_deny>{{.GlobalFilters.DenyText}}</textarea></label></div>
<button>{{tr "global_filters.save"}}</button></form></section>
<section class=card><h2>{{tr "users.heading"}}</h2><table><tr><th>{{tr "feeds.name"}}</th><th>{{tr "users.cidr"}}</th><th>{{tr "users.peer"}}</th><th>{{tr "users.asn"}}</th><th>{{tr "users.status"}}</th></tr>
{{range .Users}}<tr><td><a href="/admin/user/{{.ID}}">{{.Name}}</a></td><td><code>{{join .Networks ", "}}</code></td><td><code>{{.PeerIP}}</code></td><td>{{.PeerASN}}</td><td><span class=status>{{state $.Data.PeerStates .PeerIP}}</span></td></tr>{{end}}</table></section>
<section class=card><form method=post action=/admin/user><h3>{{tr "users.add"}}</h3><div class=grid>
<label>{{tr "feeds.name"}} <input name=name required></label><label>{{tr "users.networks"}} <input name=networks required></label>
<label>{{tr "users.peer_ip"}} <input name=peer_ip required></label><label>{{tr "users.peer_asn"}} <input type=number min=1 name=peer_asn required></label>
<label>{{tr "users.next_hop"}} <input name=next_hop></label><label>{{tr "users.bgp_password"}} <input type=password name=bgp_password></label></div>
<label><input type=checkbox name=filter_editable> {{tr "users.allow_filter_editing"}}</label>
<button>{{tr "common.add"}}</button></form></section>{{end}}`

const userEditTemplate = `{{define "selection"}}` + selectionBody + `{{end}}{{with .Data}}
<header><h1>{{printf (tr "title.user") .User.Name}}</h1><a href=/admin>{{tr "admin.link"}}</a></header>
<section class=card><h2>{{tr "user.settings"}}</h2><form method=post action="/admin/user/{{.User.ID}}">
<input type=hidden name=action value=settings><div class=grid>
<label>{{tr "feeds.name"}} <input name=name value="{{.User.Name}}" required></label><label>{{tr "users.networks"}} <input name=networks value="{{join .User.Networks ", "}}" required></label>
<label>{{tr "users.peer_ip"}} <input name=peer_ip value="{{.User.PeerIP}}" required></label><label>{{tr "users.peer_asn"}} <input type=number min=1 name=peer_asn value="{{.User.PeerASN}}" required></label>
<label>{{tr "users.next_hop"}} <input name=next_hop value="{{.User.NextHop}}"></label><label>{{tr "users.bgp_password"}} <input type=password name=bgp_password placeholder="{{if .User.BGPPassword}}{{tr "user.password_set"}}{{else}}{{tr "user.password_not_set"}}{{end}}"></label></div>
<label><input type=checkbox name=clear_bgp_password> {{tr "user.clear_password"}}</label>
<label><input type=checkbox name=enabled {{if .User.Enabled}}checked{{end}}> {{tr "user.enabled"}}</label>
<label><input type=checkbox name=locked {{if .User.SelectionLocked}}checked{{end}}> {{tr "user.lock_selection"}}</label>
<label><input type=checkbox name=filter_editable {{if .User.FilterEditable}}checked{{end}}> {{tr "users.allow_filter_editing"}}</label>
<input type=hidden name=filter_mode value="{{.User.FilterMode}}">
<button>{{tr "user.save"}}</button></form>
<form method=post action="/admin/user/{{.User.ID}}/delete" onsubmit="return confirm('{{tr "user.delete_confirm"}}');"><button class=danger>{{tr "user.delete"}}</button></form></section>
{{template "selection" .Selection}}{{end}}`
