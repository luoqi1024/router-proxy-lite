'use strict';
const $ = (id) => document.getElementById(id);
let state = null, currentPage = 'overview', working = false, toastTimer;
let failoverDraft = new Set(), failoverDirty = false;
async function api(path, body, method) {
 const options = {method: method || (body === undefined ? 'GET' : 'POST'), headers:{}, credentials:'same-origin'};
 if (body !== undefined) { options.headers['Content-Type']='application/json'; options.body=JSON.stringify(body); }
 const response=await fetch('/api/'+path,options);
 const result=await response.json();
 if (!response.ok) { if(response.status===401 && path!=='session') showLogin(); const e=new Error(result.error || '请求失败');e.status=response.status;throw e; }
 return result;
}
function message(text){$('toast').textContent=text;$('toast').hidden=false;clearTimeout(toastTimer);toastTimer=setTimeout(()=>{$('toast').hidden=true;},3200);}
function showLogin(){$('password-dialog').close();$('password-form').reset();$('setup-form').reset();$('setup-form').hidden=true;$('login-form').hidden=false;$('app').hidden=true;$('login').hidden=false;state=null;failoverDirty=false;}
function showSetup(){showLogin();$('login-form').hidden=true;$('setup-form').hidden=false;}
function error(text){$('error-banner').textContent=text;$('error-banner').hidden=!text;}
function busy(value){working=value;document.body.classList.toggle('busy',value);document.querySelectorAll('#app button:not(.nav-item), #import-form button, #password-form button, #password-form input, #setup-form button, #setup-form input, #app input, #app select').forEach(b=>b.disabled=value);if(!value&&state)renderSubscriptionActions();}
async function perform(action,success){if(working)return;busy(true);error('');try{const data=await action();if(data&&data.nodes){state=data;render();}if(success)message(success);}catch(e){error(e.message);}finally{busy(false);}}
function showPage(page){currentPage=page;for(const p of ['overview','nodes','device'])$('page-'+p).hidden=p!==page;document.querySelectorAll('[data-page]').forEach(b=>b.classList.toggle('active',b.dataset.page===page));}
function date(value){if(!value||value.startsWith('0001-'))return '尚未更新';return new Date(value).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'});}
function render(){
 if(!state)return;
 $('login').hidden=true;$('app').hidden=false;$('version').textContent='v'+state.version;
 const demo=state.mode==='demo';$('demo-banner').hidden=!demo;$('demo-import').hidden=!demo;
 const chosen=state.nodes.find(n=>n.id===state.selected);
 $('selected-node').textContent=chosen?chosen.name:'未选择节点';$('node-protocol').textContent=chosen?chosen.type:'未选择';
 const direct=state.policy==='direct';const active=state.enabled&&state.running&&!direct;
 $('status-card').classList.toggle('on',active);$('status-title').textContent=active?'代理已开启':direct?'全部直连':state.enabled?'代理未就绪':'代理已关闭';
 $('status-pill').textContent=active?(demo?'模拟运行':'运行中'):direct?'直连':state.enabled?'异常':'已关闭';
 $('status-detail').textContent=active?(demo?'演示模式，不影响实际网络。':'按所选策略转发。'):direct?'所有连接直连。':state.enabled?'内核未运行，请关闭后重新开启。':'开启后按所选策略转发。';
 $('toggle-proxy').textContent=state.enabled?'关闭代理':'开启代理';
 document.querySelectorAll('[data-policy]').forEach(b=>{b.classList.toggle('selected',b.dataset.policy===state.policy);b.setAttribute('aria-pressed',String(b.dataset.policy===state.policy));});
 $('subscription-name').textContent=state.subscriptionName||'未导入订阅';const activeProfile=(state.subscriptions||[]).find(p=>p.id===state.activeSubscription);$('subscription-updated').textContent=state.subscriptionName?'更新于 '+date(state.updatedAt)+' · '+state.nodes.length+' 个节点'+(activeProfile?.nodeFilter?' · 筛选：'+activeProfile.nodeFilter:''):'支持 Clash / Mihomo 格式';
 renderSubscriptions();
 $('node-count').textContent=String(state.nodes.length);$('warnings').textContent=(state.warnings||[]).join('\n');$('warnings').hidden=!(state.warnings||[]).length;
 if(!failoverDirty){failoverDraft=new Set(state.failover?.nodes||[]);$('failover-enabled').checked=!!state.failover?.enabled;}
 $('failover-status').textContent=failoverDirty?'名单尚未保存':healthText();
 if(active&&state.failover?.enabled&&['retrying','switching','unavailable'].includes(state.health?.status)){
  $('status-title').textContent='节点连接异常';$('status-pill').textContent=state.health.status==='switching'?'正在切换':'探测失败';$('status-detail').textContent=healthText();
 }
 renderNodes();
 $('device-description').textContent=demo?'电脑演示 · '+state.device.architecture:state.device.architecture+' · 仅 IPv4';
 $('device-checks').replaceChildren();for(const check of state.device.checks||[]){const row=document.createElement('div');row.className='check'+(check.ok?'':' bad');const icon=document.createElement('span');icon.textContent=check.ok?'✓':'!';const content=document.createElement('div'),title=document.createElement('strong'),detail=document.createElement('p');title.textContent=check.name;detail.textContent=check.detail;content.append(title,detail);row.append(icon,content);$('device-checks').append(row);}
 $('events').replaceChildren();for(const event of [...(state.events||[])].reverse()){const li=document.createElement('li'),time=document.createElement('time'),text=document.createElement('span');time.textContent=date(event.time);text.textContent=event.message;li.append(time,text);$('events').append(li);}
 showPage(currentPage);
}
function healthText(){
 const labels={disabled:'尚未开启',demo:'演示模式：只保存名单，不进行网络探测',paused:'已暂停：代理关闭、直连或内核未运行',waiting:'等待首次连通性检查',checking:'正在检查当前节点',healthy:'最近探测通过',retrying:'当前节点探测失败，正在重试',switching:'正在寻找可用备用节点',unavailable:'暂未找到可用节点，稍后重试'};
 return (labels[state.health?.status]||'等待检查')+(state.health?.failures?' · 连续失败 '+state.health.failures+' 次':'');
}
function markFailoverDirty(){failoverDirty=true;$('failover-status').textContent='名单尚未保存';}
function renderSubscriptions(){
 const select=$('subscription-select');select.replaceChildren();
 for(const sub of state.subscriptions||[]){const option=document.createElement('option');option.value=sub.id;option.textContent=sub.name+' · '+sub.nodeCount+' 个节点';select.append(option);}
 select.value=state.activeSubscription||'';$('subscription-picker').hidden=!(state.subscriptions||[]).length;
 $('subscription-limit').textContent=(state.subscriptions||[]).length+' / '+(state.maxSubscriptions||3)+' 套';renderSubscriptionActions();
}
function renderSubscriptionActions(){
 const active=(state.subscriptions||[]).find(p=>p.id===state.activeSubscription);
 $('update-subscription').disabled=working||!active?.canRefresh;
 $('delete-subscription').disabled=working||!active||state.enabled;
 $('delete-subscription').title=state.enabled?'关闭代理后可删除当前订阅':'';
}
function renderNodes(){
 if(!state)return;const search=$('node-search').value.trim().toLowerCase();$('node-list').replaceChildren();$('empty-nodes').hidden=!!state.nodes.length;
 for(const node of state.nodes.filter(n=>(n.name+' '+n.type).toLowerCase().includes(search))){
  const button=document.createElement('button');button.className='node'+(node.id===state.selected?' selected':'');button.setAttribute('aria-pressed',String(node.id===state.selected));
  const globe=document.createElement('span');globe.className='node-globe';globe.textContent='◎';const details=document.createElement('div'),name=document.createElement('strong'),protocol=document.createElement('small'),dot=document.createElement('span');name.textContent=node.name;protocol.textContent=node.type+(node.id===state.selected?' · 当前选择':'');details.append(name,protocol);dot.className='radio-dot';button.append(globe,details,dot);button.addEventListener('click',()=>perform(()=>api('settings',{selected:node.id}),'节点已选择'));const row=document.createElement('div');row.className='node-entry';const label=document.createElement('label');label.className='backup-choice';const checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.checked=failoverDraft.has(node.id);checkbox.setAttribute('aria-label','备用节点：'+node.name);checkbox.addEventListener('change',()=>{if(checkbox.checked&&failoverDraft.size>=5){checkbox.checked=false;message('备用名单最多 5 个节点');return;}if(checkbox.checked)failoverDraft.add(node.id);else failoverDraft.delete(node.id);markFailoverDirty();});label.append(checkbox,document.createTextNode('备用'));row.append(button,label);$('node-list').append(row);
 }
 if(state.nodes.length&&!$('node-list').children.length){const p=document.createElement('p');p.className='muted';p.textContent='没有匹配的节点';$('node-list').append(p);}
}
$('login-form').addEventListener('submit',async event=>{event.preventDefault();$('login-error').textContent='';const button=event.submitter;button.disabled=true;try{state=await api('session',{key:$('login-key').value});$('login-key').value='';render();}catch(e){$('login-error').textContent=e.message;}finally{button.disabled=false;}});
document.querySelectorAll('[data-page]').forEach(button=>button.addEventListener('click',()=>showPage(button.dataset.page)));
document.querySelectorAll('[data-policy]').forEach(button=>button.addEventListener('click',()=>perform(()=>api('settings',{policy:button.dataset.policy}),'策略已保存')));
$('go-nodes').addEventListener('click',()=>showPage('nodes'));
$('toggle-proxy').addEventListener('click',()=>{if(!state.selected&&!state.enabled&&state.policy!=='direct'){showPage('nodes');message('先导入订阅并选择节点');return;}perform(()=>api('settings',{enabled:!state.enabled}),state.enabled?'已恢复普通上网':'设置已保存');});
$('refresh-state').addEventListener('click',()=>perform(()=>api('state'),'状态已刷新'));
$('logout').addEventListener('click',async()=>{try{await api('session',undefined,'DELETE');showLogin();}catch(e){error(e.message);}});
$('node-search').addEventListener('input',renderNodes);
$('demo-import').addEventListener('click',()=>perform(()=>api('subscription',{url:'demo://starter',name:'示例订阅'}),'已载入示例节点'));
$('open-import').addEventListener('click',()=>{$('import-error').textContent='';$('import-dialog').showModal();});
$('close-import').addEventListener('click',()=>$('import-dialog').close());
$('import-form').addEventListener('submit',async event=>{event.preventDefault();if(working)return;const url=$('sub-url').value.trim(),content=$('sub-content').value.trim();if(!url&&!content){$('import-error').textContent='请填写订阅链接或配置内容';return;}if(url&&content){$('import-error').textContent='链接和配置内容只需填写其中一种';return;}busy(true);$('import-error').textContent='';try{const data=await api('subscription',{url,name:$('sub-name').value.trim(),content,nodeFilter:$('sub-filter').value.trim(),oneTime:$('sub-onetime').checked});if(data.activeSubscription!==state.activeSubscription||data.updatedAt!==state.updatedAt)failoverDirty=false;state=data;$('sub-url').value='';$('sub-content').value='';$('sub-filter').value='';$('sub-onetime').checked=false;$('import-dialog').close();render();message('订阅已保存，可在菜单中切换');}catch(e){$('import-error').textContent=e.message;}finally{busy(false);}});
$('update-subscription').addEventListener('click',()=>perform(async()=>{const data=await api('subscription/refresh',{});failoverDirty=false;return data;},'订阅已更新'));
$('subscription-select').addEventListener('change',()=>{const id=$('subscription-select').value;if(failoverDirty&&!confirm('切换订阅会丢弃尚未保存的备用名单，继续？')){$('subscription-select').value=state.activeSubscription;return;}perform(async()=>{try{const data=await api('subscription/switch',{id});failoverDirty=false;failoverDraft=new Set();$('node-search').value='';return data;}finally{$('subscription-select').value=state.activeSubscription;}},'订阅已切换');});
$('delete-subscription').addEventListener('click',()=>{if(!confirm('删除当前订阅？此操作无法撤销。'))return;perform(async()=>{const data=await api('subscription/delete',{id:state.activeSubscription});failoverDirty=false;$('node-search').value='';return data;},'订阅已删除');});
api('setup').then(info=>{if(info.required){showSetup();return;}return api('state').then(data=>{state=data;render();});}).catch(()=>showLogin());

$('failover-enabled').addEventListener('change',markFailoverDirty);
$('save-failover').addEventListener('click',()=>perform(async()=>{const data=await api('settings',{failover:{enabled:$('failover-enabled').checked,nodes:[...failoverDraft]}});failoverDirty=false;return data;},'备用名单已保存'));
setInterval(async()=>{
 if(!state||working||failoverDirty||document.visibilityState!=='visible'||$('import-dialog').open||$('password-dialog').open)return;
 const before=state;
 try{const data=await api('state');if(!working&&state===before&&!failoverDirty){state=data;render();}}catch{}
},15000);

$('open-password').addEventListener('click',()=>{$('password-form').reset();$('password-error').textContent='';$('password-dialog').showModal();});
$('close-password').addEventListener('click',()=>$('password-dialog').close());
$('password-dialog').addEventListener('close',()=>{$('password-form').reset();$('password-error').textContent='';});
$('password-form').addEventListener('submit',async event=>{
 event.preventDefault();if(working)return;
 const password=$('new-password').value,confirm=$('confirm-password').value;
 if(password!==confirm){$('password-error').textContent='两次输入的新密码不一致';return;}
 busy(true);$('password-error').textContent='';
 try{await api('password',{password,confirm});showLogin();$('login-error').textContent='';message('密码已修改，请使用新密码登录');$('login-key').focus();}
 catch(e){$('password-error').textContent=e.message;}
 finally{busy(false);}
});

$('setup-form').addEventListener('submit',async event=>{
 event.preventDefault();if(working)return;
 const password=$('setup-password').value,confirm=$('setup-confirm').value;
 if(password!==confirm){$('setup-error').textContent='两次输入的密码不一致';return;}
 busy(true);$('setup-error').textContent='';let saved=false;
 try{await api('setup',{password,confirm});saved=true;showLogin();state=await api('session',{key:password});render();message('管理密码已设置');}
 catch(e){if(saved||e.status===409){showLogin();$('login-error').textContent=saved?'密码已设置，请使用刚设置的密码登录':e.message;}else{$('setup-error').textContent=e.message;}}
 finally{busy(false);}
});
