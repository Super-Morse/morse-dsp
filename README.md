# 📟 Morse DSP — CW Encoder & Streaming Decoder

A high-performance **Morse code (CW)** toolkit written in pure **Go (Golang)**: a text codec, a CW audio synthesizer and a **real-time DSP decoder** built on the **Goertzel algorithm**, shipped as a Unix-pipe-friendly **CLI** and as a **WebAssembly (WASM)** radio console that runs entirely in the browser. Supports **English, Russian and Ukrainian** alphabets.

🚀 **Live Web Console:** [https://super-morse.github.io/morse-dsp](https://super-morse.github.io/morse-dsp)

🔗 **Upcoming Companion:** `sdr-receiver` (Python) — demodulates KiwiSDR / RTL-SDR radio into audio and pipes it straight into `morse-dsp decode`.

<p align="center">
  <!-- CI/CD & SonarCloud Badges -->
  <a href="https://github.com/Super-Morse/morse-dsp/actions/workflows/sonar.yml">
    <img src="https://github.com/Super-Morse/morse-dsp/actions/workflows/sonar.yml/badge.svg" alt="CI Build" />
  </a>
  <a href="https://sonarcloud.io/summary/new_code?id=Super-Morse_morse-dsp">
    <img src="https://sonarcloud.io/api/project_badges/measure?project=Super-Morse_morse-dsp&metric=alert_status" alt="Quality Gate Status" />
  </a>
  <a href="https://sonarcloud.io/summary/new_code?id=Super-Morse_morse-dsp">
    <img src="https://sonarcloud.io/api/project_badges/measure?project=Super-Morse_morse-dsp&metric=coverage" alt="Coverage" />
  </a>
  <a href="https://sonarcloud.io/summary/new_code?id=Super-Morse_morse-dsp">
    <img src="https://sonarcloud.io/api/project_badges/measure?project=Super-Morse_morse-dsp&metric=vulnerabilities" alt="Vulnerabilities" />
  </a>
  <a href="https://sonarcloud.io/summary/new_code?id=Super-Morse_morse-dsp">
    <img src="https://sonarcloud.io/api/project_badges/measure?project=Super-Morse_morse-dsp&metric=code_smells" alt="Code Smells" />
  </a>
  <br />
  <!-- Tech Stack Badges -->
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go 1.22+" />
  <img src="https://img.shields.io/badge/WebAssembly-WASM-654FF0?style=flat&logo=webassembly&logoColor=white" alt="WebAssembly" />
  <img src="https://img.shields.io/badge/DSP-Goertzel-FFB547?style=flat" alt="Goertzel DSP" />
  <img src="https://img.shields.io/badge/Dependencies-stdlib%20only-success?style=flat" alt="Standard library only" />
  <img src="https://img.shields.io/badge/Alphabets-EN%20%7C%20RU%20%7C%20UA-informational?style=flat" alt="EN RU UA" />
</p>

👉 **Live Analytics Dashboard:** [SonarCloud Project Overview](https://sonarcloud.io/project/overview?id=Super-Morse_morse-dsp)

---

## 🧠 Interesting Fact

Morse code is older than the telephone, yet it is still on the air every day:

* On **May 24, 1844** Samuel Morse sent the first long-distance telegraph message, *"What hath God wrought"*, from Washington to Baltimore.
* **SOS** is not an abbreviation: it was chosen in 1906 simply because `... --- ...` is impossible to mistake. Morse remained the maritime distress standard until **1999**.
* Timing is defined by the word **PARIS**: exactly **50 units** long, so at *N* WPM one dot lasts `1200 / N` ms.
* The receiving record, **75.2 WPM**, was set by Ted McElroy in **1939** — about 16 ms per dot. This decoder copies it too (start it with `--wpm 40`; up to 50 WPM the default start is enough).

---

## 📊 Code Quality & SonarCloud Metrics

| Metric | Status / Value | Description |
|---|---|---|
| **🎯 Coverage** | **100% core · 99.9% overall** | `pkg/morse` fully covered; the CLI is tested through `run()`, only the one-line `main()` stays outside. |
| **🧪 Test Suite** | **58 tests, 0 failures** | Unit, end-to-end (noisy audio → text) and CLI tests; the core suite also runs compiled to WebAssembly. |
| **🧹 Maintainability** | **Cognitive complexity ≤ 15** | Every function stays below Sonar's `go:S3776` threshold; `go vet` and `gofmt` clean. |
| **🔒 Dependencies** | **Go standard library only** | No third-party modules, no cgo, no `os`/`syscall` in the core — compiles unchanged for `GOOS=js`. |
| **⚡ Performance** | **~1,490× real time** | Decoding 8 kHz audio on one CPU core (`make bench`). |

Live ratings for security, reliability and duplication are published by SonarCloud on every push to `main` (see the badges above).

---

## 📸 Demo & Interface

<details open>
<summary><b>Click to expand screenshots</b></summary>
<br>

<div align="center">
  <table border="0" cellspacing="10" cellpadding="0">
    <tr>
      <td align="center" valign="top" width="64%">
        <b>🖥️ Desktop Radio Console</b><br><br>
        <img src="docs/assets/desktop.png" alt="Desktop Web Console" width="100%" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.3);"/>
      </td>
      <td align="center" valign="top" width="30%">
        <b>📱 Mobile Portrait</b><br><br>
        <img src="docs/assets/mobile-portrait.png" alt="Mobile Portrait View" width="100%" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.3);"/>
      </td>
    </tr>
    <tr>
      <td align="center" valign="top" colspan="2">
        <b>📱 Mobile Landscape (input and output side by side)</b><br><br>
        <img src="docs/assets/mobile-landscape.png" alt="Mobile Landscape View" width="70%" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.3);"/>
      </td>
    </tr>
  </table>
