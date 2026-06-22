package web

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
<header><h1>{{.Adapter.Name}}</h1></header>
<section class=card>
<p><code>{{.Adapter.Key}}</code> · rev. {{.Adapter.Revision}}{{if .Adapter.BuiltIn}} · {{tr "adapters.built_in"}}{{end}}</p>
{{if .Error}}<h2>{{tr "adapters.error"}}</h2><pre class=error-output>{{.Error}}</pre>{{end}}
<form method=post action="{{if .Adapter.ID}}/admin/adapter/{{.Adapter.ID}}{{else}}/admin/adapter{{end}}">
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
{{if .Adapter.ID}}{{if .Adapter.BuiltIn}}<form method=post action="/admin/adapter/{{.Adapter.ID}}/reset" onsubmit="return confirm('{{tr "adapters.reset_confirm"}}');">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button class=danger>{{tr "adapters.reset"}}</button></form>{{end}}
{{if not .Adapter.BuiltIn}}<form method=post action="/admin/adapter/{{.Adapter.ID}}/delete" onsubmit="return confirm('Delete this adapter?');"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button class=danger>{{tr "common.delete"}}</button></form>{{end}}{{end}}
</section>{{end}}`

const userEditTemplate = `{{define "selection"}}` + selectionBody + `{{end}}` + sharedComponents + `{{with .Data}}
<header><h1>{{printf (tr "title.user") .User.Name}}</h1></header>
{{template "back-link" dict "URL" "/admin/users" "Label" (tr "nav.users")}}
{{template "error-message" .Error}}
<div class=tab-bar>
  <button class="tab active" data-tab=settings>{{tr "user.settings"}}</button>
  <button class=tab data-tab=selection>{{tr "selection.heading"}}</button>
  <button class=tab data-tab=filters>{{tr "filters.heading"}}</button>
