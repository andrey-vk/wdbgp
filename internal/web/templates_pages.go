package web

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

const selectionTemplate = `{{with .Data}}` + selectionBody + `{{end}}`

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
<a href=/admin/modes hx-get=/admin/modes hx-target=#main hx-push-url=true>{{tr "nav.modes"}}</a>
<a href=/admin/adapters hx-get=/admin/adapters hx-target=#main hx-push-url=true>{{tr "nav.adapters"}}</a>
<a href=/admin/settings hx-get=/admin/settings hx-target=#main hx-push-url=true>{{tr "nav.settings"}}</a>
<a href=/admin/debug hx-get=/admin/debug hx-target=#main hx-push-url=true class=sidebar-subtle>{{tr "debug.heading"}}</a>
<div class=sidebar-spacer></div>
<a href=/ hx-get=/ hx-target=#main hx-push-url=true class=sidebar-subtle>{{tr "nav.user_page"}}</a>
</nav>
<div class=main>
<header class=topbar id=topbar>
<button onclick="cycleTheme()" title="Theme" class=theme-btn>🌙</button>
<a href="#" data-lang="en" class="lang-switch">EN</a>
<a href="#" data-lang="ru" class="lang-switch">RU</a>
<form method=post action=/admin/logout style=display:inline>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button type=submit>{{tr "admin.logout"}}</button>
</form>
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

const degradedTemplate = `<h1>{{tr "title.db_mismatch"}}</h1>
<section class=card>
<p>{{printf (tr "error.db_too_new") .CurrentVersion .ServerVersion}}</p>
{{if .Reason}}<p class=muted>{{.Reason}}</p>{{end}}
<p class=muted>{{tr "error.db_too_new_hint"}}</p>
</section>`