</div>

</details>

---

## ✨ Features

- **Bidirectional Text Codec:** Text ⇄ Morse for Latin (ITU), Russian and Ukrainian alphabets plus digits and punctuation. Lenient parsing of `.` `·` `•` / `-` `_` `—` notations and `/`, `|` or double-space word separators.
- **Clean CW Synthesis:** Continuous-phase sine keyed with **5 ms raised-cosine ramps** (no key clicks), PARIS timing, configurable WPM and tone, optional **white Gaussian noise at a given SNR** for testing.
- **Streaming DSP Decoder:** Sliding **Goertzel** filter → envelope detector → **adaptive threshold** (median noise floor, peak tracker, squelch, hysteresis) → glitch filter → **automatic WPM tracking** → text, emitted character by character in real time.
- **Automatic Tone Search:** Optional Welch/FFT spectrum scan finds the CW tone between 300 and 1500 Hz (`--freq auto`).
- **SDR-Ready Pipe Interface:** Reads raw `s16le`/`f32le` PCM from stdin with low latency — designed for `python sdr.py | morse-dsp decode`.
- **WAV I/O:** Reads 8/16/24/32-bit PCM and 32/64-bit float WAV (mono or stereo, any rate, even streamed WAVs); writes 16-bit PCM.
- **In-Browser Radio Console:** The same Go code compiled to WASM. Auto-detects text vs Morse and EN/RU/UA input, decodes uploaded WAVs, generates noisy test signals in one click, and re-keys a **clean signal** from what the decoder heard.
- **Responsive Mobile UX:** Portrait and landscape layouts, safe-area insets for notched phones, touch-sized controls, no horizontal scrolling down to 320 px.

---

## 🛠️ Technical Stack & Architecture

- **Core Engine:** Go (Golang), standard library only
- **Compilation Targets:** native CLI, WebAssembly (`GOOS=js GOARCH=wasm`)
- **Frontend:** Vanilla JavaScript (ES2020+), HTML5 Audio, Canvas, CSS Grid
- **Quality & DevOps:** Go test + coverage, SonarCloud CI, GitHub Pages deployment

```text
       ┌────────────────────────┐
       │  Text / Morse / Audio  │ ◄─── CLI args · stdin pipe · WAV · Web UI
       └───────────┬────────────┘
                   │
                   ▼
       ┌────────────────────────┐
       │   pkg/morse (pure Go)  │ ◄─── Alphabets · Codec · Encoder · WAV/PCM
       └───────────┬────────────┘
                   │
                   ▼ (streaming samples)
       ┌────────────────────────┐
       │   Goertzel DSP Decoder │ ◄─── Adaptive Threshold · WPM Estimator
       └───────────┬────────────┘
                   │
         ┌─────────┴──────────┐
         ▼                    ▼
┌──────────────────┐  ┌──────────────────┐
│  cmd/morse-dsp   │  │    cmd/wasm      │
│  CLI · stdout    │  │  WASM · Web UI   │
└──────────────────┘  └──────────────────┘
```

