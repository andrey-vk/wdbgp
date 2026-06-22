package web

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
<td><span class="status-dot {{if eq .PeerState "ESTABLISHED"}}up{{else}}down{{end}}" title="{{tr (printf "bgp.state.%s" .PeerState)}}"></span> <code>{{.User.PeerIP}}</code></td>
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
<tr><th>{{tr "feeds.name"}}</th><th>{{tr "catalog.modes"}}</th><th>{{tr "feeds.status"}}</th><th>{{tr "feeds.last_sync"}}</th><th></th></tr>
{{range .Feeds}}
<tr>
<td>{{.Feed.Name}}</td>
<td>{{.ModeNames}}</td>
<td>{{if .Feed.Enabled}}<span class=ok>enabled</span>{{else}}<span class=error>disabled</span>{{end}}</td>
<td>{{if .LastSync}}{{.LastSync}}{{else}}—{{end}}</td>
<td>
<a href="/admin/feed/{{.Feed.ID}}" class=button>{{tr "common.edit"}}</a>
<form method=post action="/admin/feeds/{{.Feed.ID}}/force-sync" style=display:inline>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button title="{{tr "feeds.force_sync"}}">⟳</button>
</form>
</td>
</tr>{{end}}
</table>
</div>
<p><a href="/admin/feed" class="button primary">{{tr "feeds.add"}}</a></p>
<form method=post action="/admin/feeds/sync-all" style=display:inline>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button type=submit class=button>{{tr "feeds.sync_all"}}</button>
</form>
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
<p><a href="/admin/adapter/0" class="button primary">{{tr "adapters.add"}}</a></p>
{{end}}`
