//go:build js && wasm

// Command wasm exposes pkg/morse to JavaScript as the global object MorseDSP:
//
//	MorseDSP.detect(input)                                  → "text" | "morse" | "empty"
//	MorseDSP.processText(input, needText, needAudio, opts)  → result
//	MorseDSP.processAudio(wavBytes, needText, needAudio, opts) → result
//	MorseDSP.generateTestSignal(text, opts)                 → result
//
// needText / needAudio select which outputs are produced, so no work is spent
// on synthesizing a WAV nobody listens to (or on text nobody reads).
// opts: {lang, wpm, freq, rate, snr, seed}. lang is optional: by default
// ("auto") the alphabet is detected from the input; a language tag such as
// "en", "ua" or "de" forces one. Results of processText and processAudio carry
// the alphabet used as lang (tag) and langName. processAudio always finds the
// tone itself; freq only sets generated audio (and the decoder's fallback).
// Every call returns {ok: true, ...} or {ok: false, error}.
//
// Build: make wasm (GOOS=js GOARCH=wasm go build -trimpath -buildvcs=false -ldflags="-s -w" -o docs/main.wasm ./cmd/wasm)
package main

import (
	"errors"
	"strings"
	"syscall/js"

	"github.com/Super-Morse/morse-dsp/pkg/morse"
)

const version = "1.2.0"

func main() {
	api := js.Global().Get("Object").New()
	api.Set("version", version)
	api.Set("detect", js.FuncOf(detect))
	api.Set("processText", js.FuncOf(safe(processText)))
	api.Set("processAudio", js.FuncOf(safe(processAudio)))
	api.Set("generateTestSignal", js.FuncOf(safe(generateTestSignal)))
	js.Global().Set("MorseDSP", api)
	if ready := js.Global().Get("__onMorseDSPReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {} // keep the Go runtime alive for callbacks
}

type result map[string]any

// safe turns errors and panics into {ok: false, error} objects.
func safe(fn func(args []js.Value) (result, error)) func(js.Value, []js.Value) any {
	return func(_ js.Value, args []js.Value) (ret any) {
		defer func() {
			if r := recover(); r != nil {
				ret = map[string]any{"ok": false, "error": "internal error"}
			}
		}()
		res, err := fn(args)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		res["ok"] = true
		return map[string]any(res)
	}
}

func detect(_ js.Value, args []js.Value) any {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return "empty"
	}
	s := args[0].String()
	switch {
	case morse.IsMorse(s):
		return "morse"
	case strings.TrimSpace(s) == "":
		return "empty"
	}
	return "text"
}

// options -----------------------------------------------------------------

type options struct {
	lang morse.Language
	wpm  float64
	freq float64
	rate int
	snr  float64
	seed uint64
}

func parseOptions(v js.Value) (options, error) {
	o := options{wpm: morse.DefaultWPM, freq: morse.DefaultFrequency, rate: morse.DefaultSampleRate, snr: 10}
	if v.Type() != js.TypeObject {
		return o, nil
	}
	if s := v.Get("lang"); s.Type() == js.TypeString {
		l, err := morse.ParseLanguage(s.String())
		if err != nil {
			return o, err
		}
		o.lang = l
	}
	num := func(key string, dst *float64) {
		if x := v.Get(key); x.Type() == js.TypeNumber {
			*dst = x.Float()
		}
	}
	num("wpm", &o.wpm)
	num("freq", &o.freq)
	num("snr", &o.snr)
	if x := v.Get("rate"); x.Type() == js.TypeNumber {
		o.rate = x.Int()
	}
	if x := v.Get("seed"); x.Type() == js.TypeNumber {
		o.seed = uint64(x.Float())
	}
	return o, nil
}

func argBool(args []js.Value, i int) bool { return len(args) > i && args[i].Truthy() }

func argOpts(args []js.Value, i int) (options, error) {
	if len(args) > i {
		return parseOptions(args[i])
	}
	return parseOptions(js.Undefined())
}

// API -----------------------------------------------------------------------

