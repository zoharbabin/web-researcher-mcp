package server

// dashboardHTML is the self-contained operator dashboard page (#87). It has no
// external dependencies (no CDN, no build step), uses a single nonce'd inline
// <style>/<script> pair authorized by the per-request CSP, and polls the
// admin-gated /dashboard/data JSON endpoint. {{NONCE}} and {{VERSION}} are
// substituted per request. All rendering is plain DOM + inline-SVG sparklines —
// no charting library. Aggregate data only; the page can render nothing the
// JSON endpoint does not already expose.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Web Researcher MCP — Operations</title>
<style nonce="{{NONCE}}">
:root{--bg:#0d1117;--card:#161b22;--border:#30363d;--fg:#e6edf3;--muted:#8b949e;--green:#3fb950;--amber:#d29922;--red:#f85149;--accent:#58a6ff}
*{box-sizing:border-box}
body{margin:0;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;background:var(--bg);color:var(--fg)}
header{padding:16px 24px;border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}
header h1{font-size:16px;margin:0;font-weight:600}
header .meta{color:var(--muted);font-size:12px}
main{padding:24px;max-width:1100px;margin:0 auto}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:16px;margin-bottom:24px}
.card{background:var(--card);border:1px solid var(--border);border-radius:8px;padding:16px}
.card h2{font-size:12px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted);margin:0 0 8px}
.kpi{font-size:28px;font-weight:600}
.kpi small{font-size:13px;color:var(--muted);font-weight:400}
.dot{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;vertical-align:middle}
.dot.green{background:var(--green)}.dot.amber{background:var(--amber)}.dot.red{background:var(--red)}.dot.gray{background:var(--muted)}
section{margin-bottom:28px}
section h2{font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted);margin:0 0 12px;border-bottom:1px solid var(--border);padding-bottom:6px}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{text-align:left;padding:8px 10px;border-bottom:1px solid var(--border)}
th{color:var(--muted);font-weight:500}
td.num{text-align:right;font-variant-numeric:tabular-nums}
.tag{display:inline-block;padding:1px 7px;border-radius:10px;font-size:11px;background:#21262d;color:var(--muted)}
.tag.open{background:#3d1416;color:var(--red)}.tag.closed{background:#0f2e16;color:var(--green)}.tag.half-open{background:#3a2e09;color:var(--amber)}
.empty{color:var(--muted);font-style:italic;padding:8px 0}
.err{color:var(--red);margin:0 0 16px}
#login{max-width:380px;margin:80px auto;text-align:center}
#login input{width:100%;padding:10px;margin:12px 0;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--fg);font:inherit}
#login button{padding:10px 18px;background:var(--accent);color:#0d1117;border:0;border-radius:6px;font-weight:600;cursor:pointer}
.hidden{display:none}
svg.spark{vertical-align:middle}
footer{color:var(--muted);font-size:12px;text-align:center;padding:16px}
</style>
</head>
<body>
<div id="login">
<h1>Web Researcher MCP — Operations</h1>
<p style="color:var(--muted)">Enter the admin key to view operational metrics.</p>
<input id="key" type="password" placeholder="X-Admin-Key" autocomplete="current-password">
<div><button id="enter">View dashboard</button></div>
<p id="loginerr" class="err hidden">Invalid admin key.</p>
</div>

<div id="app" class="hidden">
<header>
<h1>Web Researcher MCP — Operations</h1>
<span class="meta">v{{VERSION}} · updated <span id="updated">—</span> · <button id="logout" style="background:none;border:0;color:var(--accent);cursor:pointer;font:inherit">sign out</button></span>
</header>
<main>
<div class="grid">
<div class="card"><h2>Overall Health</h2><div class="kpi"><span id="healthDot" class="dot gray"></span><span id="healthStatus">—</span></div></div>
<div class="card"><h2>Active Sessions</h2><div class="kpi" id="sessions">—</div></div>
<div class="card"><h2>Total Tool Calls</h2><div class="kpi" id="totalCalls">—</div></div>
<div class="card"><h2>Error Rate</h2><div class="kpi" id="errRate">—</div></div>
</div>

<section>
<h2>Tools</h2>
<table><thead><tr><th>Tool</th><th class="num">Calls</th><th class="num">Errors</th><th class="num">Cache hits</th><th class="num">Avg ms</th><th class="num">p95 ms</th></tr></thead>
<tbody id="toolsBody"></tbody></table>
<div id="toolsEmpty" class="empty hidden">No tool calls recorded yet.</div>
</section>

<section>
<h2>Provider Health</h2>
<table><thead><tr><th>Provider</th><th>Type</th><th>Breaker</th><th>Available</th></tr></thead>
<tbody id="healthBody"></tbody></table>
<div id="healthEmpty" class="empty hidden">Multi-provider routing is not enabled — no breaker ladder to observe.</div>
</section>

<section>
<h2>Rate Limits</h2>
<table><tbody id="rateBody"></tbody></table>
</section>

<section>
<h2>Recent Errors</h2>
<table><thead><tr><th>Time</th><th>Tool</th><th>Kind</th><th>Provider</th><th>Cause (redacted)</th></tr></thead>
<tbody id="errorsBody"></tbody></table>
<div id="errorsEmpty" class="empty hidden">No recent errors.</div>
</section>
</main>
<footer>Aggregate operational data only — no per-user, per-query, or tenant-identifiable information.</footer>
</div>

<script nonce="{{NONCE}}">
(function(){
  "use strict";
  var KEY_STORE="wrmcp_admin_key", POLL_MS=10000, timer=null;
  var $=function(id){return document.getElementById(id)};
  function txt(el,v){el.textContent=(v==null?"—":String(v))}
  function getKey(){try{return sessionStorage.getItem(KEY_STORE)||""}catch(e){return""}}
  function setKey(k){try{sessionStorage.setItem(KEY_STORE,k)}catch(e){}}
  function clearKey(){try{sessionStorage.removeItem(KEY_STORE)}catch(e){}}

  function show(view){
    $("login").classList.toggle("hidden",view!=="login");
    $("app").classList.toggle("hidden",view!=="app");
  }

  // Zero-dependency inline-SVG sparkline from a numeric array.
  function spark(values){
    if(!values||values.length<2)return"";
    var w=80,h=20,max=Math.max.apply(null,values),min=Math.min.apply(null,values),rng=(max-min)||1;
    var pts=values.map(function(v,i){
      var x=(i/(values.length-1))*w, y=h-((v-min)/rng)*h;
      return x.toFixed(1)+","+y.toFixed(1);
    }).join(" ");
    return'<svg class="spark" width="'+w+'" height="'+h+'" viewBox="0 0 '+w+' '+h+'"><polyline fill="none" stroke="#58a6ff" stroke-width="1.5" points="'+pts+'"/></svg>';
  }

  function esc(s){var d=document.createElement("div");d.textContent=(s==null?"":String(s));return d.innerHTML}

  function renderTools(tools){
    var body=$("toolsBody"); body.innerHTML="";
    var names=Object.keys(tools||{}).sort();
    $("toolsEmpty").classList.toggle("hidden",names.length>0);
    var total=0,errs=0;
    names.forEach(function(n){
      var t=tools[n]; total+=t.totalCalls||0; errs+=t.errorCalls||0;
      var tr=document.createElement("tr");
      tr.innerHTML="<td>"+esc(n)+"</td>"+
        "<td class='num'>"+(t.totalCalls||0)+"</td>"+
        "<td class='num'>"+(t.errorCalls||0)+"</td>"+
        "<td class='num'>"+(t.cacheHits||0)+"</td>"+
        "<td class='num'>"+(t.avgLatencyMs||0)+"</td>"+
        "<td class='num'>"+(t.p95LatencyMs||0)+"</td>";
      body.appendChild(tr);
    });
    txt($("totalCalls"),total);
    txt($("errRate"), total>0?((errs/total*100).toFixed(1)+"%"):"0%");
  }

  function renderHealth(health){
    var body=$("healthBody"); body.innerHTML="";
    var providers=(health&&health.providers)||[];
    $("healthEmpty").classList.toggle("hidden",providers.length>0);
    var status=(health&&health.status)||null;
    txt($("healthStatus"),status||"n/a");
    var dot=$("healthDot"); dot.className="dot "+(status==="healthy"?"green":status==="degraded"?"amber":status==="unhealthy"?"red":"gray");
    providers.forEach(function(p){
      var tr=document.createElement("tr");
      tr.innerHTML="<td>"+esc(p.name)+"</td><td>"+esc(p.type)+"</td>"+
        "<td><span class='tag "+esc(p.breaker)+"'>"+esc(p.breaker)+"</span></td>"+
        "<td>"+(p.available?"yes":"no")+"</td>";
      body.appendChild(tr);
    });
  }

  function renderRate(rl){
    var body=$("rateBody"); body.innerHTML="";
    if(!rl)return;
    var rows=[
      ["Per-minute / tenant",rl.perMinutePerTenant],
      ["Global / second",rl.globalPerSecond],
      ["Daily / tenant",rl.dailyPerTenant]
    ];
    rows.forEach(function(r){
      var tr=document.createElement("tr");
      tr.innerHTML="<th style='width:50%'>"+esc(r[0])+"</th><td class='num'>"+(r[1]||0)+"</td>";
      body.appendChild(tr);
    });
  }

  function renderErrors(errors){
    var body=$("errorsBody"); body.innerHTML="";
    errors=errors||[];
    $("errorsEmpty").classList.toggle("hidden",errors.length>0);
    errors.forEach(function(e){
      var tr=document.createElement("tr");
      tr.innerHTML="<td>"+esc(e.timestamp)+"</td><td>"+esc(e.tool)+"</td>"+
        "<td>"+esc(e.kind)+"</td><td>"+esc(e.provider||"")+"</td>"+
        "<td>"+esc(e.cause||"")+"</td>";
      body.appendChild(tr);
    });
  }

  function render(d){
    txt($("updated"),new Date(d.generatedAt).toLocaleTimeString());
    txt($("sessions"),d.activeSessions);
    renderTools(d.tools);
    renderHealth(d.health);
    renderRate(d.rateLimit);
    renderErrors(d.recentErrors);
  }

  function poll(){
    fetch("dashboard/data",{headers:{"X-Admin-Key":getKey()},cache:"no-store"})
      .then(function(r){
        if(r.status===401){clearKey();stop();show("login");$("loginerr").classList.remove("hidden");throw new Error("unauthorized")}
        if(!r.ok)throw new Error("http "+r.status);
        return r.json();
      })
      .then(render)
      .catch(function(){});
  }

  function start(){show("app");poll();timer=setInterval(poll,POLL_MS)}
  function stop(){if(timer){clearInterval(timer);timer=null}}

  $("enter").addEventListener("click",function(){
    var k=$("key").value.trim(); if(!k)return;
    setKey(k); $("loginerr").classList.add("hidden"); start();
  });
  $("key").addEventListener("keydown",function(e){if(e.key==="Enter")$("enter").click()});
  $("logout").addEventListener("click",function(){clearKey();stop();show("login")});

  if(getKey())start(); else show("login");
})();
</script>
</body>
</html>`
