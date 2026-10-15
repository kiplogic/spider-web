const $ = (id) => document.getElementById(id);

let currentResults = { articles: [], errors: [] };
let crawling = false;
let jobId = null;
let eventSource = null;
const PREVIEW_LIMIT = 300;

const PHASE_LABEL = {
  discover: 'Memindai halaman website...',
  titles: 'Mengambil judul artikel...',
  done: 'Selesai',
};

function onProgress(p) {
  if (!p) return;
  if (p.log) appendLog(p.log);
  $('statPhase').textContent = PHASE_LABEL[p.phase] || p.phase || '-';
  if (p.phase !== 'done' && p.phase !== 'finished') {
    $('statPages').textContent = p.pagesDone || 0;
    $('statFound').textContent = p.articlesFound || 0;
    $('statTitles').textContent = p.titlesDone || 0;
    $('statTotal').textContent = p.totalTitles || 0;
  }
  updateBar(p);
}

function updateBar(p) {
  const bar = $('bar');
  if (!bar) return;
  if (p.phase === 'discover') {
    bar.classList.add('indeterminate');
    bar.style.width = '';
  } else if (p.phase === 'titles' && p.totalTitles > 0) {
    bar.classList.remove('indeterminate');
    bar.style.width = Math.min(100, Math.round((p.titlesDone / p.totalTitles) * 100)) + '%';
  } else if (p.phase === 'done' || p.phase === 'finished') {
    bar.classList.remove('indeterminate');
    bar.style.width = '100%';
  }
}

function appendLog(line) {
  const log = $('log');
  const div = document.createElement('div');
  div.textContent = line;
  log.appendChild(div);
  while (log.childNodes.length > 500) log.removeChild(log.firstChild);
  log.scrollTop = log.scrollHeight;
}

function setBusy(b) {
  crawling = b;
  $('btnStart').disabled = b;
  $('btnStop').disabled = !b;
}

function switchTab(name) {
  document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === name));
  $('log').hidden = name !== 'log';
  $('resultsPane').hidden = name !== 'results';
}

document.querySelectorAll('.tab').forEach(t =>
  t.addEventListener('click', () => switchTab(t.dataset.tab))
);

function closeStream() {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
}

async function fetchResults() {
  const res = await fetch('/api/jobs/' + jobId + '/results');
  if (!res.ok) throw new Error('gagal mengambil hasil (HTTP ' + res.status + ')');
  const out = await res.json();
  currentResults = { articles: out.articles || [], errors: out.errors || [] };
  renderResults(out.stopped);
  appendLog('== Selesai: ' + currentResults.articles.length + ' artikel, ' + currentResults.errors.length + ' gagal' + (out.stopped ? ' (dihentikan pengguna)' : '') + ' ==');
}

$('btnStart').addEventListener('click', async () => {
  if (crawling) return;
  const urls = $('inputUrls').value.split('\n').map(s => s.trim()).filter(Boolean);
  if (!urls.length) { alert('Masukkan minimal satu URL website dulu.'); return; }
  const opts = {
    mainUrls: urls,
    maxPages: parseInt($('optPages').value, 10) || 200,
    maxArticles: parseInt($('optArticles').value, 10) || 3000,
    concurrency: parseInt($('optConc').value, 10) || 8,
    delayMs: parseInt($('optDelay').value, 10) || 0,
  };
  $('log').textContent = '';
  $('progressCard').hidden = false;
  $('workCard').hidden = false;
  $('resultInfo').hidden = true;
  $('btnExport').hidden = true;
  switchTab('log');
  const bar = $('bar');
  bar.classList.add('indeterminate');
  bar.style.width = '';
  setBusy(true);
  try {
    const res = await fetch('/api/jobs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(opts),
    });
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}));
      throw new Error(errBody.error || 'gagal memulai crawl (HTTP ' + res.status + ')');
    }
    const data = await res.json();
    jobId = data.jobId;

    closeStream();
    eventSource = new EventSource('/api/jobs/' + jobId + '/events');
    eventSource.addEventListener('progress', (ev) => {
      try { onProgress(JSON.parse(ev.data)); } catch (e) { /* abaikan */ }
    });
    eventSource.addEventListener('done', async () => {
      closeStream();
      try {
        await fetchResults();
      } catch (e) {
        appendLog('ERROR: ' + e.message);
      } finally {
        setBusy(false);
      }
    });
    eventSource.onerror = () => {
      // Stream putus di tengah jalan (mis. server restart): cek status sekali.
      closeStream();
      fetch('/api/jobs/' + jobId)
        .then(r => r.json())
        .then(st => {
          if (st.done) return fetchResults();
          appendLog('ERROR: koneksi progress terputus. Coba mulai ulang.');
        })
        .catch(() => appendLog('ERROR: koneksi progress terputus.'))
        .finally(() => setBusy(false));
    };
  } catch (e) {
    appendLog('ERROR: ' + e.message);
    setBusy(false);
  }
});

