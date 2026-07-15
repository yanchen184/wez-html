package handler

// generatePageHTML 是 /new 的前端:三個入口(空白生成 / 從模板 / 從文件)
// → 打 /api/generate → 輪詢 → 顯示網址 → 不滿意可繼續 refine。
// 單檔自包含、CSS/JS inline,配上 favicon(內嵌 SVG,分頁不是預設地球)。
//
// 注意:這是 Go 反引號字串,JS 裡不能用 template literal(反引號),一律單引號串接。
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
  .hint { color:var(--muted); font-weight:400; font-size:12px; }
  textarea, input[type=text] { width:100%; border:1px solid var(--line); border-radius:10px;
          padding:11px 13px; font-size:15px; font-family:inherit; color:var(--ink); background:#fff; }
  textarea { min-height:120px; resize:vertical; }
  textarea:focus, input:focus { outline:none; border-color:var(--red); box-shadow:0 0 0 3px rgba(225,29,72,.12); }
  .row { display:flex; gap:14px; }
  .row > div { flex:1; }
  .file { font-size:14px; color:var(--muted); }
  button.main { margin-top:22px; width:100%; background:var(--red); color:#fff; border:0; border-radius:10px;
           padding:13px; font-size:16px; font-weight:600; cursor:pointer; transition:opacity .15s; }
  button.main:hover { opacity:.9; }
  button.main:disabled { opacity:.5; cursor:not-allowed; }
  /* 三入口頁籤 */
  .tabs { display:flex; gap:8px; margin-bottom:18px; }
  .tab { flex:1; padding:10px 6px; border:1px solid var(--line); border-radius:10px; background:#fff;
         font-size:14px; font-weight:600; color:var(--muted); cursor:pointer; text-align:center; }
  .tab.on { border-color:var(--red); color:var(--red); background:rgba(225,29,72,.05); }
  .sec { display:none; }
  .sec.on { display:block; }
  /* 模板卡 */
  .cards { display:grid; grid-template-columns:repeat(3,1fr); gap:10px; }
  @media (max-width:560px) { .cards { grid-template-columns:repeat(2,1fr); } }
  .tcard { border:1px solid var(--line); border-radius:10px; padding:12px 10px; background:#fff;
           cursor:pointer; text-align:center; transition:border-color .1s; }
  .tcard:hover { border-color:#f9a8b8; }
  .tcard.sel { border-color:var(--red); background:rgba(225,29,72,.05); box-shadow:0 0 0 2px rgba(225,29,72,.15); }
  .tcard .ic { font-size:22px; }
  .tcard .nm { font-weight:600; font-size:13px; margin-top:4px; }
  .tcard .ds { color:var(--muted); font-size:11px; line-height:1.4; margin-top:2px; }
  #status { margin-top:22px; padding:16px; border-radius:10px; font-size:14px; display:none; }
  #status.show { display:block; }
  #status.run { background:#fef3c7; border:1px solid #fde68a; color:#92400e; }
  #status.ok  { background:#dcfce7; border:1px solid #bbf7d0; color:#166534; }
  #status.err { background:#fee2e2; border:1px solid #fecaca; color:#991b1b; }
  #status a { color:inherit; font-weight:700; }
  .spin { display:inline-block; width:14px; height:14px; border:2px solid currentColor;
          border-top-color:transparent; border-radius:50%; animation:s .7s linear infinite; vertical-align:-2px; margin-right:6px; }
  @keyframes s { to { transform:rotate(360deg); } }
  #refine { display:none; margin-top:24px; padding-top:22px; border-top:1px dashed var(--line); }
  #refine.show { display:block; }
  #refine textarea { min-height:90px; }
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
  <p class="sub">選一個入口,交給 <code>claude -p</code> 生成一頁 HTML,直接上線到 wez-html。</p>

  <div class="card">
    <div class="tabs">
      <button type="button" class="tab on" data-mode="blank">✍️ 空白生成</button>
      <button type="button" class="tab" data-mode="tpl">🧩 從模板</button>
      <button type="button" class="tab" data-mode="doc">📄 從文件</button>
    </div>

    <form id="f">
      <div class="sec on" id="sec-blank">
        <label>要做什麼樣的網頁?<span class="hint">越具體越好:用途、內容、風格</span></label>
        <textarea id="prompt" placeholder="例:做一頁部門週會的議程頁,深藍配色,列出五個議題與負責人,底部放一個倒數計時器。"></textarea>
      </div>

      <div class="sec" id="sec-tpl">
        <label style="margin-top:0">選一個模板</label>
        <div class="cards" id="cards"></div>
        <label>你的內容<span class="hint">標題、重點、名單…想放進頁面的東西直接貼</span></label>
        <textarea id="tplcontent" placeholder="例:6/30 部門週會|議題:Q3 目標(Bob)、新人報到(Amy)、系統改版進度(YC)"></textarea>
      </div>

      <div class="sec" id="sec-doc">
        <label style="margin-top:0">上傳文件<span class="hint">Word / Excel / PowerPoint / 純文字,內容會變成網頁</span></label>
        <input type="file" id="doc" class="file" accept=".docx,.xlsx,.pptx,.txt,.md,.markdown,.html,.htm,.csv,.json">
        <label>補充指示<span class="hint">選填,不填就自動排成易讀的網頁</span></label>
        <textarea id="dprompt" style="min-height:80px" placeholder="例:做成對外公告的樣子,重點數字放大。"></textarea>
      </div>

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

      <button type="submit" class="main" id="go">生成並上線</button>
    </form>
    <div id="status"></div>

    <div id="refine">
      <label>不滿意?繼續下提示詞修改<span class="hint">會在現在這一頁上改,不是重畫一頁</span></label>
      <textarea id="rprompt" placeholder="例:標題改成深藍色、把倒數計時器拿掉、議題再多加兩條。"></textarea>
      <button type="button" class="main" id="rgo">套用修改</button>
    </div>
  </div>

  <p class="foot"><a href="/">← 回站台列表</a></p>
</div>

<script>
(function(){
  var TPL = [
    { id:'agenda', ic:'📋', nm:'會議議程', ds:'議題、負責人、時間',
      p:'做一頁會議議程網頁:深藍專業配色,頂部大標放會議名稱與日期,議題用卡片或表格清楚列出(議題、負責人、時間分配),重點議題視覺上突出。' },
    { id:'notice', ic:'📢', nm:'公告通知', ds:'重點條列、生效日期',
      p:'做一頁正式公告網頁:醒目的標題區,內文重點條列、關鍵字加粗,生效日期與聯絡窗口放在顯眼位置,正式但不死板的配色。' },
    { id:'event', ic:'🎉', nm:'活動報名', ds:'亮點、時間地點、報名',
      p:'做一頁活動介紹網頁:活潑吸睛的 hero 區(活動名稱+一句話),往下依序是活動亮點、時間地點、流程表、報名方式,底部放一個到活動日的倒數計時器。' },
    { id:'report', ic:'📊', nm:'數據報表', ds:'數字卡、表格、圖',
      p:'做一頁數據報表網頁:乾淨商務風,頂部放幾張重點數字大卡,中段是資料表格(斑馬紋、可讀性優先),適合的數據用 inline SVG 或純 CSS 畫簡單長條圖,不引用外部圖表庫。' },
    { id:'showcase', ic:'🚀', nm:'專案成果', ds:'亮點、功能、時間軸',
      p:'做一頁專案成果展示網頁:現代感漸層 hero 區(專案名+一句話定位),功能清單用卡片排列,進度用垂直時間軸呈現,整體像產品 landing page。' },
    { id:'manual', ic:'📖', nm:'操作手冊', ds:'步驟、FAQ、目錄',
      p:'做一頁操作手冊網頁:頂部目錄(錨點連結),操作步驟用大編號區塊逐步呈現,常見問題用可折疊的 details/summary,易讀優先、行距寬鬆。' }
  ];

  var f = document.getElementById('f');
  var go = document.getElementById('go');
  var st = document.getElementById('status');
  var refine = document.getElementById('refine');
  var rgo = document.getElementById('rgo');
  var rprompt = document.getElementById('rprompt');
  var poll = null;
  var mode = 'blank';
  var selTpl = null;
  var lastSite = '';      // 生成成功後鎖定的站台,refine 就改這一個
  var lastIdentity = '';

  // 模板卡渲染 + 點選
  var cardsEl = document.getElementById('cards');
  TPL.forEach(function(t){
    var d = document.createElement('div');
    d.className = 'tcard';
    d.innerHTML = '<div class="ic">'+t.ic+'</div><div class="nm">'+t.nm+'</div><div class="ds">'+t.ds+'</div>';
    d.addEventListener('click', function(){
      selTpl = t;
      var all = cardsEl.querySelectorAll('.tcard');
      for (var i=0;i<all.length;i++) all[i].className = 'tcard';
      d.className = 'tcard sel';
    });
    cardsEl.appendChild(d);
  });

  // 頁籤切換
  var tabs = document.querySelectorAll('.tab');
  for (var i=0;i<tabs.length;i++) {
    tabs[i].addEventListener('click', function(){
      mode = this.getAttribute('data-mode');
      for (var j=0;j<tabs.length;j++) tabs[j].className = 'tab' + (tabs[j]===this ? ' on' : '');
      var secs = ['blank','tpl','doc'];
      for (var k=0;k<secs.length;k++) {
        document.getElementById('sec-'+secs[k]).className = 'sec' + (secs[k]===mode ? ' on' : '');
      }
    });
  }

  function show(cls, html){ st.className = 'show ' + cls; st.innerHTML = html; }
  function busy(b){ go.disabled = b; rgo.disabled = b; }
  // 加 cache-bust,避免改完點進去看到瀏覽器快取的舊頁。
  function bust(u){ return u + '?v=' + Date.now(); }

  // 依目前入口組出 prompt;不合法回 null(並自己 show 錯誤)。
  function buildPromptForMode(){
    if (mode === 'blank') {
      var p = document.getElementById('prompt').value.trim();
      if (!p) { show('err','請先輸入提示詞'); return null; }
      return p;
    }
    if (mode === 'tpl') {
      if (!selTpl) { show('err','請先選一個模板'); return null; }
      var c = document.getElementById('tplcontent').value.trim();
      if (!c) { show('err','請貼上你的內容(標題、重點…)'); return null; }
      return selTpl.p + '\n\n我的內容:\n' + c;
    }
    // doc 模式:prompt 給預設指示,重點在附件
    var extra = document.getElementById('dprompt').value.trim();
    return extra || '把這份文件的內容做成一個清楚易讀、排版好看的網頁,重點資訊視覺上突出。';
  }

  f.addEventListener('submit', function(e){
    e.preventDefault();
    if (poll) { clearInterval(poll); poll = null; }
    var site = document.getElementById('site').value.trim();
    var identity = document.getElementById('identity').value.trim();
    var prompt = buildPromptForMode();
    if (prompt === null) return;
    var df = document.getElementById('doc').files[0];
    if (mode === 'doc' && !df) { show('err','請先選擇要上傳的文件'); return; }
    if (!/^[a-z0-9-]{1,40}$/.test(site)) { show('err','站台名格式:小寫英數與 - ,1~40 字'); return; }
    if (!/^[a-zA-Z0-9_-]{1,20}$/.test(identity)) { show('err','代號格式:英數 _ - ,1~20 字'); return; }

    var fd = new FormData();
    fd.append('prompt', prompt);
    fd.append('site', site);
    fd.append('identity', identity);
    if (document.getElementById('force').checked) fd.append('force','1');
    if (mode === 'doc' && df) fd.append('doc', df);

    busy(true);
    show('run','<span class="spin"></span>已送出,claude 生成中(約 1~2 分鐘,請勿關閉)…');

    fetch('/api/generate', { method:'POST', body:fd })
      .then(function(r){ return r.json().then(function(j){ return {ok:r.ok, s:r.status, j:j}; }); })
      .then(function(res){
        if (res.s === 409) { busy(false); show('err','站台「'+site+'」已被 '+(res.j.existing_uploader||'某人')+' 使用,勾選 force 可覆蓋。'); return; }
        if (!res.ok || !res.j.job) { busy(false); show('err','送出失敗:'+(res.j.error||res.s)); return; }
        lastSite = site; lastIdentity = identity;
        startPoll(res.j.job, '生成');
      })
      .catch(function(err){ busy(false); show('err','送出失敗:'+err); });
  });

  rgo.addEventListener('click', function(){
    if (poll) { clearInterval(poll); poll = null; }
    var instruction = rprompt.value.trim();
    if (!instruction) { show('err','請先輸入要改什麼'); return; }
    if (!lastSite) { show('err','請先生成一頁再修改'); return; }

    var fd = new FormData();
    fd.append('prompt', instruction);
    fd.append('site', lastSite);
    fd.append('identity', lastIdentity);

    busy(true);
    show('run','<span class="spin"></span>修改中(約 1~2 分鐘,請勿關閉)…');

    fetch('/api/refine', { method:'POST', body:fd })
      .then(function(r){ return r.json().then(function(j){ return {ok:r.ok, s:r.status, j:j}; }); })
      .then(function(res){
        if (!res.ok || !res.j.job) { busy(false); show('err','送出失敗:'+(res.j.error||res.s)); return; }
        startPoll(res.j.job, '修改');
      })
      .catch(function(err){ busy(false); show('err','送出失敗:'+err); });
  });

  function startPoll(job, verb){
    poll = setInterval(function(){
      fetch('/api/generate/'+job).then(function(r){ return r.json(); }).then(function(j){
        if (j.status === 'done') {
          clearInterval(poll); poll=null; busy(false);
          refine.className = 'show';
          rprompt.value = '';
          show('ok','✅ 已'+verb+'完成並上線!<br><a href="'+bust(j.url)+'" target="_blank">'+j.url+'</a> ('+(j.size_bytes||0)+' bytes)<br>不滿意的話,下面可以繼續下提示詞改。');
        } else if (j.status === 'error') {
          clearInterval(poll); poll=null; busy(false);
          show('err',verb+'失敗:'+(j.error||'unknown'));
        } else {
          show('run','<span class="spin"></span>'+verb+'中('+(j.status||'…')+')…請稍候');
        }
      }).catch(function(){ /* 輪詢暫時失敗就下次再試 */ });
    }, 2500);
  }
})();
</script>
</body>
</html>`