### 📡 Ecosystem: Unix Pipe from the SDR Receiver

```text
┌───────────────────────────────┐                    ┌───────────────────────────────┐
│ sdr-receiver (Python)         │   raw PCM, mono    │ morse-dsp decode (Go)         │
│ IQSource → demodulator → audio│ ─────── stdin ───▶ │ Goertzel → threshold → text   │ ──▶ stdout
└───────────────────────────────┘   s16le / f32le    └───────────────────────────────┘
```

The contract is a header-less stream of samples:

| Parameter | Value | Flag |
|---|---|---|
| Sample format | `s16le` (default) or `f32le` | `--sample-format` |
| Channels | 1 (mono) | — |
| Sample rate | any, 1–384 kHz (default 8000; KiwiSDR uses 12000) | `--rate` |

```python
import sys
import numpy as np

def emit(audio: np.ndarray) -> None:
    """audio: mono float samples in [-1, 1] at RATE Hz."""
    pcm = (np.clip(audio, -1, 1) * 32767).astype("<i2")
    sys.stdout.buffer.write(pcm.tobytes())
    sys.stdout.buffer.flush()
```

```bash
python sdr.py | ./bin/morse-dsp decode --in-format pcm --rate 12000 --freq auto --linebreak 2
```

---

## 🚀 Local Development Setup

Requires **Go 1.22+** (Python 3 for `make serve`, Node.js for `make test-wasm`).

### 1. Clone the repository
```bash
git clone https://github.com/Super-Morse/morse-dsp.git
cd morse-dsp
```

### 2. Build the CLI and the WebAssembly binary
```bash
make            # tests + bin/morse-dsp + docs/main.wasm
make cli        # bin/morse-dsp only
make wasm       # docs/main.wasm + the wasm_exec.js matching your Go toolchain
```

### 3. Launch the local web console
```bash
make serve      # serves docs/ on http://localhost:8080
```

The page must be served over HTTP: browsers refuse to load WebAssembly from `file://`.

---

## 💻 CLI Usage

```text
morse-dsp [encode|decode] [flags] [text ...]
morse-dsp --mode encode|decode [flags] [text ...]
```

Input comes from text arguments, `--in`/`-i`, or stdin; output goes to `--out`/`-o` or stdout. Without a mode the tool decides: Morse text and audio are decoded, anything else is encoded.

```bash
# A. Text → Morse
morse-dsp encode "HELLO WORLD"                     # .... . .-.. .-.. --- / .-- --- .-. .-.. -..
morse-dsp encode --lang ua "Дим"                   # -.. -.-- --   (Ukrainian И)

# B. Morse → Text
morse-dsp decode --in-format text ".... . .-.. .-.. ---"
morse-dsp decode -.-. --.-                          # CQ (Morse arguments are not mistaken for flags)
morse-dsp decode --lang ru ".--. .-. .. .-- . -"    # ПРИВЕТ
echo "... --- ..." | morse-dsp                      # SOS, mode detected automatically

# C. Text → Audio
morse-dsp encode -o cq.wav "CQ CQ DE R1ABC K"
morse-dsp encode --out-format wav --wpm 25 --freq 650 --snr 10 "TEST" > test.wav
morse-dsp encode --out-format pcm "CQ" | aplay -f S16_LE -r 8000 -c 1

# D. Audio → Text (real time)
morse-dsp decode -i cq.wav
morse-dsp decode < cq.wav                           # WAV / PCM / text sniffed from the first bytes
morse-dsp encode --out-format pcm --realtime "CQ CQ TEST" | morse-dsp decode --in-format pcm
```

| Flag | Default | Description |
|---|---|---|
| `--mode` | auto | `encode` or `decode` (same as the first argument) |
| `--in-format` | `auto` | `text`, `wav`, `pcm` or `auto` |
| `--out-format` | `text` | for encode: `text`, `wav`, `pcm` (`wav` when `-o *.wav`) |
| `-i`, `--in` / `-o`, `--out` | stdin / stdout | input and output files |
| `--wpm` | 20 | speed; for the decoder only the starting guess |
| `--freq` | 700 | tone in Hz; for decoding also `auto` |
| `--rate` | 8000 | sample rate of generated audio and of raw PCM input |
| `--sample-format` | `s16le` | raw PCM format: `s16le` or `f32le` |
| `--lang` | `en` | alphabet: `en`, `ru` or `ua` (`uk`) |
| `--snr` | off | add white noise at this SNR in dB |
| `--seed` | random | noise seed for reproducible output |
| `--amp`, `--ramp` | 0.5, 5 | tone amplitude and ramp time in ms |
| `--squelch` | 12 | decoder threshold above the noise floor, dB |
| `--linebreak` | 0 | newline after N seconds of silence (0 = off) |
| `--realtime` | off | pace PCM output like a live receiver |
| `-v` | off | diagnostics on stderr: duration, detected tone, WPM estimate |

