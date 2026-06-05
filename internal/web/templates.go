package web

const pageStart = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
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
</style></head><body>`

const pageEnd = `</body></html>`

const accessDeniedTemplate = `<h1>Нет доступа</h1><p>IP: <code>{{.Data}}</code></p>`

const loginTemplate = `<h1>Админка</h1>{{if .Data}}<p class=error>{{.Data}}</p>{{end}}
<form method=post><label>Пароль <input type=password name=password autofocus required></label><button>Войти</button></form>`

const selectionBody = `{{$selection := .}}
<header><h1>{{.User.Name}}</h1>{{if not .Admin}}<a href="/admin">Админка</a>{{end}}</header>
<section class=card><h2>Выбор сервисов</h2>
<p class=muted>Категория целиком включает также сервисы, которые появятся в ней позже.</p>
{{if eq .Saved "1"}}<p class=ok>Выбор сохранён, BGP-анонсы обновлены.</p>{{else if eq .Saved "0"}}<p class=error>Не удалось обновить BGP-анонсы.</p>{{end}}
{{if not .Editable}}<p class=muted>Выбор заблокирован администратором.</p>{{end}}
<form class=selection-form method=post action="{{if .Admin}}/admin/user/{{.User.ID}}{{else}}/selection{{end}}">
{{if .Admin}}<input type=hidden name=action value=selection>{{end}}
<div class=save-bar><div><strong>{{.User.Name}}</strong><br><span class=muted>Изменения применяются сразу после сохранения</span></div>
<button {{if not .Editable}}disabled{{end}}>Сохранить маршруты</button></div>
{{if .Categories}}<div class=catalog-grid>{{range .Categories}}
<fieldset class=category-card><legend><label class=category-title><input type=checkbox name=category value="{{.Name}}" {{if .Selected}}checked{{end}} {{if not $selection.Editable}}disabled{{end}}> <strong>{{.Name}}</strong> <span class=pill>целиком</span></label></legend>
<div class=service-list>{{range .Services}}<label><input type=checkbox name=service value="{{.Value}}" {{if .Selected}}checked{{end}} {{if not $selection.Editable}}disabled{{end}}> {{.Name}}</label>{{end}}</div>
</fieldset>{{end}}</div>{{else}}<p class=empty>Каталог пока пуст.</p>{{end}}</form></section>
{{with .Filters}}<section class=card><h2>Фильтрация маршрутов</h2>
{{if .Editable}}<p class=muted>Режим "дополнить" применяет глобальные и пользовательские списки вместе. Режим "заменить" использует только пользовательские списки. Пустой allow разрешает все выбранные маршруты; deny вырезается из широких префиксов.</p>
<form method=post action="{{if .Admin}}/admin/user/{{$selection.User.ID}}{{else}}/filters{{end}}">
{{if .Admin}}<input type=hidden name=action value=filters>{{end}}
<label>Режим фильтрации <select name=filter_mode>
<option value="global" {{if eq .Mode "global"}}selected{{end}}>использовать только глобальные списки</option>
<option value="extend" {{if eq .Mode "extend"}}selected{{end}}>дополнить глобальные списки пользовательскими</option>
<option value="override" {{if eq .Mode "override"}}selected{{end}}>заменить глобальные списки пользовательскими</option>
</select></label>
<div class=grid><label>Allow CIDR, по одному на строку <textarea name=filter_allow placeholder="Пусто = разрешить всё">{{.AllowText}}</textarea></label>
<label>Deny CIDR, по одному на строку <textarea name=filter_deny placeholder="1.1.1.1/32">{{.DenyText}}</textarea></label></div>
<button>Сохранить фильтр</button></form>
{{else}}<p class=muted>{{if eq .Mode "override"}}Используются пользовательские списки, управляемые администратором.{{else if eq .Mode "extend"}}Глобальные списки дополнены пользовательскими списками администратора.{{else}}Используются глобальные списки администратора.{{end}}</p>{{end}}
</section>{{end}}`

const selectionTemplate = `{{with .Data}}` + selectionBody + `{{end}}`

const adminTemplate = `{{with .Data}}
<header><h1>Админка</h1><a href="/">Пользовательский интерфейс</a></header>
<section class=card><h2>Фиды</h2><table><tr><th>Имя</th><th>URL</th><th>Последняя загрузка</th><th>Ошибка</th></tr>
{{range .Feeds}}<tr><td>{{.Name}}</td><td><code>{{.URL}}</code></td><td>{{.LastSuccess}}</td><td class=error>{{.LastError}}</td></tr>{{end}}</table>
<form method=post action=/admin/feed><h3>Добавить фид</h3><label>Имя <input name=name required></label><label>URL <input type=url name=url required></label><button>Добавить</button></form>
<form method=post action=/admin/sync><button>Скачать фиды сейчас</button></form></section>
<section class=card><h2>Глобальная фильтрация маршрутов</h2>
<p class=muted>Пустой allow разрешает все выбранные маршруты. Deny-подсети физически вырезаются из более широких анонсов. Default routes из фидов всегда отбрасываются.</p>
<form method=post action=/admin/filters><div class=grid>
<label>Allow CIDR, по одному на строку <textarea name=filter_allow placeholder="Пусто = разрешить всё">{{.GlobalFilters.AllowText}}</textarea></label>
<label>Deny CIDR, по одному на строку <textarea name=filter_deny>{{.GlobalFilters.DenyText}}</textarea></label></div>
<button>Сохранить глобальный фильтр</button></form></section>
<section class=card><h2>Пользователи</h2><table><tr><th>Имя</th><th>CIDR</th><th>BGP peer</th><th>ASN</th><th>Состояние</th></tr>
{{range .Users}}<tr><td><a href="/admin/user/{{.ID}}">{{.Name}}</a></td><td><code>{{join .Networks ", "}}</code></td><td><code>{{.PeerIP}}</code></td><td>{{.PeerASN}}</td><td><span class=status>{{state $.Data.PeerStates .PeerIP}}</span></td></tr>{{end}}</table></section>
<section class=card><form method=post action=/admin/user><h3>Добавить пользователя</h3><div class=grid>
<label>Имя <input name=name required></label><label>Пользовательские CIDR, через запятую <input name=networks required></label>
<label>IP BGP peer <input name=peer_ip required></label><label>ASN peer <input type=number min=1 name=peer_asn required></label>
<label>Next hop для анонсов <input name=next_hop></label><label>BGP MD5 пароль <input type=password name=bgp_password></label></div>
<label><input type=checkbox name=filter_editable> разрешить пользователю настраивать режим и списки фильтрации</label>
<button>Добавить</button></form></section>{{end}}`

const userEditTemplate = `{{define "selection"}}` + selectionBody + `{{end}}{{with .Data}}
<header><h1>Пользователь {{.User.Name}}</h1><a href=/admin>Админка</a></header>
<section class=card><h2>Параметры пользователя</h2><form method=post action="/admin/user/{{.User.ID}}">
<input type=hidden name=action value=settings><div class=grid>
<label>Имя <input name=name value="{{.User.Name}}" required></label><label>Пользовательские CIDR, через запятую <input name=networks value="{{join .User.Networks ", "}}" required></label>
<label>IP BGP peer <input name=peer_ip value="{{.User.PeerIP}}" required></label><label>ASN peer <input type=number min=1 name=peer_asn value="{{.User.PeerASN}}" required></label>
<label>Next hop для анонсов <input name=next_hop value="{{.User.NextHop}}"></label><label>BGP MD5 пароль <input type=password name=bgp_password placeholder="{{if .User.BGPPassword}}Пароль задан; пустое поле не изменит его{{else}}Не задан{{end}}"></label></div>
<label><input type=checkbox name=clear_bgp_password> очистить BGP MD5 пароль</label>
<label><input type=checkbox name=enabled {{if .User.Enabled}}checked{{end}}> пользователь включён</label>
<label><input type=checkbox name=locked {{if .User.SelectionLocked}}checked{{end}}> запретить пользователю менять выбор</label>
<label><input type=checkbox name=filter_editable {{if .User.FilterEditable}}checked{{end}}> разрешить пользователю настраивать режим и списки фильтрации</label>
<input type=hidden name=filter_mode value="{{.User.FilterMode}}">
<button>Сохранить параметры</button></form>
<form method=post action="/admin/user/{{.User.ID}}/delete" onsubmit="return confirm('Удалить пользователя? Это также удалит его выбор сервисов.');"><button class=danger>Удалить пользователя</button></form></section>
{{template "selection" .Selection}}{{end}}`