</div>
<div class="tab-content tab-settings">
<section class=card>
{{template "section-header" (tr "users.bgp_section")}}
<form method=post action="{{if .User.ID}}/admin/user/{{.User.ID}}{{else}}/admin/user{{end}}">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=action value=settings>
<div class=form-grid>
{{template "form-field" dict "Label" "feeds.name" "Name" "name" "Value" .User.Name "Required" true "Hint" "hints.user_name"}}
{{template "form-field" dict "Label" "users.networks" "Name" "networks" "Value" .NetworksStr "Required" true "Hint" "hints.user_networks"}}
{{template "form-field" dict "Label" "users.peer_ip" "Name" "peer_ip" "Value" .User.PeerIP "Required" true "Hint" "hints.user_peer_ip" "Attrs" .PeerIPAttrs}}
{{template "form-checkbox" dict "Name" "dynamic_ip" "Label" "users.dynamic_ip" "Checked" .DynamicChecked "Hint" "hints.user_dynamic_ip" "Attrs" .DynamicIPAttrs}}
{{template "form-field" dict "Label" "users.peer_asn" "Type" "number" "Name" "peer_asn" "Value" .User.PeerASN "Required" true "Hint" "hints.user_peer_asn"}}
{{template "form-field" dict "Label" "users.next_hop" "Name" "next_hop" "Value" .User.NextHop "Placeholder" "auto" "Hint" "hints.user_next_hop"}}
{{template "form-field" dict "Label" "users.bgp_password" "Type" "password" "Name" "bgp_password" "Placeholder" (tr "users.bgp_password_placeholder") "Attrs" .PasswordAttrs}}
{{template "form-checkbox" dict "Name" "active_dial" "Label" "users.active_dial" "Checked" .ActiveDial "Hint" .ActiveDialHintResolved "Attrs" .ActiveDialAttrs}}
{{template "form-checkbox" dict "Name" "enabled" "Label" "user.enabled" "Checked" .User.Enabled}}
{{template "form-checkbox" dict "Name" "clear_bgp_password" "Label" "user.clear_password"}}
</div>
{{template "form-select" dict "Label" "catalog.mode" "Name" "catalog_mode_id" "Options" .ModeOptions "Hint" "hints.user_catalog_mode"}}
{{template "section-header" (tr "users.access_section")}}
<div class=form-grid>
{{template "form-select" dict "Label" "users.web_auth" "Name" "web_auth" "Options" .WebAuthOptions "Hint" "users.web_auth_hint"}}
{{template "form-checkbox" dict "Name" "locked" "Label" "user.lock_selection" "Checked" .User.SelectionLocked}}
{{template "form-checkbox" dict "Name" "filter_editable" "Label" "users.allow_filter_editing" "Checked" .User.FilterEditable}}
{{template "form-checkbox" dict "Name" "catalog_mode_editable" "Label" "users.allow_mode_editing" "Checked" .User.CatalogEditable}}
</div>
{{if .User.ID}}<section class=card id=credentials-section>
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
</section>{{end}}
<input type=hidden name=filter_mode value="{{.User.FilterMode}}">
<button class=primary>{{tr "user.save"}}</button></form>
{{if .User.ID}}<form method=post action="/admin/user/{{.User.ID}}/delete" onsubmit="return confirm('{{tr "user.delete_confirm"}}');"><input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button class=danger>{{tr "user.delete"}}</button></form>{{end}}</section></div>
{{template "selection" .Selection}}
<script>
(function(){
  var authSelect = document.querySelector('select[name=web_auth]');
  var credSection = document.getElementById('credentials-section');
  function toggleCredentials() {
    var v = authSelect.value;
    credSection.style.display = (v === 'login' || v === 'both' || v === 'any') ? '' : 'none';
  }
  if (authSelect) {
    authSelect.addEventListener('change', toggleCredentials);
    toggleCredentials();
  }
})();
</script>
<script>
(function(){
  var peerIp = document.getElementById('peer-ip');
  var dynamicIp = document.getElementById('dynamic-ip');
  var savedIp = '';
  if (peerIp && dynamicIp) {
    dynamicIp.addEventListener('change', function() {
      if (this.checked) {
        savedIp = peerIp.value;
        peerIp.value = '0.0.0.0';
        peerIp.readOnly = true;
      } else {
        peerIp.value = savedIp || '';
        peerIp.readOnly = false;
      }
    });
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
<header><h1>{{tr "communities.title"}}</h1></header>
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

const debugTemplate = `{{with .Data}}
<h1>{{tr "debug.heading"}}</h1>
<p class=muted>{{tr "debug.description"}}</p>

<form method=get action=/admin/debug class=card>
<div class=form-grid>
<label>{{tr "debug.input"}} <input name=cidr value="{{.CIDR}}" placeholder="8.8.8.8 or 10.0.0.0/8" autofocus></label>
<label>{{tr "catalog.mode"}}
<select name=mode>
{{range .Modes}}<option value="{{.ID}}" {{if eq .ID $.Data.ModeID}}selected{{end}}>{{.Name}}{{if not .Enabled}} ({{tr "catalog.disabled"}}){{end}}</option>{{end}}
</select></label>
</div>
<button type=submit class=primary>{{tr "debug.submit"}}</button>
</form>

{{if .Result}}
<section class=card>
<h2>{{tr "debug.results"}}</h2>
<p><strong>{{tr "debug.input"}}:</strong> <code>{{.Result.Query}}</code></p>

{{if .Result.FullServices}}
<h3>{{tr "debug.full_services"}}</h3>
<table>
<tr><th>{{tr "catalog.category"}}</th><th>{{tr "catalog.service"}}</th><th>{{tr "debug.coverage"}}</th></tr>
{{range .Result.FullServices}}
<tr><td>{{.Category}}</td><td>{{.Service}}</td><td class=ok>100%</td></tr>
{{end}}
</table>
{{end}}

{{if .Result.PartialServices}}
<h3>{{tr "debug.partial_services"}}</h3>
<table>
<tr><th>{{tr "catalog.category"}}</th><th>{{tr "catalog.service"}}</th><th>{{tr "debug.coverage"}}</th></tr>
{{range .Result.PartialServices}}
<tr><td>{{.Category}}</td><td>{{.Service}}</td><td>{{printf "%.1f%%" .Percentage}}</td></tr>
{{end}}
{{if .Result.CombinedServices}}
<tr style="font-weight:600"><td colspan=2>{{tr "debug.combined"}}</td><td>{{printf "%.1f%%" .Result.CombinedPercentage}}</td></tr>
{{end}}
</table>
{{end}}

{{if .Result.Users}}
<h3>{{tr "debug.users"}}</h3>
<table>
<tr><th>{{tr "feeds.name"}}</th><th>{{tr "debug.before_filters"}}</th><th>{{tr "debug.after_filters"}}</th></tr>
{{range .Result.Users}}
<tr><td>{{.Name}}</td><td>{{printf "%.1f%%" .BeforePercentage}}</td><td>{{printf "%.1f%%" .AfterPercentage}}</td></tr>
{{end}}
</table>
{{end}}

{{if not .Result.FullServices}}{{if not .Result.PartialServices}}{{if not .Result.Users}}
<p class=muted>{{tr "debug.no_matches"}}</p>
{{end}}{{end}}{{end}}
</section>
{{end}}
{{end}}`

const modesTemplate = `{{with .Data}}
<h1>{{tr "modes.heading"}}</h1>
{{if .Saved}}<p class=ok>{{tr "catalog.modes_saved"}}</p>{{end}}
<p class=muted>{{tr "catalog.modes_hint"}}</p>
<div class=card>
<table>
<tr><th>{{tr "catalog.mode_name"}}</th><th>{{tr "catalog.key"}}</th><th>{{tr "feeds.status"}}</th><th>{{tr "stats.feeds"}}</th><th></th></tr>
{{range .Modes}}
<tr>
<td>{{.Mode.Name}}</td>
<td><code>{{.Mode.Key}}</code></td>
<td>{{if .Mode.Enabled}}<span class=ok>enabled</span>{{else}}<span class=error>{{tr "catalog.disabled"}}</span>{{end}}</td>
<td>{{.FeedCount}}</td>
<td><a href="/admin/mode/{{.Mode.ID}}" class=button>{{tr "common.edit"}}</a></td>
</tr>{{end}}
</table>
</div>
<div class=card>
<h2>{{tr "catalog.mode_add"}}</h2>
<form method=post action=/admin/modes>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<div class=form-grid>
{{template "form-field" dict "Label" "catalog.mode_name" "Name" "name" "Required" true}}
{{template "form-field" dict "Label" "catalog.key" "Name" "key"}}
{{template "form-checkbox" dict "Name" "enabled" "Label" "feeds.enabled"}}
</div>
<button type=submit class=primary>{{tr "common.add"}}</button>
</form>
</div>
{{end}}`

const modeEditTemplate = `{{with .Data}}
<a href="/admin/modes" class=back-link>← {{tr "modes.heading"}}</a>
<h1>{{.Mode.Name}}</h1>
<div class=card>
<h2>{{tr "catalog.mode"}}</h2>
<form method=post action="/admin/modes/{{.Mode.ID}}">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<label>{{tr "catalog.mode_name"}} <input name=name value="{{.Mode.Name}}" required></label>
{{template "form-checkbox" dict "Name" "enabled" "Label" "feeds.enabled" "Checked" .Mode.Enabled}}
<button type=submit class=primary>{{tr "common.save"}}</button>
</form>
{{if gt .Mode.ID 3}}
<form method=post action="/admin/modes/{{.Mode.ID}}/delete" onsubmit="return confirm('{{tr "catalog.mode_delete_confirm"}}')">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<button type=submit class=danger>{{tr "common.delete"}}</button>
</form>
{{end}}
</div>
<div class=card>
<h2>{{tr "nav.feeds"}}</h2>
<table>
<tr><th>{{tr "feeds.name"}}</th><th>{{tr "feeds.url"}}</th><th></th></tr>
{{range .Feeds}}
<tr>
<td>{{.Name}}</td>
<td><code>{{.URL}}</code></td>
<td>
{{if index $.Data.ModeFeedIDs .ID}}
<form method=post action="/admin/modes/{{$.Data.Mode.ID}}/feeds" style=display:inline>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=feed_id value="{{.ID}}">
<input type=hidden name=action value=remove>
<button type=submit class=danger>{{tr "common.delete"}}</button>
</form>
{{else}}
<form method=post action="/admin/modes/{{$.Data.Mode.ID}}/feeds" style=display:inline>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<input type=hidden name=feed_id value="{{.ID}}">
<button type=submit>{{tr "common.add"}}</button>
</form>
{{end}}
</td>
</tr>{{end}}
</table>
</div>
{{end}}`

const feedEditTemplate = sharedComponents + `{{with .Data}}
<a href="/admin/feeds" class=back-link>← {{tr "nav.feeds"}}</a>
<h1>{{if .IsNew}}{{tr "feeds.add"}}{{else}}{{tr "feeds.edit"}}{{end}}</h1>
<div class=card>
{{template "section-header" (tr "feeds.edit")}}
<form method=post action="{{if .IsNew}}/admin/feed{{else}}/admin/feed/{{.Feed.ID}}{{end}}">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<label>{{tr "feeds.name"}} <input name=name value="{{.Feed.Name}}" required></label>
<p class=muted>{{tr "hints.feed_name"}}</p>
<label>{{tr "feeds.url"}} <input name=url value="{{.Feed.URL}}" required></label>
<p class=muted>{{tr "hints.feed_url"}}</p>
<label>{{tr "feeds.data"}} <textarea name=data rows=4 placeholder='{"category": "Russia", "service": "geoip-ru"}' class=mono>{{.Feed.Data}}</textarea></label>
<p class=muted>{{tr "hints.feed_data"}}</p>
<label>{{tr "catalog.modes"}}</label>
<div>
{{range .Modes}}<label class=checkbox-row><input type=checkbox name=mode_ids value="{{.ID}}" {{if index $.Data.FeedModeIDs .ID}}checked{{end}}> {{.Name}}</label>{{end}}
</div>
<div class=form-grid>
<label>{{tr "feeds.adapter"}} <select name=adapter_id>{{range .Adapters}}<option value="{{.ID}}" {{if eq .ID $.Data.Feed.AdapterID}}selected{{end}}>{{.Name}}</option>{{end}}</select></label>
{{template "form-field" dict "Label" "feeds.sync_interval" "Type" "number" "Name" "sync_interval" "Value" .Feed.SyncInterval "Placeholder" (tr "feeds.default_interval") "Hint" "hints.feed_sync_interval"}}
</div>
{{template "form-checkbox" dict "Name" "enabled" "Label" "feeds.enabled" "Checked" .Feed.Enabled}}
<button type=submit class=primary>{{tr "common.save"}}</button>
</form>
{{if not .IsNew}}<form method=post action="/admin/feed/{{.Feed.ID}}/delete" onsubmit="return confirm('{{tr "feeds.delete_confirm"}}')">
<input type=hidden name=csrf_token value="{{$.CSRFToken}}"><button type=submit class=danger>{{tr "common.delete"}}</button></form>{{end}}
</div>
{{end}}`

const settingsTemplate = `{{with .Data}}
<h1>{{tr "nav.settings"}}</h1>
{{if .Saved}}<p class=ok>{{tr "settings.saved"}}</p>{{end}}
<form method=post action=/admin/settings>
<input type=hidden name=csrf_token value="{{$.CSRFToken}}">
<div class=save-bar><button type=submit class=primary>{{tr "common.save"}}</button></div>
<div class=card>
<div class=settings-grid>
{{range .Sections}}
<div class=section-head><h2>{{tr .TitleKey}}</h2></div>
{{range .Fields}}
<div class=setting-row x-data="{hint:false}">
  <div class=setting-label><label for="s_{{.Key}}">{{tr .Name}}</label>
  {{if .EnvOverride}}<span class="pill muted" title="{{tr "settings.env_override_hint"}}">{{tr "settings.env_override"}}</span>{{end}}
  <button type=button class="hint-btn" @click="hint=!hint" :aria-expanded="hint" aria-label="{{tr "settings.help"}}">?</button>
  </div>
  <div class=setting-field>
  {{if eq .Type "select"}}
    <select name="{{.Key}}" id="s_{{.Key}}" {{if .EnvOverride}}disabled title="{{tr "settings.env_override_hint"}}"{{end}}>
    {{$field := .}}{{if not $field.Value}}<option value="" disabled selected>{{tr "settings.default"}}: {{$field.DefaultValue}}</option>{{end}}
    {{range $val, $labelKey := $field.Options}}<option value="{{$val}}" {{if eq $val $field.Value}}selected{{end}}>{{tr $labelKey}}</option>{{end}}
    </select>
  {{else if eq .Type "bool"}}
    <label class=checkbox-row><input type=checkbox name="{{.Key}}" value="true" {{if eq .Value "true"}}checked{{end}} {{if .EnvOverride}}disabled title="{{tr "settings.env_override_hint"}}"{{end}}> {{tr .Name}}</label>
  {{else if eq .Type "number"}}
    <input type=number name="{{.Key}}" id="s_{{.Key}}" value="{{.Value}}" {{if .EnvOverride}}disabled title="{{tr "settings.env_override_hint"}}"{{end}} {{if .Placeholder}}placeholder="{{tr .Placeholder}}"{{end}}>
  {{else}}
    <input type="{{.Type}}" name="{{.Key}}" id="s_{{.Key}}" value="{{.Value}}" {{if .EnvOverride}}disabled title="{{tr "settings.env_override_hint"}}"{{end}} {{if .Placeholder}}placeholder="{{tr .Placeholder}}"{{end}}>
  {{end}}
  {{if .Restart}}<span class=muted> ({{tr "settings.requires_restart"}})</span>{{end}}
  <div class=hint-popup x-show="hint" @click.away="hint=false" x-transition x-cloak>
    <p>{{tr (printf "settings.%s_hint" .Key)}}</p>
    <code>{{.EnvVar}}</code>
  </div>
  </div>
</div>
{{end}}
{{end}}
<div class=section-head><h2>{{tr "settings.section_bgp"}}</h2></div>
<div class=setting-row x-data="{hint:false}">
  <div class=setting-label><label for="s_allow_dynamic_peers">{{tr "settings.allow_dynamic_peers"}}</label>
  <button type=button class="hint-btn" @click="hint=!hint" :aria-expanded="hint" aria-label="{{tr "settings.help"}}">?</button>
  </div>
  <div class=setting-field>
  <label class=checkbox-row><input type=checkbox value="true" {{if .AllowDynamicPeers}}checked{{end}} disabled title="{{tr "settings.allow_dynamic_peers_hint"}}"> {{tr "settings.allow_dynamic_peers"}}</label>
  <div class=hint-popup x-show="hint" @click.away="hint=false" x-transition x-cloak>
    <p>{{tr "settings.allow_dynamic_peers_hint"}}</p>
    <code>WDBGP_ALLOW_DYNAMIC_PEERS</code>
  </div>
  </div>
</div>
<div class=setting-row x-data="{hint:false}">
  <div class=setting-label><label for="s_auto_restore_enabled">{{tr "settings.auto_restore_enabled"}}</label>
  <button type=button class="hint-btn" @click="hint=!hint" :aria-expanded="hint" aria-label="{{tr "settings.help"}}">?</button>
  </div>
  <div class=setting-field>
  <label class=checkbox-row><input type=checkbox value="true" {{if .AutoRestoreEnabled}}checked{{end}} disabled title="{{tr "settings.auto_restore_enabled_hint"}}"> {{tr "settings.auto_restore_enabled"}}</label>
  <div class=hint-popup x-show="hint" @click.away="hint=false" x-transition x-cloak>
    <p>{{tr "settings.auto_restore_enabled_hint"}}</p>
    <code>WDBGP_AUTO_RESTORE_ENABLED</code>
  </div>
  </div>
</div>
{{if .GlobalFilters}}
<div class=section-head><h2>{{tr "settings.section_filters"}}</h2></div>
<div style="grid-column:1/-1;padding:.5rem 0;border-bottom:1px solid var(--border)">
<p class=muted style=margin-bottom:.75rem>{{tr "global_filters.explanation"}}</p>
<div class=grid>
<label>{{tr "filters.allow"}} <textarea class=large style=min-height:12em name=filter_allow placeholder="{{tr "filters.allow_placeholder"}}">{{.GlobalFilters.Allow}}</textarea></label>
<label>{{tr "filters.deny"}} <textarea class=large style=min-height:12em name=filter_deny placeholder="0.0.0.0/0">{{.GlobalFilters.Deny}}</textarea></label>
</div>
</div>
{{end}}
</div>
</div>
</form>
{{end}}`