// processText(input, needText, needAudio, opts): input may be plain text or
// Morse text; the kind is detected automatically.
func processText(args []js.Value) (result, error) {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return nil, errors.New("processText: input must be a string")
	}
	input := args[0].String()
	needText, needAudio := argBool(args, 1), argBool(args, 2)
	o, err := argOpts(args, 3)
	if err != nil {
		return nil, err
	}
	var c conversion
	if morse.IsMorse(input) {
		c, err = fromMorse(input, needText, o.lang)
	} else {
		c, err = fromText(input, o.lang)
	}
	if err != nil {
		return nil, err
	}
	res := result{"kind": c.kind, "warnings": c.warnings, "lang": c.lang.String(), "langName": c.lang.Name()}
	if needText {
		res["text"] = c.text
		res["morse"] = c.code
	}
	if needAudio && c.code != "" {
		if err := synthesizeInto(res, c.code, o, 0.3, false); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// conversion is the text side of processText.
type conversion struct {
	kind, text, code string
	lang             morse.Language // the alphabet used
	warnings         []any
}

// resolve returns lang, or with Auto the detected language.
func resolve(lang morse.Language, detect func(string) morse.Language, input string) morse.Language {
	if lang == morse.Auto {
		return detect(input)
	}
	return lang
}

// fromMorse normalizes Morse input and, if needed, decodes it.
func fromMorse(input string, needText bool, lang morse.Language) (conversion, error) {
	c := conversion{kind: "morse"}
	var err error
	if c.code, err = morse.NormalizeMorse(input); err != nil {
		return c, err
	}
	c.lang = resolve(lang, morse.DetectMorseLanguage, input)
	if !needText {
		return c, nil
	}
	c.text, err = morse.DecodeText(input, c.lang)
	var uk *morse.UnknownCodesError
	if errors.As(err, &uk) {
		c.warnings = append(c.warnings, "Unknown codes were replaced with *: "+strings.Join(uk.Codes, " "))
		err = nil
	}
	return c, err
}

// fromText encodes plain text; the normalized text is cheap, so it is
// always filled in.
func fromText(input string, lang morse.Language) (conversion, error) {
	c := conversion{kind: "text", text: morse.NormalizeText(input), lang: resolve(lang, morse.DetectLanguage, input)}
	var err error
	c.code, err = morse.EncodeText(input, lang)
	var uc *morse.UnknownCharsError
	if errors.As(err, &uc) {
		c.warnings = append(c.warnings, "Skipped characters without a Morse code: "+joinRunes(uc.Chars))
		err = nil
	}
	return c, err
}

// synthesizeInto keys Morse text into a WAV and stores it in res (see
// addAudio). lead is the silence before and after, noise adds white noise at
// opts.snr.
func synthesizeInto(res result, code string, o options, lead float64, noise bool) error {
	enc, err := morse.NewEncoder(morse.EncoderConfig{
		SampleRate: o.rate, WPM: o.wpm, Frequency: o.freq, Language: o.lang,
		Amplitude: morse.DefaultAmplitude, RampMs: morse.DefaultRampMs, LeadIn: lead, Tail: lead,
		Noise: noise, SNR: o.snr, Seed: o.seed,
	})
	if err != nil {
		return err
	}
	runs, err := enc.Timeline(code)
	if err != nil {
		return err
	}
	addAudio(res, enc.Render(runs), o.rate, runs)
	return nil
}

// processAudio(wavBytes, needText, needAudio, opts): decodes a WAV file. With
// needAudio the result carries a clean signal re-keyed from the decoder's
// detected key timeline (original timing, no noise).
func processAudio(args []js.Value) (result, error) {
	if len(args) == 0 || args[0].Type() != js.TypeObject {
		return nil, errors.New("processAudio: expected a Uint8Array with WAV data")
	}
	data := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(data, args[0])
	needText, needAudio := argBool(args, 1), argBool(args, 2)
	o, err := argOpts(args, 3)
	if err != nil {
		return nil, err
	}

	samples, rate, err := morse.DecodeWAV(data)
	if err != nil {
		return nil, err
	}
	cfg := morse.DefaultDecoderConfig()
	cfg.SampleRate = rate
	cfg.WPM = o.wpm
	cfg.Frequency = o.freq // only a fallback: the tone is always searched for
	cfg.AutoFrequency = true
	cfg.Language = o.lang
	if cfg.Frequency >= float64(rate)/2 {
		cfg.Frequency = float64(rate) / 4
	}
	r, err := morse.DecodeSamples(samples, cfg)
	if err != nil {
		return nil, err
	}

	res := result{
		"kind":       "audio",
		"wpm":        r.WPM,
		"freq":       r.Frequency,
		"sampleRate": rate,
		"duration":   float64(len(samples)) / float64(rate),
		"lang":       r.Language.String(),
		"langName":   r.Language.Name(),
	}
	if needText {
		res["text"] = r.Text
		res["morse"] = r.Morse
	}
	if needAudio {
		enc, err := morse.NewEncoder(morse.EncoderConfig{
			SampleRate: rate, Frequency: r.Frequency, Amplitude: morse.DefaultAmplitude, RampMs: morse.DefaultRampMs,
		})
		if err != nil {
			return nil, err
		}
		addAudio(res, enc.Render(r.Keying), rate, r.Keying)
	}
	return res, nil
}

// generateTestSignal(text, opts): a noisy test recording at opts.snr dB.
// text may be plain text or Morse.
func generateTestSignal(args []js.Value) (result, error) {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return nil, errors.New("generateTestSignal: text must be a string")
	}
	input := args[0].String()
	o, err := argOpts(args, 1)
	if err != nil {
		return nil, err
	}
	var code string
	if morse.IsMorse(input) {
		code, err = morse.NormalizeMorse(input)
	} else {
		code, err = morse.EncodeText(input, o.lang)
		var uc *morse.UnknownCharsError
		if errors.As(err, &uc) {
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	if code == "" {
		return nil, errors.New("nothing to transmit: the text has no characters with a Morse code")
	}
	res := result{"morse": code, "snr": o.snr}
	if err := synthesizeInto(res, code, o, 0.6, true); err != nil {
		return nil, err
	}
	return res, nil
}

// helpers -------------------------------------------------------------------

// addAudio stores the WAV bytes (Uint8Array), duration and the key timeline as
// a flat array of alternating durations starting with key-up, which the page
// uses to flash its key LED in sync with playback.
func addAudio(res result, samples []float32, rate int, runs []morse.KeyRun) {
	wav := morse.EncodeWAV(samples, rate)
	arr := js.Global().Get("Uint8Array").New(len(wav))
	js.CopyBytesToJS(arr, wav)
	res["wav"] = arr
	res["duration"] = float64(len(samples)) / float64(rate)
	keying := make([]any, 0, len(runs)+1)
	if len(runs) > 0 && runs[0].On {
		keying = append(keying, 0.0)
	}
	for _, r := range runs {
		keying = append(keying, r.Dur)
	}
	res["keying"] = keying
}

func joinRunes(rs []rune) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}