$('btnStop').addEventListener('click', async () => {
  if (!jobId) return;
  appendLog('>> Permintaan berhenti dikirim, menghentikan...');
  try {
    await fetch('/api/jobs/' + jobId + '/stop', { method: 'POST' });
  } catch (e) {
    appendLog('ERROR: ' + e.message);
  }
});

$('btnExport').addEventListener('click', () => {
  if (!jobId) return;
  if (!currentResults.articles.length && !currentResults.errors.length) {
    alert('Belum ada hasil. Klik "Mulai" dulu.');
    return;
  }
  // Download langsung via browser (1 website -> .xlsx, multi -> .zip).
  window.location.href = '/api/jobs/' + jobId + '/export';
  toast('Mengunduh file Excel...');
});

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

function categoryOf(u) {
  try {
    const segs = new URL(u).pathname.split('/').filter(Boolean);
    if (segs.length < 2) return '';
    return segs[0].replace(/[-_]+/g, ' ').trim();
  } catch (e) {
    return '';
  }
}

function keywordOf(u) {
  try {
    const segs = new URL(u).pathname.split('/').filter(Boolean);
    if (!segs.length) return '';
    return segs[segs.length - 1].replace(/[-_]+/g, ' ').trim();
  } catch (e) {
    return '';
  }
}

function renderResults(stopped) {
  const arts = currentResults.articles || [];
  const errs = currentResults.errors || [];
  $('resultInfo').hidden = false;
  $('btnExport').hidden = false;
  $('resultCount').textContent = arts.length + ' artikel';
  $('resultErr').textContent = errs.length + ' gagal';
  const tbody = $('resultTable').querySelector('tbody');
  tbody.innerHTML = '';
  const shown = Math.min(arts.length, PREVIEW_LIMIT);
  for (let i = 0; i < shown; i++) {
    const a = arts[i];
    const tr = document.createElement('tr');
    tr.innerHTML = `<td>${i + 1}</td><td class="wrap">${esc(a.date)}</td><td class="wrap"><a href="${esc(a.articleUrl)}" target="_blank" rel="noreferrer">${esc(a.articleUrl)}</a></td><td class="wrap">${esc(categoryOf(a.articleUrl))}</td><td class="wrap">${esc(keywordOf(a.articleUrl))}</td><td class="wrap">${esc(a.title)}</td>`;
    tbody.appendChild(tr);
  }

  const errBox = $('errBox');
  if (errs.length) {
    $('errCountInline').textContent = errs.length;
    const eb = $('errTableBody');
    eb.innerHTML = '';
    const eshown = Math.min(errs.length, PREVIEW_LIMIT);
    for (let i = 0; i < eshown; i++) {
      const e = errs[i];
      const tr = document.createElement('tr');
      tr.innerHTML = `<td>${i + 1}</td><td class="wrap"><a href="${esc(e.articleUrl)}" target="_blank" rel="noreferrer">${esc(e.articleUrl)}</a></td><td class="wrap">${esc(e.error)}</td>`;
      eb.appendChild(tr);
    }
    errBox.hidden = false;
    errBox.open = false;
  } else {
    errBox.hidden = true;
  }
  const urlCount = $('inputUrls').value.split('\n').map(s => s.trim()).filter(Boolean).length || 1;
  const cap = (parseInt($('optArticles').value, 10) || 3000) * urlCount;
  let note = arts.length > PREVIEW_LIMIT
    ? 'Pratinjau menampilkan ' + PREVIEW_LIMIT + ' baris pertama dari ' + arts.length + ' baris. Semua baris ikut dalam file Excel.'
    : '';
  if (arts.length >= cap) {
    note = 'Hasil dibatasi "Maks artikel per website" (' + cap + '). Naikkan nilainya, lalu jalankan lagi untuk mengambil lebih banyak.' + (note ? ' ' + note : '');
  }
  $('previewNote').textContent = note;
  switchTab('results');
}

let toastTimer = null;
function toast(msg) {
  const t = $('toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 6000);
}
