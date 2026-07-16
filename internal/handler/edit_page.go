package handler

// editPageHTML 是 /edit 的前端:選自己的站台 → 預覽 → 兩種改法:
// 🤖 AI 修改(POST /api/refine → 輪詢 /api/generate/<id>)、✍️ 手動編輯(讀回 HTML → POST /api/save-html)。
// 支援 ?site=xxx 直接帶入站台。單檔自包含、CSS/JS inline、配 favicon。
//
// 注意:這是 Go 反引號字串,JS 裡不能用 template literal(反引號),一律單引號串接。
const editPageHTML = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>線上編輯 · wez-html</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='14' fill='%23e11d48'/%3E%3Cpath d='M40 14l10 10-24 24-13 3 3-13z' stroke='white' stroke-width='4' fill='none' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E">
<style>
  :root { --red:#e11d48; --ink:#1f2937; --muted:#6b7280; --line:#e5e7eb; --bg:#f8fafc; }
  * { box-sizing: border-box; }
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans TC",sans-serif;
         background:var(--bg); color:var(--ink); line-height:1.6; }
  .wrap { max-width:1080px; margin:0 auto; padding:40px 20px 80px; }
  header { display:flex; align-items:center; gap:12px; margin-bottom:8px; }
  header .logo { width:40px; height:40px; border-radius:10px; background:var(--red); color:#fff;
                 display:grid; place-items:center; font-weight:700; font-size:20px; }
  h1 { font-size:26px; margin:0; }
  .sub { color:var(--muted); margin:4px 0 28px; }
  .card { background:#fff; border:1px solid var(--line); border-radius:14px; padding:24px;
          box-shadow:0 1px 3px rgba(0,0,0,.04); }
  label { display:block; font-weight:600; font-size:14px; margin:16px 0 6px; }
  .hint { color:var(--muted); font-weight:400; font-size:12px; }
  textarea, input[type=text], select { width:100%; border:1px solid var(--line); border-radius:10px;
          padding:11px 13px; font-size:15px; font-family:inherit; color:var(--ink); background:#fff; }
  textarea { min-height:100px; resize:vertical; }
  textarea:focus, input:focus, select:focus { outline:none; border-color:var(--red); box-shadow:0 0 0 3px rgba(225,29,72,.12); }
  #htmlsrc { min-height:340px; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:13px; line-height:1.5; }
  .row { display:flex; gap:14px; align-items:flex-end; }
  .row > div { flex:1; }
  button.main { background:var(--red); color:#fff; border:0; border-radius:10px;
           padding:12px 18px; font-size:15px; font-weight:600; cursor:pointer; transition:opacity .15s; }
  button.main:hover { opacity:.9; }
  button.main:disabled { opacity:.5; cursor:not-allowed; }
  .btn2 { background:#fff; border:1px solid var(--line); border-radius:10px; padding:10px 14px;
          font-size:14px; font-weight:600; color:var(--ink); cursor:pointer; white-space:nowrap; }
  .btn2:hover { border-color:var(--red); color:var(--red); }
  .btn2:disabled { opacity:.5; cursor:not-allowed; }
  /* 編輯區:左控制右預覽 */
  #editor { display:none; }
  #editor.show { display:block; }
  .split { display:flex; gap:20px; margin-top:20px; }
  .split .ctrl { flex:0 0 44%; min-width:320px; }
  .split .pv { flex:1; display:flex; flex-direction:column; }
  @media (max-width:860px) { .split { flex-direction:column; } .split .ctrl { flex:1; min-width:0; } }
  .pvbar { display:flex; align-items:center; gap:10px; margin-bottom:8px; }
  .pvbar .ttl { font-weight:600; font-size:14px; }
  .pvbar a { color:var(--red); font-size:13px; text-decoration:none; margin-left:auto; }
  iframe { width:100%; flex:1; min-height:420px; border:1px solid var(--line); border-radius:10px; background:#fff; }
  .tabs { display:flex; gap:8px; margin-bottom:14px; }
  .tab { flex:1; padding:10px 6px; border:1px solid var(--line); border-radius:10px; background:#fff;
         font-size:14px; font-weight:600; color:var(--muted); cursor:pointer; text-align:center; }
  .tab.on { border-color:var(--red); color:var(--red); background:rgba(225,29,72,.05); }
  .sec { display:none; }
  .sec.on { display:block; }
  #status { margin-top:16px; padding:14px 16px; border-radius:10px; font-size:14px; display:none; }
  #status.show { display:block; }
  #status.run { background:#fef3c7; border:1px solid #fde68a; color:#92400e; }
  #status.ok  { background:#dcfce7; border:1px solid #bbf7d0; color:#166534; }
  #status.err { background:#fee2e2; border:1px solid #fecaca; color:#991b1b; }
  .spin { display:inline-block; width:14px; height:14px; border:2px solid currentColor;
          border-top-color:transparent; border-radius:50%; animation:s .7s linear infinite; vertical-align:-2px; margin-right:6px; }
  @keyframes s { to { transform:rotate(360deg); } }
  .foot { text-align:center; color:var(--muted); font-size:13px; margin-top:26px; }
  .foot a { color:var(--red); text-decoration:none; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <div class="logo">✏️</div>
    <div><h1>線上編輯我的網站</h1></div>
  </header>
  <p class="sub">選自己上傳/生成的站台,用 AI 下修改指示,或直接改 HTML 原始碼。只有原上傳者代號能改。</p>

  <div class="card">
    <div class="row">
      <div>
        <label style="margin-top:0">你的代號<span class="hint">上傳/生成時用的 identity</span></label>
        <input type="text" id="identity" maxlength="20" placeholder="例:bob">
      </div>
      <div>
        <label style="margin-top:0">站台</label>
        <select id="site"><option value="">先輸入代號載入站台…</option></select>
      </div>
      <div style="flex:0 0 auto">
        <button type="button" class="btn2" id="load">🔄 載入我的站台</button>
      </div>
    </div>

    <div id="editor">
      <div class="split">
        <div class="ctrl">
          <div class="tabs">
            <button type="button" class="tab on" data-mode="ai">🤖 AI 修改</button>
            <button type="button" class="tab" data-mode="manual">✍️ 手動編輯</button>
          </div>
          <div class="sec on" id="sec-ai">
            <label style="margin-top:0">想怎麼改?<span class="hint">AI 會在現有頁面上迭代修改</span></label>
            <textarea id="aiprompt" placeholder="例:標題改成深藍色,底部加一個回到頂部的按鈕。"></textarea>
            <button type="button" class="main" id="aibtn" style="margin-top:14px; width:100%">🚀 送出修改</button>
          </div>
          <div class="sec" id="sec-manual">
            <label style="margin-top:0">HTML 原始碼<span class="hint">整份覆蓋 index.html</span></label>
            <textarea id="htmlsrc" spellcheck="false" placeholder="按下面「載入目前 HTML」讀回原始碼…"></textarea>
            <div class="row" style="margin-top:14px">
              <button type="button" class="btn2" id="loadhtml" style="flex:1">📥 載入目前 HTML</button>
              <button type="button" class="main" id="savebtn" style="flex:1">💾 儲存並上線</button>
            </div>
          </div>
          <div id="status"></div>
        </div>
        <div class="pv">
          <div class="pvbar">
            <span class="ttl">即時預覽</span>
            <button type="button" class="btn2" id="pvre" style="padding:6px 10px; font-size:12px">↻ 重新整理</button>
            <a id="pvopen" href="#" target="_blank">開新視窗 ↗</a>
          </div>
          <iframe id="pv" title="預覽"></iframe>
        </div>
      </div>
    </div>
  </div>

  <p class="foot"><a href="/">← 回站台清單</a> · <a href="/new">➕ AI 生成新網站</a></p>
</div>
<script>
(function(){
  function q(id){ return document.getElementById(id); }
  var idIn = q('identity'), siteSel = q('site'), editor = q('editor'), pv = q('pv');
  var params = new URLSearchParams(location.search);
  var wantSite = params.get('site') || '';
  var polling = null;

  idIn.value = localStorage.getItem('wez-edit-id') || '';

  function show(cls, html){
    var st = q('status');
    st.className = 'show ' + cls;
    st.innerHTML = html;
  }
  function hideStatus(){ q('status').className = ''; }

  function curSite(){ return siteSel.value; }
  function refreshPv(){
    var s = curSite();
    if (!s) return;
    pv.src = '/' + s + '/?_=' + Date.now();
    q('pvopen').href = '/' + s + '/';
  }

  function loadSites(){
    var who = idIn.value.trim();
    if (!who) { show('err', '請先輸入代號'); return; }
    localStorage.setItem('wez-edit-id', who);
    hideStatus();
    fetch('/api/sites').then(function(r){ return r.json(); }).then(function(j){
      var mine = (j.sites || []).filter(function(x){ return x.uploader === who; });
      siteSel.innerHTML = '';
      if (!mine.length) {
        siteSel.innerHTML = '<option value="">(這個代號沒有站台)</option>';
        editor.className = '';
        show('err', '代號 ' + who + ' 名下沒有站台。到 <a href="/new">/new</a> 先生成一個,或確認代號拼字。');
        return;
      }
      mine.forEach(function(x){
        var o = document.createElement('option');
        o.value = x.name;
        o.textContent = x.name + (x.project_name ? '(' + x.project_name + ')' : '');
        siteSel.appendChild(o);
      });
      if (wantSite && mine.some(function(x){ return x.name === wantSite; })) siteSel.value = wantSite;
      editor.className = 'show';
      q('htmlsrc').value = '';
      refreshPv();
    }).catch(function(e){ show('err', '載入站台失敗:' + e); });
  }

  q('load').addEventListener('click', loadSites);
  siteSel.addEventListener('change', function(){ q('htmlsrc').value = ''; hideStatus(); refreshPv(); });
  q('pvre').addEventListener('click', refreshPv);

  // tabs
  document.querySelectorAll('.tab').forEach(function(t){
    t.addEventListener('click', function(){
      document.querySelectorAll('.tab').forEach(function(x){ x.className = 'tab'; });
      t.className = 'tab on';
      var m = t.getAttribute('data-mode');
      q('sec-ai').className = 'sec' + (m === 'ai' ? ' on' : '');
      q('sec-manual').className = 'sec' + (m === 'manual' ? ' on' : '');
    });
  });

  // ---- AI 修改:POST /api/refine → 輪詢 ----
  q('aibtn').addEventListener('click', function(){
    var s = curSite(), who = idIn.value.trim(), p = q('aiprompt').value.trim();
    if (!s) { show('err', '先選站台'); return; }
    if (!p) { show('err', '先寫要怎麼改'); return; }
    var btn = q('aibtn');
    btn.disabled = true;
    show('run', '<span class="spin"></span>已送出,AI 修改中…約 1~2 分鐘,別關頁面。');
    var fd = new FormData();
    fd.append('prompt', p);
    fd.append('site', s);
    fd.append('identity', who);
    fetch('/api/refine', { method: 'POST', body: fd })
      .then(function(r){ return r.json().then(function(j){ return { ok: r.ok, j: j }; }); })
      .then(function(res){
        if (!res.ok || !res.j.job) {
          btn.disabled = false;
          show('err', '送出失敗:' + (res.j.error || JSON.stringify(res.j)));
          return;
        }
        if (polling) clearInterval(polling);
        polling = setInterval(function(){
          fetch('/api/generate/' + res.j.job).then(function(r){ return r.json(); }).then(function(j){
            if (j.status === 'done') {
              clearInterval(polling); polling = null;
              btn.disabled = false;
              q('aiprompt').value = '';
              q('htmlsrc').value = '';
              refreshPv();
              show('ok', '✅ 改好了!預覽已更新。不滿意可以再下一輪指示。');
            } else if (j.status === 'error') {
              clearInterval(polling); polling = null;
              btn.disabled = false;
              show('err', '修改失敗:' + (j.error || '未知錯誤'));
            }
          }).catch(function(){ /* 單次輪詢失敗容忍,下一輪再試 */ });
        }, 2500);
      })
      .catch(function(e){ btn.disabled = false; show('err', '送出失敗:' + e); });
  });

  // ---- 手動編輯:讀回 HTML / POST /api/save-html ----
  q('loadhtml').addEventListener('click', function(){
    var s = curSite();
    if (!s) { show('err', '先選站台'); return; }
    fetch('/' + s + '/', { cache: 'no-store' }).then(function(r){
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.text();
    }).then(function(t){
      q('htmlsrc').value = t;
      hideStatus();
    }).catch(function(e){ show('err', '讀取失敗:' + e); });
  });

  q('savebtn').addEventListener('click', function(){
    var s = curSite(), who = idIn.value.trim(), html = q('htmlsrc').value;
    if (!s) { show('err', '先選站台'); return; }
    if (!html.trim()) { show('err', '內容是空的 — 先「載入目前 HTML」再改'); return; }
    var btn = q('savebtn');
    btn.disabled = true;
    show('run', '<span class="spin"></span>儲存中…');
    fetch('/api/save-html', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: s, identity: who, html: html })
    }).then(function(r){ return r.json().then(function(j){ return { ok: r.ok, j: j }; }); })
      .then(function(res){
        btn.disabled = false;
        if (!res.ok) { show('err', '儲存失敗:' + (res.j.error || JSON.stringify(res.j))); return; }
        refreshPv();
        show('ok', '✅ 已儲存上線,預覽已更新。');
      })
      .catch(function(e){ btn.disabled = false; show('err', '儲存失敗:' + e); });
  });

  // 帶 ?site= 進來且已有代號 → 自動載入
  if (idIn.value) loadSites();
})();
</script>
</body>
</html>`
