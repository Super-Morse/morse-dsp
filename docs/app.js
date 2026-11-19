// morse-dsp web console. All signal processing happens in Go (main.wasm);
// this file only wires the UI to the MorseDSP API exported by cmd/wasm.
'use strict';

(() => {
  const $ = (id) => document.getElementById(id);
  const el = {
    status: $('wasmStatus'), brandLamp: $('brandLamp'),
    tabText: $('tabText'), tabAudio: $('tabAudio'), paneText: $('paneText'), paneAudio: $('paneAudio'),
    textIn: $('textIn'), clearBtn: $('clearBtn'), detectBadge: $('detectBadge'), detectText: $('detectText'),
    dropzone: $('dropzone'), fileIn: $('fileIn'), genBtn: $('genBtn'), snrIn: $('snrIn'), snrOut: $('snrOut'),
    audioInBox: $('audioInBox'), audioInName: $('audioInName'), audioInMeta: $('audioInMeta'),
    audioInPlayer: $('audioInPlayer'), inLamp: $('inLamp'), compareLine: $('compareLine'),
    wpmIn: $('wpmIn'), wpmOut: $('wpmOut'), freqIn: $('freqIn'), freqOut: $('freqOut'),
    mDot: $('mDot'), mDash: $('mDash'), mWpm: $('mWpm'), mTone: $('mTone'),
    outText: $('outText'), outAudio: $('outAudio'), toggleHint: $('toggleHint'), notices: $('notices'),
    textBlock: $('textBlock'), audioBlock: $('audioBlock'), plainOut: $('plainOut'), morseOut: $('morseOut'), tape: $('tape'),
    langTag: $('langTag'),
    audioLabel: $('audioLabel'), audioMeta: $('audioMeta'), audioOut: $('audioOut'), outLamp: $('outLamp'),
    downloadBtn: $('downloadBtn'),
  };
  const SAMPLE_RATE = 8000;
  const DEFAULT_PHRASE = 'CQ CQ DE UR5ABC TEST K';
  const STORE_KEY = 'morse-dsp:settings:v1';

  const state = {
    ready: false,
    tab: 'text',
    wpm: 20, freq: 700, snr: 10,
    outText: true, outAudio: true,
    audioIn: null,            // { bytes, name, reference, keying }
    urls: { in: null, out: null },
    keyTimes: { in: null, out: null },
    tapeCode: '',
  };

  // ---------------------------------------------------------------- settings
  function loadSettings() {
    let s = {};
    try { s = JSON.parse(localStorage.getItem(STORE_KEY) || '{}') || {}; } catch { /* private mode */ }
    const num = (v, lo, hi, d) => (Number.isFinite(v) && v >= lo && v <= hi ? v : d);
    state.wpm = num(s.wpm, 10, 40, 20);
    state.freq = num(s.freq, 400, 1000, 700);
    state.snr = num(s.snr, -5, 30, 10);
    state.outText = s.outText !== false;
    state.outAudio = s.outAudio !== false;
    if (!state.outText && !state.outAudio) state.outText = true;
  }

  function saveSettings() {
    const { wpm, freq, snr, outText, outAudio } = state;
    try { localStorage.setItem(STORE_KEY, JSON.stringify({ wpm, freq, snr, outText, outAudio })); } catch { /* ignore */ }
  }

  function syncControls() {
    el.wpmIn.value = state.wpm;
    el.freqIn.value = state.freq;
    el.snrIn.value = state.snr;
    el.outText.checked = state.outText;
    el.outAudio.checked = state.outAudio;
    updateReadouts();
    updateBlocks();
  }

  function setFill(input) {
    const p = ((input.value - input.min) / (input.max - input.min)) * 100;
    input.style.setProperty('--fill', p + '%');
  }

  function updateReadouts() {
    el.wpmOut.innerHTML = `<b>${state.wpm}</b> WPM`;
    el.freqOut.innerHTML = `<b>${state.freq}</b> Hz`;
    el.snrOut.textContent = `${state.snr} dB`;
    [el.wpmIn, el.freqIn, el.snrIn].forEach(setFill);
    const dot = 1200 / state.wpm;
    el.mDot.textContent = `${Math.round(dot)} ms`;
    el.mDash.textContent = `${Math.round(dot * 3)} ms`;
  }

  function updateEstimate(r) {
    el.mWpm.textContent = r && r.text ? r.wpm.toFixed(1) : '—';
    el.mTone.textContent = r && r.freq ? `${Math.round(r.freq)} Hz` : '—';
  }

  function updateBlocks() {
    el.textBlock.hidden = !state.outText;
    el.audioBlock.hidden = !state.outAudio;
  }

  // ---------------------------------------------------------------- WASM boot
  function setStatus(kind, text) {
    el.status.dataset.state = kind;
    el.status.querySelector('.status-text').textContent = text;
  }

  async function boot() {
    if (location.protocol === 'file:') {
      setStatus('error', 'needs an HTTP server');
      notice('error', 'Browsers do not load WebAssembly from file://. Run "make serve" and open http://localhost:8080.');
      return;
    }
    try {
      const go = new Go();
      const ready = new Promise((resolve) => { window.__onMorseDSPReady = resolve; });
      let instance;
      try {
        ({ instance } = await WebAssembly.instantiateStreaming(fetch('main.wasm'), go.importObject));
      } catch {
        // e.g. a server without the application/wasm MIME type
        const resp = await fetch('main.wasm');
        if (!resp.ok) throw new Error(`main.wasm: HTTP ${resp.status}`);
        ({ instance } = await WebAssembly.instantiate(await resp.arrayBuffer(), go.importObject));
      }
      go.run(instance);
      await ready;
      state.ready = true;
      el.genBtn.disabled = false;
      setStatus('ready', `WASM ${window.MorseDSP.version} · online`);
      run();
    } catch (err) {
      setStatus('error', 'WASM failed to load');
      notice('error', `Could not load main.wasm (${err.message}). Build it with: make wasm.`);
    }
  }

  // ---------------------------------------------------------------- notices
  function clearNotices() { el.notices.replaceChildren(); }
  function notice(kind, text) {
    const div = document.createElement('div');
    div.className = `notice ${kind}`;
    div.textContent = text;
    el.notices.append(div);
  }

  // ---------------------------------------------------------------- processing
  let timer = 0;
  function schedule(delay = 140) {
    clearTimeout(timer);
    timer = setTimeout(run, delay);
  }

  // No lang and no tone: the core detects the alphabet and always finds the tone itself.
  function opts() {
    return { wpm: state.wpm, freq: state.freq, rate: SAMPLE_RATE };
  }

  function run() {
    clearTimeout(timer);
    if (!state.ready) return;
    clearNotices();
    if (state.tab === 'text') runText();
    else runAudio();
  }

  function runText() {
    const input = el.textIn.value;
    const kind = window.MorseDSP.detect(input);
    showDetect(kind);
    updateEstimate(null);
    if (kind === 'empty') {
      renderEmpty();
      return;
    }
    const r = window.MorseDSP.processText(input, state.outText, state.outAudio, opts());
    if (!r.ok) {
      renderEmpty();
      notice('error', r.error);
      return;
    }
    (r.warnings || []).forEach((w) => notice('warn', w));
    const slug = (r.text || input).trim().slice(0, 24).replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'morse';
    render(r, {
      label: r.kind === 'morse' ? 'Signal for the entered code' : 'Generated signal',
      filename: `morse-${slug}-${state.wpm}wpm.wav`,
    });
  }

  function runAudio() {
    updateEstimate(null);
    el.compareLine.hidden = true;
    const a = state.audioIn;
    if (!a) {
      renderEmpty();
      notice('info', 'Upload a .wav or press "Generate noisy test signal".');
      return;
    }
    const t0 = performance.now();
    const r = window.MorseDSP.processAudio(a.bytes, state.outText, state.outAudio, opts());
    const ms = performance.now() - t0;
    if (!r.ok) {
      renderEmpty();
      notice('error', `Could not process the file: ${r.error}`);
      el.audioInMeta.textContent = '';
      return;
    }
    el.audioInMeta.textContent = `${r.duration.toFixed(2)} s · ${(r.sampleRate / 1000).toFixed(r.sampleRate % 1000 ? 2 : 0)} kHz · DSP ${ms < 1 ? '<1' : ms.toFixed(0)} ms`;
    updateEstimate(r);
    if (!r.morse && state.outText) {
      notice('warn', 'No tone found: the signal may be too weak, or it is not CW.');
    }
    if (a.reference && state.outText) {
      el.compareLine.hidden = false;
      const same = r.text === a.reference;
      el.compareLine.innerHTML = '';
      const mark = document.createElement('span');
      mark.className = same ? 'ok' : 'bad';
      mark.textContent = same ? '✓ matches the reference' : '✗ differs from the reference';
      el.compareLine.append(mark, document.createTextNode(` · reference: ${a.reference}`));
    }
    render(r, {
      label: 'Clean signal (tone keyed by the decoder)',
      filename: a.name.replace(/\.wav$/i, '') + '-clean.wav',
    });
  }

  function render(r, { label, filename }) {
    if (state.outText) {
      el.plainOut.textContent = r.text || '';
      el.morseOut.textContent = r.morse || '';
      showLanguage(r.text ? r.langName : '');
      drawTape(r.morse || '');
    }
    if (state.outAudio) {
      el.audioLabel.textContent = label;
      if (r.wav) setOutAudio(r.wav, r.keying, r.duration, filename);
      else clearOutAudio();
    }
  }

  function renderEmpty() {
    el.plainOut.textContent = '';
    el.morseOut.textContent = '';
    showLanguage('');
    drawTape('');
    clearOutAudio();
  }

  function showLanguage(name) {
    el.langTag.textContent = name || '';
    el.langTag.hidden = !name;
  }

  function showDetect(kind) {
    el.detectBadge.dataset.kind = kind;
    el.detectText.textContent = {
      text: 'text detected → Morse',
      morse: 'Morse detected → text',
      empty: 'waiting for input',
    }[kind] || kind;
  }

  // ---------------------------------------------------------------- audio
  function wavURL(bytes) {
    return URL.createObjectURL(new Blob([bytes], { type: 'audio/wav' }));
  }

  function cumulative(keying) {
    if (!keying || !keying.length) return null;
    const t = new Float64Array(keying.length);
    let acc = 0;
    keying.forEach((d, i) => { acc += d; t[i] = acc; });
    return t; // run i ends at t[i]; odd runs are key-down
  }

  function setOutAudio(wav, keying, duration, filename) {
    if (state.urls.out) URL.revokeObjectURL(state.urls.out);
    state.urls.out = wavURL(wav);
    state.keyTimes.out = cumulative(keying);
    el.audioOut.src = state.urls.out;
    el.downloadBtn.href = state.urls.out;
    el.downloadBtn.download = filename;
    el.downloadBtn.setAttribute('aria-disabled', 'false');
    el.audioMeta.textContent = `${duration.toFixed(2)} s · 16-bit WAV · ${(wav.length / 1024).toFixed(0)} KB`;
  }

  function clearOutAudio() {
    if (state.urls.out) URL.revokeObjectURL(state.urls.out);
    state.urls.out = null;
    state.keyTimes.out = null;
    el.audioOut.removeAttribute('src');
    el.audioOut.load();
    el.downloadBtn.removeAttribute('href');
    el.downloadBtn.setAttribute('aria-disabled', 'true');
    el.audioMeta.textContent = '';
  }

  function setAudioInput(bytes, name, reference, keying) {
    state.audioIn = { bytes, name, reference, keying };
    if (state.urls.in) URL.revokeObjectURL(state.urls.in);
    state.urls.in = wavURL(bytes);
    state.keyTimes.in = cumulative(keying);
    el.audioInPlayer.src = state.urls.in;
    el.audioInName.textContent = name;
    el.audioInMeta.textContent = '';
    el.audioInBox.hidden = false;
    run();
  }

  // Cuts text to at most max characters, ending at a word boundary.
  function truncateAtWord(text, max) {
    if (text.length <= max) return text;
    const cut = text.slice(0, max);
    const space = Math.max(cut.lastIndexOf(' '), cut.lastIndexOf('\n'), cut.lastIndexOf('\t'));
    return (space > 0 ? cut.slice(0, space) : cut).trimEnd();
  }

  function openFile(file) {
    loadFile(file).catch((err) => {
      clearNotices();
      notice('error', `Could not read the file: ${err.message}`);
    });
  }

  async function loadFile(file) {
    if (!file) return;
    if (file.size > 64 * 1024 * 1024) {
      clearNotices();
      notice('error', 'The file is larger than 64 MB. Trim the recording and try again.');
      return;
    }
    const bytes = new Uint8Array(await file.arrayBuffer());
    setAudioInput(bytes, file.name, null, null);
  }

  function generateTest() {
    if (!state.ready) return;
    let phrase = el.textIn.value.trim();
    if (window.MorseDSP.detect(phrase) === 'empty') phrase = DEFAULT_PHRASE;
    if (phrase.length > 160) phrase = truncateAtWord(phrase, 160);
    const seed = Math.floor(Math.random() * 2 ** 31);
    const r = window.MorseDSP.generateTestSignal(phrase, { wpm: state.wpm, freq: state.freq, snr: state.snr, seed, rate: SAMPLE_RATE });
    if (!r.ok) {
      clearNotices();
      notice('error', r.error);
      return;
    }
    const ref = window.MorseDSP.processText(phrase, true, false, {});
    const name = `test-${state.wpm}wpm-${state.freq}hz-snr${state.snr}db.wav`;
    setAudioInput(r.wav, name, ref.ok ? ref.text : null, r.keying);
  }

  // Key lamps follow the key timeline while audio plays.
  function keyAt(times, t) {
    let lo = 0;
    let hi = times.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (times[mid] > t) hi = mid; else lo = mid + 1;
    }
    return lo < times.length && lo % 2 === 1;
  }

  function bindLamp(audio, which, lamp) {
    let raf = 0;
    const set = (on) => {
      lamp.classList.toggle('on', on);
      el.brandLamp.classList.toggle('on', on);
    };
    const tick = () => {
      const times = state.keyTimes[which];
      set(Boolean(times) && !audio.paused && keyAt(times, audio.currentTime));
      raf = requestAnimationFrame(tick);
    };
    audio.addEventListener('play', () => { cancelAnimationFrame(raf); raf = requestAnimationFrame(tick); });
    const stop = () => { cancelAnimationFrame(raf); set(false); };
    audio.addEventListener('pause', stop);
    audio.addEventListener('ended', stop);
    audio.addEventListener('emptied', stop);
  }

  // ---------------------------------------------------------------- tape
  function drawTape(code) {
    state.tapeCode = code;
    const c = el.tape;
    if (!code) { c.hidden = true; return; }
    c.hidden = false;
    const width = c.clientWidth || c.parentElement.clientWidth;
    const unit = width < 420 ? 2 : 3;
    const pad = 10;
    const rowH = 16;
    const maxUnits = Math.max(20, Math.floor((width - 2 * pad) / unit));
    // canonical Morse from the core: words are joined by " / ", lines by "\n"
    const words = code.split('\n').flatMap((line) => line.split(' / ')).map((w) => w.trim()).filter(Boolean);

    const rows = [[]];
    let x = 0;
    for (const w of words) {
      const marks = [];
      let u = 0;
      w.split(' ').filter(Boolean).forEach((letter, li) => {
        if (li) u += 3;
        [...letter].forEach((s, i) => {
          if (i) u += 1;
          const len = s === '.' ? 1 : 3;
          marks.push([u, len]);
          u += len;
        });
      });
      if (x > 0 && x + 7 + u > maxUnits) { rows.push([]); x = 0; }
      if (x > 0) x += 7;
      rows[rows.length - 1].push({ x, marks });
      x += u;
    }
    const shown = rows.slice(0, 10);
    const dpr = window.devicePixelRatio || 1;
    const height = shown.length * rowH + 2 * pad - 6;
    c.width = Math.round(width * dpr);
    c.height = Math.round(height * dpr);
    c.style.height = height + 'px';
    const g = c.getContext('2d');
    g.scale(dpr, dpr);
    g.clearRect(0, 0, width, height);
    g.fillStyle = '#ffb547';
    g.shadowColor = 'rgba(255,181,71,0.55)';
    g.shadowBlur = 6;
    shown.forEach((row, ri) => {
      const y = pad + ri * rowH + 2;
      for (const word of row) {
        for (const [u, len] of word.marks) {
          const px = pad + (word.x + u) * unit;
          if (px > width - pad) continue;
          g.fillRect(px, y, Math.max(1, len * unit - 0.5), 6);
        }
      }
    });
    if (rows.length > shown.length) {
      g.shadowBlur = 0;
      g.fillStyle = '#56665e';
      g.font = '11px "JetBrains Mono", monospace';
      g.fillText(`… ${rows.length - shown.length} more lines`, width - 120, height - 4);
    }
  }

  // ---------------------------------------------------------------- tabs
  function setTab(tab, focus) {
    state.tab = tab;
    const isText = tab === 'text';
    el.tabText.setAttribute('aria-selected', String(isText));
    el.tabAudio.setAttribute('aria-selected', String(!isText));
    el.tabText.tabIndex = isText ? 0 : -1;
    el.tabAudio.tabIndex = isText ? -1 : 0;
    el.paneText.hidden = !isText;
    el.paneAudio.hidden = isText;
    if (focus) (isText ? el.tabText : el.tabAudio).focus();
    run();
  }

  // ---------------------------------------------------------------- output toggles
  let hintTimer = 0;
  function onToggle(changed) {
    const other = changed === el.outText ? el.outAudio : el.outText;
    if (!changed.checked && !other.checked) {
      // at least one output must stay on: switch the other one on instead
      other.checked = true;
      const t = other.closest('.toggle');
      t.classList.remove('flash');
      void t.offsetWidth;
      t.classList.add('flash');
      el.toggleHint.textContent = `At least one output stays on, so "${other === el.outText ? 'Text' : 'Audio'}" was switched on`;
      clearTimeout(hintTimer);
      hintTimer = setTimeout(() => { el.toggleHint.textContent = ''; }, 2600);
    }
    state.outText = el.outText.checked;
    state.outAudio = el.outAudio.checked;
    if (!state.outAudio) el.audioOut.pause();
    updateBlocks();
    saveSettings();
    run();
  }

  // ---------------------------------------------------------------- copy
  async function copyText(btn) {
    const text = $(btn.dataset.copy).textContent;
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.append(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    btn.textContent = 'Copied ✓';
    btn.classList.add('done');
    setTimeout(() => { btn.textContent = 'Copy'; btn.classList.remove('done'); }, 1400);
  }

  // ---------------------------------------------------------------- events
  function wire() {
    el.tabText.addEventListener('click', () => setTab('text'));
    el.tabAudio.addEventListener('click', () => setTab('audio'));
    [el.tabText, el.tabAudio].forEach((t) => t.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Home' || e.key === 'End') {
        e.preventDefault();
        const next = e.key === 'Home' ? 'text' : e.key === 'End' ? 'audio' : state.tab === 'text' ? 'audio' : 'text';
        setTab(next, true);
      }
    }));

    el.textIn.addEventListener('input', () => {
      const v = el.textIn.value;
      showDetect(state.ready ? window.MorseDSP.detect(v) : 'empty');
      schedule(Math.min(600, 140 + v.length / 2)); // long texts make long WAVs
    });
    el.textIn.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); run(); }
    });
    el.clearBtn.addEventListener('click', () => { el.textIn.value = ''; el.textIn.focus(); run(); });
    document.querySelectorAll('[data-example]').forEach((b) => b.addEventListener('click', () => {
      el.textIn.value = b.dataset.example;
      setTab('text');
    }));

    el.fileIn.addEventListener('change', () => openFile(el.fileIn.files[0]));
    ['dragenter', 'dragover'].forEach((ev) => el.dropzone.addEventListener(ev, (e) => {
      e.preventDefault();
      el.dropzone.classList.add('drag');
    }));
    ['dragleave', 'drop'].forEach((ev) => el.dropzone.addEventListener(ev, () => el.dropzone.classList.remove('drag')));
    el.dropzone.addEventListener('drop', (e) => {
      e.preventDefault();
      openFile(e.dataTransfer.files[0]);
    });
    el.genBtn.disabled = true;
    el.genBtn.addEventListener('click', generateTest);
    el.snrIn.addEventListener('input', () => { state.snr = +el.snrIn.value; updateReadouts(); saveSettings(); });

    el.wpmIn.addEventListener('input', () => { state.wpm = +el.wpmIn.value; updateReadouts(); saveSettings(); schedule(); });
    el.freqIn.addEventListener('input', () => { state.freq = +el.freqIn.value; updateReadouts(); saveSettings(); schedule(); });

    el.outText.addEventListener('change', () => onToggle(el.outText));
    el.outAudio.addEventListener('change', () => onToggle(el.outAudio));
    document.querySelectorAll('.copy').forEach((b) => b.addEventListener('click', () => copyText(b)));

    bindLamp(el.audioOut, 'out', el.outLamp);
    bindLamp(el.audioInPlayer, 'in', el.inLamp);
    // only one player at a time
    el.audioOut.addEventListener('play', () => el.audioInPlayer.pause());
    el.audioInPlayer.addEventListener('play', () => el.audioOut.pause());

    let lastWidth = 0;
    new ResizeObserver((entries) => {
      const w = entries[0].contentRect.width;
      if (Math.abs(w - lastWidth) > 4 && state.tapeCode) drawTape(state.tapeCode);
      lastWidth = w;
    }).observe(el.textBlock);
  }

  loadSettings();
  syncControls();
  wire();
  showDetect('empty');
  boot().catch((err) => {
    setStatus('error', 'WASM failed to load');
    notice('error', `Could not start: ${err.message}`);
  });
})();