Exit codes: `0` success, `1` processing error, `2` invalid arguments.

> **Ukrainian vs Russian:** both alphabets share most codes, so decoding needs `--lang`. Encoding only needs it for **И** (`..` in Russian, `-.--` in Ukrainian); with `--lang en` the variant is detected from the text (І, Ї, Є, Ґ ⇒ Ukrainian). **Ґ** has no code of its own and is sent as Г.

---

## 🌐 Web Console & GitHub Pages

- **Text tab:** detects plain text vs Morse and the EN/RU/UA alphabet as you type.
- **Audio tab:** upload a `.wav`, or press **Generate noisy test signal** to synthesize the phrase at the chosen WPM, tone and SNR; the decoded result is compared with the reference.
- **DSP settings:** language, speed (10–40 WPM), tone (400–1000 Hz), automatic tone search.
- **Output:** *Text* (plain text + Morse with copy buttons and a dot/dash tape) and *Audio* (player + `.wav` download; for audio input a clean signal re-keyed from the decoder's timeline). At least one output is always on — unchecking the last one switches the other on — and a disabled output is not computed in WASM at all.

The site is the `docs/` folder, served by GitHub Pages straight from the `main` branch (**Settings → Pages → Deploy from a branch → `main` / `/docs`**). Because there is no build step on the Pages side, the compiled `docs/main.wasm` and its `wasm_exec.js` are committed: after changing Go code, run `make wasm` and commit both files. The build is reproducible (`-trimpath -buildvcs=false`), and the `wasm.yml` workflow rebuilds it on every push and fails if the committed binary is stale. `docs/.nojekyll` turns off Jekyll processing, and `docs/assets/` holds the README screenshots.

```js
MorseDSP.detect(input)                                     // "text" | "morse" | "empty"
MorseDSP.processText(input, needText, needAudio, opts)     // {kind, text, morse, wav, duration, keying, warnings}
MorseDSP.processAudio(wavBytes, needText, needAudio, opts) // {text, morse, wav, wpm, freq, sampleRate, duration, keying}
MorseDSP.generateTestSignal(text, opts)                    // {wav, morse, duration, keying}
// opts: {lang: "en"|"ru"|"ua", wpm, freq, rate, autoFreq, snr, seed}; errors: {ok: false, error}
```

---

## 📚 Go Library

```go
import "github.com/Super-Morse/morse-dsp/pkg/morse"

code, _ := morse.EncodeText("CQ DE R1ABC", morse.English) // "-.-. --.- / -.. . / .-. .---- .- -... -.-."
text, _ := morse.DecodeText(code, morse.English)          // "CQ DE R1ABC"

cfg := morse.DefaultEncoderConfig()
cfg.WPM, cfg.Noise, cfg.SNR = 25, true, 10
enc, _ := morse.NewEncoder(cfg)
samples, _ := enc.Synthesize("CQ CQ TEST")                // []float32
_ = morse.WriteWAV(w, samples, cfg.SampleRate)

dec, _ := morse.NewDecoder(morse.DecoderConfig{SampleRate: 12000, AutoFrequency: true, Language: morse.Ukrainian})
_ = dec.DecodeReader(os.Stdin, morse.S16LE, os.Stdout)    // streaming, text as it arrives
// or: for s := range dec.DecodeChan(ch) { ... }          // ch <-chan float32
// or: s := dec.Process(chunk); ...; s += dec.Flush()
```

---

## 🔬 How the Decoder Works

```text
samples ─▶ sliding Goertzel ─▶ envelope (dB) ─▶ adaptive threshold ─▶ glitch filter
           Hann 16 ms / 4 ms                    noise median, peak,   ≈0.3 dot
                                                squelch, hysteresis
        ─▶ mark/space durations ─▶ unit (WPM) fit ─▶ dots/dashes & gaps ─▶ text
```

1. **Goertzel filter:** every 4 ms the tone amplitude is measured over a 16 ms Hann window — a ~94 Hz pass band, tolerant to about ±60 Hz of mistuning.
2. **Adaptive threshold:** the noise floor is the median of the last second of key-up frames; the signal level is a fast-attack peak tracker that also follows fading (QSB). The decision level sits halfway between them in the *linear* domain, so mark edges are not biased by filter smearing, and never below *noise + squelch*. A key-down far longer than any element (a carrier) becomes the new noise floor.
3. **Glitch filter:** a change of key state is accepted only after it lasted ~0.3 dot and is back-dated to where it started.
4. **Speed tracking:** the dot length is fitted robustly to the last 32 marks (1, 3 units) and gaps (1, 3, 7 units). Every element divided by every multiple is a candidate, so the estimate cannot lock up when the speed doubles and old dashes look like new dots.
5. **Acquisition:** the first 12 elements of every transmission are held back until the speed fit is reliable, then replayed — so the first characters decode correctly even when the real speed is half or double `--wpm`.
6. **Real-time output:** a character is printed as soon as the following gap exceeds 2 dots, a word space after 5 dots.

### 📈 Decoding Accuracy

Character error rate on synthetic audio (8 kHz, SNR over the full 0–4 kHz band, 12 runs × 80 characters, decoder always starting from 20 WPM):

| SNR | 12 WPM | 18 WPM | 25 WPM | 32 WPM | 40 WPM |
|---|---|---|---|---|---|
| 0 dB | 0 % | 0 % | 0 % | 0 % | 0 % |
| −3 dB | 0.1 % | 0.4 % | 1.0 % | 2.9 % | 46 % |

Below about −5 dB the signal drops under the squelch. Ten minutes of pure noise produce **zero** false characters.

---

## 🧪 Testing & Code Coverage

```bash
go test -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1
make test-wasm      # the core suite compiled to WebAssembly and run in Node.js
```

**Output Result:**
```text
ok      github.com/Super-Morse/morse-dsp/cmd/morse-dsp  0.7s  coverage: 99.6% of statements
ok      github.com/Super-Morse/morse-dsp/pkg/morse      1.4s  coverage: 100.0% of statements
```

The acceptance test `TestEndToEndNoisyStreaming` synthesizes 4 phrases at **15 and 25 WPM with white noise at SNR 10 dB**, streams them through the decoder — as raw PCM in random odd-sized chunks (the SDR pipe scenario) and as a `<-chan float32` — and requires an **exact** text match for 3 noise seeds each. Further tests cover all three alphabets, speeds from 10 to 40 WPM, speed changes between transmissions, carriers, fading stations, auto-tuning after 35 s of noise, 6–48 kHz sample rates and every CLI scenario.

---

## 💡 Engineering Trade-offs (SRE Note)

- **Squelch 12 dB:** at 11 dB ten minutes of noise still give zero false characters, at 10 dB about 32. The default keeps a margin for impulsive band noise; `--squelch` trades it for sensitivity.
- **Acquisition latency:** holding back the first elements costs ~1–2 s of latency at the start of each transmission, in exchange for correct first characters at unknown speeds.
- **Speed estimator memory:** a faster-forgetting estimator follows abrupt 2× speed jumps one character sooner, but triples the error rate at −3 dB SNR — stability won.

## ⚠️ Limitations

- Farnsworth timing (stretched letter gaps) can produce extra word spaces.
- A sudden ~10 dB jump of the noise floor may cost one spurious character while the noise estimate adapts (~0.5 s).
- Very irregular hand keying decodes worse than machine-sent CW.
- Punctuation follows ITU for all languages (the Russian `......` full stop is not used).

---

## 🔗 Related Projects

* **[rubiks-visual-detector](https://github.com/Super-Morse/rubiks-visual-detector)** — Computer-vision Rubik's cube scanner (YOLO11 + ONNX Runtime) with an in-browser Go/WASM solver.
* **[rubiks-solver](https://github.com/Super-Morse/rubiks-solver)** — Standalone WebAssembly Kociemba solver with physical cube validation and 100% test coverage.

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---
<p align="right">
  <em>Morse Code <span style="letter-spacing: 2px;">..- ._</span></em> 📟
</p>
