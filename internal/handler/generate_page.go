package handler

// generatePageHTML 是 /new 的前端:輸入提示詞(可選附文件)→ 打 /api/generate → 輪詢 → 顯示網址。
// 單檔自包含、CSS/JS inline,配上 favicon(內嵌 SVG,分頁不是預設地球)。
const generatePageHTML = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>AI 生成網站 · wez-html</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='14' fill='%23e11d48'/%3E%3Cpath d='M20 24l-8 8 8 8M44 24l8 8-8 8M36 18L28 46' stroke='white' stroke-width='4' fill='none' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E">
<style>
  :root { --red:#e11d48; --ink:#1f2937; --muted:#6b7280; --line:#e5e7eb; --bg:#f8fafc; }
  * { box-sizing: border-box; }
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans TC",sans-serif;
         background:var(--bg); color:var(--ink); line-height:1.6; }
  .wrap { max-width:760px; margin:0 auto; padding:40px 20px 80px; }
  header { display:flex; align-items:center; gap:12px; margin-bottom:8px; }
  header .logo { width:40px; height:40px; border-radius:10px; background:var(--red); color:#fff;
                 display:grid; place-items:center; font-weight:700; font-size:20px; }
  h1 { font-size:26px; margin:0; }
  .sub { color:var(--muted); margin:4px 0 28px; }
  .card { background:#fff; border:1px solid var(--line); border-radius:14px; padding:24px;
          box-shadow:0 1px 3px rgba(0,0,0,.04); }
  label { display:block; font-weight:600; font-size:14px; margin:16px 0 6px; }
  label:first-child { margin-top:0; }
  .hint { color:var(--muted); font-weight:400; font-size:12px; }
  textarea, input[type=text] { width:100%; border:1px solid var(--line); border-radius:10px;
          padding:11px 13px; font-size:15px; font-family:inherit; color:var(--ink); background:#fff; }
  textarea { min-height:130px; resize:vertical; }
  textarea:focus, input:focus { outline:none; border-color:var(--red); box-shadow:0 0 0 3px rgba(225,29,72,.12); }
  .row { display:flex; gap:14px; }
  .row > div { flex:1; }
  .file { font-size:14px; color:var(--muted); }
  button { margin-top:22px; width:100%; background:var(--red); color:#fff; border:0; border-radius:10px;
           padding:13px; font-size:16px; font-weight:600; cursor:pointer; transition:opacity .15s; }
  button:hover { opacity:.9; }
  button:disabled { opacity:.5; cursor:not-allowed; }
  #status { margin-top:22px; padding:16px; border-radius:10px; font-size:14px; display:none; }
  #status.show { display:block; }
  #status.run { background:#fef3c7; border:1px solid #fde68a; color:#92400e; }
  #status.ok  { background:#dcfce7; border:1px solid #bbf7d0; color:#166534; }
  #status.err { background:#fee2e2; border:1px solid #fecaca; color:#991b1b; }
  #status a { color:inherit; font-weight:700; }
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
    <div class="logo">&lt;/&gt;</div>
    <div>
      <h1>AI 生成網站</h1>
    </div>
  </header>
  <p class="sub">打一段提示詞(可選附一份文件),交給 <code>claude -p</code> 生成一頁 HTML,直接上線到 wez-html。</p>

  <div class="card">
    <form id="f">
      <label>要做什麼樣的網頁?<span class="hint">越具體越好:用途、內容、風格</span></label>
      <textarea id="prompt" placeholder="例:做一頁部門週會的議程頁,深藍配色,列出五個議題與負責人,底部放一個倒數計時器。"></textarea>

      <label>參考文件<span class="hint">選填,.txt / .md / .html,內容會併入提示詞</span></label>
      <input type="file" id="doc" class="file" accept=".txt,.md,.markdown,.html,.htm,.csv,.json">

      <div class="row">
        <div>
          <label>站台名 <span class="hint">網址代號</span></label>
          <input type="text" id="site" placeholder="my-page" pattern="[a-z0-9-]{1,40}">
        </div>
        <div>
          <label>你的代號 <span class="hint">identity</span></label>
          <input type="text" id="identity" placeholder="bob" pattern="[a-zA-Z0-9_-]{1,20}">
        </div>
      </div>

      <label style="display:flex;align-items:center;gap:8px;font-weight:400;margin-top:14px;">
        <input type="checkbox" id="force" style="width:auto;margin:0;"> 站台已存在時直接覆蓋(force)
      </label>

      <button type="submit" id="go">生成並上線</button>
    </form>
    <div id="status"></div>
  </div>

  <p class="foot"><a href="/">← 回站台列表</a></p>
</div>

<script>
(function(){
  var f = document.getElementById('f');
  var go = document.getElementById('go');
  var st = document.getElementById('status');
  var poll = null;

  function show(cls, html){ st.className = 'show ' + cls; st.innerHTML = html; }

  f.addEventListener('submit', function(e){
    e.preventDefault();
    if (poll) { clearInterval(poll); poll = null; }
    var prompt = document.getElementById('prompt').value.trim();
    var site = document.getElementById('site').value.trim();
    var identity = document.getElementById('identity').value.trim();
    if (!prompt) { show('err','請先輸入提示詞'); return; }
    if (!/^[a-z0-9-]{1,40}$/.test(site)) { show('err','站台名格式:小寫英數與 - ,1~40 字'); return; }
    if (!/^[a-zA-Z0-9_-]{1,20}$/.test(identity)) { show('err','代號格式:英數 _ - ,1~20 字'); return; }

    var fd = new FormData();
    fd.append('prompt', prompt);
    fd.append('site', site);
    fd.append('identity', identity);
    if (document.getElementById('force').checked) fd.append('force','1');
    var df = document.getElementById('doc').files[0];
    if (df) fd.append('doc', df);

    go.disabled = true;
    show('run','<span class="spin"></span>已送出,claude 生成中(約 1~2 分鐘,請勿關閉)…');

    fetch('/api/generate', { method:'POST', body:fd })
      .then(function(r){ return r.json().then(function(j){ return {ok:r.ok, s:r.status, j:j}; }); })
      .then(function(res){
        if (res.s === 409) { go.disabled=false; show('err','站台「'+site+'」已被 '+(res.j.existing_uploader||'某人')+' 使用,勾選 force 可覆蓋。'); return; }
        if (!res.ok || !res.j.job) { go.disabled=false; show('err','送出失敗:'+(res.j.error||res.s)); return; }
        startPoll(res.j.job);
      })
      .catch(function(err){ go.disabled=false; show('err','送出失敗:'+err); });
  });

  function startPoll(job){
    poll = setInterval(function(){
      fetch('/api/generate/'+job).then(function(r){ return r.json(); }).then(function(j){
        if (j.status === 'done') {
          clearInterval(poll); poll=null; go.disabled=false;
          show('ok','✅ 已上線!<br><a href="'+j.url+'" target="_blank">'+j.url+'</a> ('+(j.size_bytes||0)+' bytes)');
        } else if (j.status === 'error') {
          clearInterval(poll); poll=null; go.disabled=false;
          show('err','生成失敗:'+(j.error||'unknown'));
        } else {
          show('run','<span class="spin"></span>生成中('+(j.status||'…')+')…請稍候');
        }
      }).catch(function(){ /* 輪詢暫時失敗就下次再試 */ });
    }, 2500);
  }
})();
</script>
</body>
</html>`
