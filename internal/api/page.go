package api

// indexHTML 是内嵌的单页前端 (纯原生 JS, 无外部依赖)。
// 访问 GET / 即可打开, 提供: 敏感词检测、词库增删改查、统计看板。
const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>noblack · 敏感词检测控制台</title>
<style>
  * { box-sizing: border-box; }
  body { margin:0; font-family:-apple-system,"Segoe UI","Microsoft YaHei",sans-serif; background:#0f172a; color:#e2e8f0; }
  header { padding:18px 28px; background:#1e293b; border-bottom:1px solid #334155; display:flex; align-items:center; gap:14px; }
  header h1 { font-size:18px; margin:0; }
  header .badge { font-size:12px; color:#94a3b8; }
  .tabs { display:flex; gap:6px; padding:12px 28px 0; background:#1e293b; }
  .tab { padding:9px 18px; cursor:pointer; border-radius:8px 8px 0 0; color:#94a3b8; user-select:none; }
  .tab.active { background:#0f172a; color:#38bdf8; font-weight:600; }
  main { padding:24px 28px; max-width:1100px; }
  .panel { display:none; } .panel.active { display:block; }
  textarea, input, select { background:#1e293b; border:1px solid #334155; color:#e2e8f0; border-radius:8px; padding:10px 12px; font-size:14px; width:100%; }
  textarea { min-height:120px; resize:vertical; }
  button { background:#0ea5e9; color:#fff; border:none; border-radius:8px; padding:10px 18px; font-size:14px; cursor:pointer; }
  button:hover { background:#0284c7; }
  button.ghost { background:transparent; border:1px solid #334155; color:#94a3b8; }
  button.danger { background:#ef4444; } button.danger:hover { background:#dc2626; }
  button.sm { padding:5px 12px; font-size:13px; }
  .row { display:flex; gap:12px; align-items:center; flex-wrap:wrap; margin:12px 0; }
  .card { background:#1e293b; border:1px solid #334155; border-radius:12px; padding:18px; margin:14px 0; }
  table { width:100%; border-collapse:collapse; font-size:14px; }
  th,td { text-align:left; padding:10px 8px; border-bottom:1px solid #334155; }
  th { color:#94a3b8; font-weight:500; }
  .tag { display:inline-block; background:#334155; color:#7dd3fc; border-radius:5px; padding:2px 8px; font-size:12px; margin:2px; }
  .tag.remark { color:#fbbf24; }
  .hl { background:#7c2d12; color:#fed7aa; border-radius:3px; padding:0 2px; }
  .stat-grid { display:grid; grid-template-columns:repeat(4,1fr); gap:14px; }
  .model-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:14px; margin:14px 0; }
  .model-card { background:#0f172a; border:1px solid #334155; border-radius:12px; padding:16px; }
  .model-card h4 { margin:0 0 10px; display:flex; justify-content:space-between; align-items:center; gap:8px; }
  .model-score { font-size:28px; font-weight:700; color:#38bdf8; margin:8px 0; }
  .action { display:inline-block; border-radius:999px; padding:3px 10px; font-size:12px; font-weight:700; }
  .action-pass { color:#bef264; background:#365314; }
  .action-review { color:#fde68a; background:#78350f; }
  .action-block { color:#fecaca; background:#7f1d1d; }
  .model-meta { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:6px 12px; font-size:12px; color:#94a3b8; }
  @media(max-width:720px){ .model-grid{grid-template-columns:1fr;} }
  .stat-box { background:#0f172a; border:1px solid #334155; border-radius:10px; padding:16px; text-align:center; }
  .stat-box .num { font-size:28px; font-weight:700; color:#38bdf8; }
  .stat-box .lbl { font-size:12px; color:#94a3b8; margin-top:4px; }
  .muted { color:#64748b; font-size:13px; }
  .toast { position:fixed; top:20px; right:20px; background:#334155; color:#e2e8f0; padding:12px 18px; border-radius:8px; border-left:4px solid #38bdf8; opacity:0; transition:.3s; z-index:99; }
  .toast.show { opacity:1; } .toast.err { border-left-color:#ef4444; }
  h3 { margin-top:0; }
  .lvl-high{color:#f87171} .lvl-mid{color:#fbbf24} .lvl-low{color:#a3e635}
</style>
</head>
<body>
<header>
  <h1>🛡️ noblack 控制台</h1>
  <span class="badge" id="hdr-info">加载中…</span>
</header>
<div class="tabs">
  <div class="tab active" data-tab="check">🔍 检测</div>
  <div class="tab" data-tab="words">📚 词库管理</div>
  <div class="tab" data-tab="stats">📊 统计</div>
</div>
<main>
  <!-- 检测 -->
  <section class="panel active" id="panel-check">
    <div class="card">
      <h3>敏感词检测</h3>
      <textarea id="check-text" placeholder="输入要检测的文本…"></textarea>
      <div class="row"><button onclick="doCheck()">检测</button>
        <span class="muted" id="check-hint"></span></div>
      <div id="check-result"></div>
    </div>
  </section>

  <!-- 词库管理 -->
  <section class="panel" id="panel-words">
    <div class="card" id="token-card" style="display:none;border-color:#eab308">
      <div class="row" style="margin:0">
        <span>🔑</span>
        <div style="flex:1;min-width:180px"><label class="muted">写操作令牌 (新增/修改/删除需要)</label>
          <input id="w-token" type="password" placeholder="输入令牌…"></div>
        <button class="sm" onclick="verifyToken()">验证并保存</button>
        <span id="token-state" class="muted"></span>
      </div>
    </div>
    <div class="card">
      <h3 id="word-form-title">➕ 新增词条</h3>
      <div class="row">
        <div style="flex:1;min-width:160px"><label class="muted">敏感词(逗号分隔可多个)</label><input id="w-word" placeholder="如: 大雷,小雷"></div>
        <div style="flex:1;min-width:160px"><label class="muted">等级(逗号分隔,可多个)</label><input id="w-levels" placeholder="如: bilibili,引流"></div>
        <div style="flex:1;min-width:160px"><label class="muted">备注(逗号分隔,可多个)</label><input id="w-remarks" placeholder="如: 引流站点"></div>
      </div>
      <div class="row">
        <button onclick="saveWord()" id="w-save-btn">保存</button>
        <button class="ghost" onclick="resetWordForm()">清空</button>
        <button class="ghost sm" onclick="reload()">↻ 从文件重载</button>
      </div>
    </div>
    <div class="card">
      <div class="row" style="justify-content:space-between">
        <h3 style="margin:0">词库列表 (<span id="w-count">0</span>)</h3>
        <input id="w-filter" placeholder="筛选…" style="max-width:200px" oninput="renderWords()">
      </div>
      <table><thead><tr><th>敏感词</th><th>等级</th><th>备注</th><th style="width:120px">操作</th></tr></thead>
      <tbody id="w-tbody"></tbody></table>
    </div>
  </section>

  <!-- 统计 -->
  <section class="panel" id="panel-stats">
    <div class="card">
      <div class="row" style="justify-content:space-between">
        <h3 style="margin:0">运行统计</h3>
        <div><button class="ghost sm" onclick="loadStats()">↻ 刷新</button>
          <button class="danger sm" onclick="resetStats()">清零</button></div>
      </div>
      <div class="stat-grid" id="stat-boxes"></div>
    </div>
    <div class="card">
      <h3>🔥 触发最多的敏感词</h3>
      <table><thead><tr><th style="width:60px">#</th><th>敏感词</th><th style="width:160px">命中次数</th></tr></thead>
      <tbody id="stat-tbody"></tbody></table>
      <p class="muted" id="stat-empty" style="display:none">暂无命中记录，去「检测」页试试。</p>
    </div>
  </section>
</main>
<div class="toast" id="toast"></div>

<script>
const $ = s => document.querySelector(s);
function toast(msg, err){ const t=$('#toast'); t.textContent=msg; t.className='toast show'+(err?' err':''); setTimeout(()=>t.className='toast',2200); }
async function api(path, opts){
  const r = await fetch(path, opts);
  const j = await r.json().catch(()=>({code:r.status,message:'非JSON响应'}));
  if(j.code!==200) throw new Error(j.message||('HTTP '+r.status));
  return j.data;
}
// Tab 切换
document.querySelectorAll('.tab').forEach(t=>t.onclick=()=>{
  document.querySelectorAll('.tab').forEach(x=>x.classList.remove('active'));
  document.querySelectorAll('.panel').forEach(x=>x.classList.remove('active'));
  t.classList.add('active'); $('#panel-'+t.dataset.tab).classList.add('active');
  if(t.dataset.tab==='words') loadWords();
  if(t.dataset.tab==='stats') loadStats();
});
function lvlClass(l){ l=l.toLowerCase(); if(l==='high')return'lvl-high'; if(l==='medium')return'lvl-mid'; if(l==='low')return'lvl-low'; return''; }

// ---- 令牌 (写操作鉴权) ----
let TOKEN = localStorage.getItem('nb_token') || '';
let AUTH_REQUIRED = false;
// 给写请求附加令牌头
function authHeaders(extra){ const h = Object.assign({}, extra||{}); if(TOKEN) h['X-Auth-Token']=TOKEN; return h; }
// 查询服务端是否启用鉴权, 决定是否显示令牌框
async function checkAuth(){
  try{ const d=await api('/auth/status'); AUTH_REQUIRED=!!d.required;
    $('#token-card').style.display = AUTH_REQUIRED ? 'block' : 'none';
    if(AUTH_REQUIRED){ $('#w-token').value=TOKEN; if(TOKEN) $('#token-state').textContent='(已保存)'; }
  }catch(e){}
}
async function verifyToken(){
  TOKEN=$('#w-token').value.trim(); localStorage.setItem('nb_token',TOKEN);
  try{ await api('/auth/verify',{method:'POST',headers:authHeaders()}); $('#token-state').innerHTML='<span style="color:#a3e635">✅ 有效, 已保存</span>'; toast('令牌有效'); }
  catch(e){ $('#token-state').innerHTML='<span style="color:#f87171">❌ '+esc(e.message)+'</span>'; }
}

// ---- 检测 ----
async function doCheck(){
  const text = $('#check-text').value;
  try{
    const d = await api('/check',{method:'POST',headers:authHeaders({'Content-Type':'application/json'}),body:JSON.stringify({text})});
    renderCheck(text, d);
  }catch(e){ toast(e.message,true); }
}
function actionLabel(action){
  return {pass:'\u901a\u8fc7',review:'\u4eba\u5de5\u590d\u6838',block:'\u62e6\u622a'}[action]||action;
}
function renderModelResults(d){
  if(d.model_error) return '<div class="card" style="border-color:#ef4444"><b>AI \u6a21\u578b\u670d\u52a1\u6682\u65f6\u4e0d\u53ef\u7528</b><div class="muted">'+esc(d.model_error)+'</div></div>';
  const models=d.model_results||[];
  if(!models.length) return '<div class="card"><span class="muted">AI \u6a21\u578b\u672a\u542f\u7528\u3002</span></div>';
  const cards=models.map(m=>{
    const pct=(Number(m.sexual_harm_probability||0)*100).toFixed(2)+'%';
    const gate=(Number(m.semantic_gate||0)*100).toFixed(1)+'%';
    const name=m.model==='macbert'?'MacBERT':'Lite BiGRU';
    const rules=(m.rule_hits||[]).length?'<div class="muted" style="margin-top:8px">Rule: '+esc(m.rule_hits.join(', '))+'</div>':'';
    return '<div class="model-card"><h4><span>'+name+'</span><span class="action action-'+esc(m.action)+'">'+actionLabel(m.action)+'</span></h4>'+
      '<div class="model-score">'+pct+'</div>'+
      '<div class="model-meta"><span>\u8bed\u4e49\u95e8\u63a7 '+gate+'</span><span>\u8017\u65f6 '+Number(m.latency_ms||0).toFixed(1)+' ms</span>'+
      '<span>Pass &lt; '+Number(m.pass_threshold||0).toFixed(2)+'</span><span>Block &ge; '+Number(m.block_threshold||0).toFixed(2)+'</span></div>'+rules+'</div>';
  }).join('');
  return '<div class="card"><div class="row" style="justify-content:space-between;margin-top:0"><h3 style="margin:0">AI \u53cc\u6a21\u578b\u7ed3\u679c</h3>'+
    '<span class="muted">'+esc(d.model_device||'cpu')+' ? '+(d.models_parallel?'\u5e76\u884c':'\u4e32\u884c')+' ? '+Number(d.model_latency_ms||0).toFixed(1)+' ms</span></div>'+
    '<div class="model-grid">'+cards+'</div><div class="muted">\u7efc\u5408\u5efa\u8bae: <span class="action action-'+esc(d.combined_action)+'">'+actionLabel(d.combined_action)+'</span></div></div>';
}
function renderCheck(text, d){
  const box = $('#check-result');
  const modelHTML=renderModelResults(d);
  let keywordHTML='';
  if(!d.has_sensitive_word){
    keywordHTML='<div class="card" style="background:#0f172a"><p class="muted">\u2705 \u8bcd\u5e93\u672a\u68c0\u6d4b\u5230\u654f\u611f\u8bcd\u3002</p></div>';
  }else{
    const chars=[...text]; const mark=new Array(chars.length).fill(false);
    d.matches.forEach(m=>{ for(let i=m.position.start;i<m.position.end;i++) mark[i]=true; });
    let highlighted=''; chars.forEach((c,i)=>{ highlighted+= mark[i]?'<span class="hl">'+esc(c)+'</span>':esc(c); });
    const rows=d.matches.map(m=>'<tr><td><b>'+esc(m.word)+'</b></td><td>'+m.levels.map(l=>'<span class="tag '+lvlClass(l)+'">'+esc(l)+'</span>').join('')+'</td><td>'+
      (m.remarks.length?m.remarks.map(r=>'<span class="tag remark">'+esc(r)+'</span>').join(''):'<span class="muted">\u2014</span>')+'</td><td class="muted">['+m.position.start+','+m.position.end+')</td></tr>').join('');
    keywordHTML='<div class="card" style="background:#0f172a"><h3>\u8bcd\u5e93\u547d\u4e2d</h3><div style="line-height:1.9;margin-bottom:12px">'+highlighted+'</div>'+
      '<table><thead><tr><th>\u547d\u4e2d\u8bcd</th><th>\u7b49\u7ea7</th><th>\u5907\u6ce8</th><th>\u4f4d\u7f6e</th></tr></thead><tbody>'+rows+'</tbody></table></div>';
  }
  box.innerHTML=modelHTML+keywordHTML;
  const modelCount=(d.model_results||[]).length;
  $('#check-hint').textContent='AI '+modelCount+' \u4e2a\u6a21\u578b ? \u8bcd\u5e93\u547d\u4e2d '+(d.matches||[]).length+' \u5904';
}

// ---- 词库 ----
let ALL_WORDS=[];
let RENDERED_WORDS=[];
async function loadWords(){
  checkAuth(); // 决定是否显示令牌框
  try{ const d=await api('/words'); ALL_WORDS=d.words||[]; $('#w-count').textContent=d.count; renderWords(); }
  catch(e){ toast(e.message,true); }
}
function renderWords(){
  const f=$('#w-filter').value.trim();
  RENDERED_WORDS = f? ALL_WORDS.filter(w=>w.word.includes(f)||w.levels.join().includes(f)) : ALL_WORDS;
  $('#w-tbody').innerHTML = RENDERED_WORDS.map((w,i)=>'<tr>'+
    '<td><b>'+esc(w.word)+'</b></td>'+
    '<td>'+w.levels.map(l=>'<span class="tag '+lvlClass(l)+'">'+esc(l)+'</span>').join('')+'</td>'+
    '<td>'+(w.remarks.length?w.remarks.map(r=>'<span class="tag remark">'+esc(r)+'</span>').join(''):'<span class="muted">&mdash;</span>')+'</td>'+
    '<td><button class="ghost sm" data-word-action="edit" data-word-index="'+i+'">&#25913;</button> '+
    '<button class="danger sm" data-word-action="delete" data-word-index="'+i+'">&#21024;</button></td></tr>').join('');
}
$('#w-tbody').addEventListener('click',e=>{
  const btn=e.target.closest('button[data-word-action]');
  if(!btn)return;
  const w=RENDERED_WORDS[Number(btn.dataset.wordIndex)];
  if(!w)return;
  if(btn.dataset.wordAction==='edit')editWord(w);
  else if(btn.dataset.wordAction==='delete')delWord(w.word);
});
let EDITING=null;
function editWord(w){
  EDITING=w.word; $('#w-word').value=w.word;
  $('#w-levels').value=w.levels.join(','); $('#w-remarks').value=w.remarks.join(',');
  $('#word-form-title').textContent='✏️ 编辑词条: '+w.word; $('#w-save-btn').textContent='更新';
  window.scrollTo({top:0,behavior:'smooth'});
}
function resetWordForm(){ EDITING=null; $('#w-word').value='';
  $('#w-levels').value='';$('#w-remarks').value=''; $('#word-form-title').textContent='➕ 新增词条'; $('#w-save-btn').textContent='保存'; }
function splitComma(s){ return s.split(/[,，]/).map(x=>x.trim()).filter(Boolean); }
async function saveWord(){
  const raw=$('#w-word').value.trim();
  const word=splitComma(raw).join(','); // 与后端 NormalizeWord 一致的清洗
  if(!word){ toast('请填写敏感词',true); return; }
  const body={word, levels:splitComma($('#w-levels').value), remarks:splitComma($('#w-remarks').value)};
  const jsonHdr=authHeaders({'Content-Type':'application/json'});
  try{
    if(!EDITING){
      await api('/words',{method:'POST',headers:jsonHdr,body:JSON.stringify(body)}); toast('已新增');
    } else if(EDITING===word){
      // 敏感词没变, 只改等级/备注 -> 直接更新
      await api('/words/'+encodeURIComponent(EDITING),{method:'PUT',headers:jsonHdr,body:JSON.stringify(body)}); toast('已更新');
    } else {
      // 敏感词变了 = 重命名: 先建新词条, 成功后再删旧的 (先建后删, 失败时旧词条完好)
      await api('/words',{method:'POST',headers:jsonHdr,body:JSON.stringify(body)});
      await api('/words/'+encodeURIComponent(EDITING),{method:'DELETE',headers:authHeaders()});
      toast('已更新 (敏感词已变更)');
    }
    resetWordForm(); loadWords();
  }catch(e){ toast(e.message,true); }
}
async function delWord(word){
  if(!confirm('确定删除 "'+word+'" ?')) return;
  try{ await api('/words/'+encodeURIComponent(word),{method:'DELETE',headers:authHeaders()}); toast('已删除'); loadWords(); }
  catch(e){ toast(e.message,true); }
}
async function reload(){ try{ const d=await api('/reload',{method:'POST',headers:authHeaders()}); toast('已从文件重载 '+d.word_count+' 词'); loadWords(); }catch(e){ toast(e.message,true);} }

// ---- 统计 ----
async function loadStats(){
  try{ const d=await api('/stats?top=30');
    $('#stat-boxes').innerHTML=[
      ['检测请求数',d.check_requests],['命中请求数',d.hit_requests],
      ['累计命中词次',d.total_matches],['不同命中词',d.distinct_words]
    ].map(([l,n])=>'<div class="stat-box"><div class="num">'+n+'</div><div class="lbl">'+l+'</div></div>').join('');
    const tb=$('#stat-tbody');
    if(!d.top_words||!d.top_words.length){ tb.innerHTML=''; $('#stat-empty').style.display='block'; }
    else{ $('#stat-empty').style.display='none';
      tb.innerHTML=d.top_words.map((w,i)=>'<tr><td>'+(i+1)+'</td><td><b>'+esc(w.word)+'</b></td><td>'+w.count+'</td></tr>').join(''); }
  }catch(e){ toast(e.message,true); }
}
async function resetStats(){ if(!confirm('清零所有统计?'))return; try{ await api('/stats/reset',{method:'POST',headers:authHeaders()}); toast('已清零'); loadStats(); }catch(e){ toast(e.message,true);} }

function esc(s){ return String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
// 初始化
(async()=>{ try{ const h=await api('/health'); $('#hdr-info').textContent='词条 '+h.word_count+' · 等级 '+h.levels.length+' 种'; }catch(e){} })();
</script>
</body>
</html>`
