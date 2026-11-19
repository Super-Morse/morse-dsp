package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Super-Morse/morse-dsp/pkg/morse"
)

const usageText = `morse-dsp — Morse code (CW) encoder and streaming DSP decoder

Usage:
  morse-dsp [encode|decode] [flags] [text ...]
  morse-dsp --mode encode|decode [flags] [text ...]

Input is the text arguments, else --in FILE, else stdin. Output goes to
--out FILE or stdout. Without a mode, morse-dsp guesses: Morse text and audio
are decoded, anything else is encoded.

Scenarios:
  A  text → Morse     morse-dsp encode "ПРИВІТ, ЕФІРЕ! 73"
  B  Morse → text     morse-dsp decode --in-format text ".... . .-.. .-.. ---"
  C  text → audio     morse-dsp encode --out-format wav -o cq.wav "CQ CQ DE UR5ABC K"
                      morse-dsp encode --out-format pcm "CQ DE UR5ABC" | aplay -f S16_LE -r 8000
  D  audio → text     morse-dsp decode -i cq.wav
                      python sdr.py | morse-dsp decode --in-format pcm --rate 12000

Languages (--lang auto, the default):
  Any supported letter is encoded without a flag: ITU Latin, German Ä Ö Ü ẞ,
  French À Ç È É, Spanish Á Ñ Ó, Polish Ą Ć Ę Ł Ń Ś Ź Ż, Nordic Å Ä Ö Æ Ø,
  Turkish Ç Ğ İ Ş, Ukrainian and Russian Cyrillic. Decoding detects the
  alphabet; ham abbreviations, callsigns and short bursts read as ITU. Files
  are decoded as a whole; stdin is decoded live, character by character, and
  short replies keep the language last detected in the session.

Flags:
`

type options struct {
	mode, inFormat, outFormat string
	inPath, outPath           string
	wpm                       float64
	freq                      string
	rate                      int
	lang                      string
	snr                       float64
	seed                      uint64
	amp, ramp                 float64
	squelch                   float64
	sampleFmt                 string
	realtime                  bool
	lineBreak                 float64
	verbose                   bool
	set                       map[string]bool
	text                      []string
}

// run is main without process globals, so tests can drive every scenario.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opt, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "morse-dsp:", err)
		return 2
	}
	if err := execute(opt, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "morse-dsp:", err)
		return 1
	}
	return 0
}

func parseArgs(args []string, stderr io.Writer) (*options, error) {
	opt := &options{set: map[string]bool{}}
	if len(args) > 0 && (args[0] == "encode" || args[0] == "decode") {
		opt.mode, args = args[0], args[1:]
	}
	fs, mode := newFlagSet(opt, stderr)
	text, err := parseInterleaved(fs, args)
	if err != nil {
		return nil, err
	}
	opt.text = text
	fs.Visit(func(f *flag.Flag) { opt.set[f.Name] = true })
	if *mode != "" {
		if opt.mode != "" && opt.mode != *mode {
			return nil, fmt.Errorf("conflicting modes %q and %q", opt.mode, *mode)
		}
		opt.mode = *mode
	}
	if err := opt.normalize(); err != nil {
		return nil, err
	}
	return opt, nil
}

// newFlagSet defines every flag on opt; the returned pointer receives --mode.
func newFlagSet(opt *options, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("morse-dsp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usageText)
		fs.PrintDefaults()
	}
	mode := fs.String("mode", "", "`encode` or `decode` (same as the first argument)")
	fs.StringVar(&opt.inFormat, "in-format", "auto", "input: `text`, wav, pcm or auto")
	fs.StringVar(&opt.outFormat, "out-format", "", "encode output: `text`, wav or pcm (default text, or wav for -o *.wav)")
	fs.StringVar(&opt.inPath, "in", "", "read input from `file` instead of stdin")
	fs.StringVar(&opt.inPath, "i", "", "shorthand for --in")
	fs.StringVar(&opt.outPath, "out", "", "write output to `file` instead of stdout")
	fs.StringVar(&opt.outPath, "o", "", "shorthand for --out")
	fs.Float64Var(&opt.wpm, "wpm", morse.DefaultWPM, "speed in words per minute (decoder: initial guess, adapts automatically)")
	fs.StringVar(&opt.freq, "freq", "700", "tone frequency in Hz; for decoding also `auto` (search 300–1500 Hz)")
	fs.IntVar(&opt.rate, "rate", morse.DefaultSampleRate, "sample rate in Hz of generated audio and of raw PCM input")
	fs.StringVar(&opt.lang, "lang", "auto", "alphabet: `auto`, en, ua, ru, de, fr, es, pl, sv or tr (a fixed one overrides detection)")
	fs.Float64Var(&opt.snr, "snr", 0, "add white noise at this SNR in dB (encode to audio)")
	fs.Uint64Var(&opt.seed, "seed", 0, "noise seed (default: random)")
	fs.Float64Var(&opt.amp, "amp", morse.DefaultAmplitude, "tone amplitude, 0..1")
	fs.Float64Var(&opt.ramp, "ramp", morse.DefaultRampMs, "rise/fall time of each element in ms")
	fs.Float64Var(&opt.squelch, "squelch", morse.DefaultSquelchDB, "decoder: minimum tone level above the noise floor, dB")
	fs.StringVar(&opt.sampleFmt, "sample-format", "s16le", "raw PCM sample format: `s16le` or f32le")
	fs.BoolVar(&opt.realtime, "realtime", false, "pace raw PCM output at the sample rate (simulates a live receiver)")
	fs.Float64Var(&opt.lineBreak, "linebreak", 0, "decoder: start a new line after this many `seconds` of silence (0 = off)")
	fs.BoolVar(&opt.verbose, "v", false, "print diagnostics (speed, tone, duration) to stderr")
	return fs, mode
}

// parseInterleaved parses flags that may come after positional text, and
// keeps Morse words such as "-.-." (which look like flags) as text. Everything
// after "--" is text.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var text []string
	for len(args) > 0 {
		if looksLikeMorseArg(args[0]) {
			text, args = append(text, args[0]), args[1:]
			continue
		}
		j := 0
		for j < len(args) && !looksLikeMorseArg(args[j]) {
			j++
		}
		words, err := parseChunk(fs, args[:j])
		if err != nil {
			return nil, err
		}
		text, args = append(text, words...), args[j:]
	}
	return append(text, tail...), nil
}

// parseChunk parses a run of arguments without Morse words, returning the
// positional words found between the flags.
func parseChunk(fs *flag.FlagSet, chunk []string) ([]string, error) {
	var words []string
	for len(chunk) > 0 {
		if err := fs.Parse(chunk); err != nil {
			return nil, err
		}
		chunk = fs.Args()
		if len(chunk) > 0 { // a positional word; flags may follow it
			words, chunk = append(words, chunk[0]), chunk[1:]
		}
	}
	return words, nil
}

// normalize checks the option combination and fills in derived defaults.
func (opt *options) normalize() error {
	if !oneOf(opt.mode, "", "encode", "decode") {
		return fmt.Errorf("unknown mode %q (want encode or decode)", opt.mode)
	}
	opt.inFormat = strings.ToLower(opt.inFormat)
	if !oneOf(opt.inFormat, "auto", "text", "wav", "pcm") {
		return fmt.Errorf("unknown --in-format %q (want text, wav, pcm or auto)", opt.inFormat)
	}
	opt.outFormat = strings.ToLower(opt.outFormat)
	if opt.outFormat == "" {
		opt.outFormat = "text"
		if strings.EqualFold(filepath.Ext(opt.outPath), ".wav") && opt.mode != "decode" {
			opt.outFormat = "wav"
		}
	}
	if !oneOf(opt.outFormat, "text", "wav", "pcm") {
		return fmt.Errorf("unknown --out-format %q (want text, wav or pcm)", opt.outFormat)
	}
	switch {
	case opt.mode == "decode" && opt.outFormat != "text":
		return errors.New("decode produces text; --out-format must be text")
	case opt.mode == "encode" && (opt.inFormat == "wav" || opt.inFormat == "pcm"):
		return errors.New("encode reads text; use decode for audio input")
	case len(opt.text) > 0 && opt.inPath != "":
		return errors.New("give either text arguments or --in, not both")
	}
	if opt.outFormat != "text" && opt.mode == "" {
		opt.mode = "encode"
	}
	return nil
}

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// looksLikeMorseArg reports whether an argument that starts with "-" is
// Morse text ("-.-.", "---", "-- ---") rather than a flag. "-" and "--" keep
// their usual meanings.
func looksLikeMorseArg(a string) bool {
	return a != "-" && a != "--" && strings.HasPrefix(a, "-") && morse.IsMorse(a)
}

func execute(opt *options, stdin io.Reader, stdout, stderr io.Writer) error {
	lang, err := morse.ParseLanguage(opt.lang)
	if err != nil {
		return err
	}
	in, closeIn, err := openInput(opt, stdin)
	if err != nil {
		return err
	}
	defer closeIn()
	inFormat := opt.inFormat
	if inFormat == "auto" && len(opt.text) > 0 {
		inFormat = "text"
	} else if inFormat == "auto" {
		inFormat = sniff(in)
	}
	out, closeOut, err := openOutput(opt, stdout)
	if err != nil {
		return err
	}
	defer closeOut()

	if inFormat == "wav" || inFormat == "pcm" {
		if opt.mode == "encode" {
			return errors.New("input is audio; use decode")
		}
		return decodeAudio(opt, lang, inFormat, in, out, stderr)
	}
	return processText(opt, lang, in, out, stderr)
}

// openInput returns the text arguments, the --in file or stdin.
func openInput(opt *options, stdin io.Reader) (*bufio.Reader, func(), error) {
	switch {
	case len(opt.text) > 0:
		return bufio.NewReader(strings.NewReader(strings.Join(opt.text, " "))), func() {}, nil
	case opt.inPath != "" && opt.inPath != "-":
		f, err := os.Open(opt.inPath)
		if err != nil {
			return nil, nil, err
		}
		return bufio.NewReaderSize(f, 64<<10), func() { f.Close() }, nil
	}
	return bufio.NewReaderSize(stdin, 64<<10), func() {}, nil
}

// openOutput returns the --out file or stdout.
func openOutput(opt *options, stdout io.Writer) (io.Writer, func(), error) {
	if opt.outPath == "" || opt.outPath == "-" {
		return stdout, func() {}, nil
	}
	f, err := os.Create(opt.outPath)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

// processText handles text input: Morse text is decoded, anything else is
// encoded (unless the mode says otherwise).
func processText(opt *options, lang morse.Language, in io.Reader, out, stderr io.Writer) error {
	raw, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return errors.New("input text is not valid UTF-8 (use --in-format pcm or wav for audio)")
	}
	text := string(raw)
	decode := opt.mode == "decode" || (opt.mode == "" && morse.IsMorse(text))
	if !decode {
		return encode(opt, lang, text, out, stderr)
	}
	if opt.verbose {
		reportLanguage(stderr, lang, func() morse.Language { return morse.DetectMorseLanguage(text) })
	}
	plain, err := morse.DecodeText(text, lang)
	if err != nil && !warnPartial(err, stderr) {
		return err
	}
	_, err = fmt.Fprintln(out, plain)
	return err
}

// sniff guesses the input format from the first bytes: RIFF → wav, valid
// printable UTF-8 → text, anything else → raw PCM.
func sniff(r *bufio.Reader) string {
	head, _ := r.Peek(512)
	if len(head) >= 4 && string(head[:4]) == "RIFF" {
		return "wav"
	}
	// tolerate a multi-byte rune cut off by the peek window
	for i := 0; i < 3 && len(head) > 0 && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	if !utf8.Valid(head) {
		return "pcm"
	}
	for _, r := range string(head) {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return "pcm"
		}
	}
	return "text"
}

// reportLanguage prints the alphabet in use; detect runs only for auto.
func reportLanguage(stderr io.Writer, lang morse.Language, detect func() morse.Language) {
	how := "fixed"
	if lang == morse.Auto {
		lang, how = detect(), "detected"
	}
	fmt.Fprintf(stderr, "morse-dsp: language %s (%s)\n", lang.Name(), how)
}

func encode(opt *options, lang morse.Language, text string, out io.Writer, stderr io.Writer) error {
	if opt.verbose && !morse.IsMorse(text) {
		reportLanguage(stderr, lang, func() morse.Language { return morse.DetectLanguage(text) })
	}
	if opt.outFormat == "text" {
		code, err := morse.EncodeText(text, lang)
		warnPartial(err, stderr) // the only possible error: skipped characters
		_, err = fmt.Fprintln(out, code)
		return err
	}
	samples, err := synthesize(opt, lang, text, stderr)
	if err != nil {
		return err
	}
	if opt.outFormat == "wav" {
		return writeBuffered(out, func(w io.Writer) error { return morse.WriteWAV(w, samples, opt.rate) })
	}
	sf, err := morse.ParseSampleFormat(opt.sampleFmt)
	if err != nil {
		return err
	}
	if opt.realtime {
		return writeRealtime(out, samples, opt.rate, sf)
	}
	return writeBuffered(out, func(w io.Writer) error { return morse.WritePCM(w, samples, sf) })
}

// writeBuffered runs write through a 64 KB buffer and flushes it.
func writeBuffered(out io.Writer, write func(io.Writer) error) error {
	bw := bufio.NewWriterSize(out, 64<<10)
	return errors.Join(write(bw), bw.Flush())
}

// synthesize renders text (or Morse text, keyed as written) into samples.
func synthesize(opt *options, lang morse.Language, text string, stderr io.Writer) ([]float32, error) {
	freq, err := strconv.ParseFloat(opt.freq, 64)
	if err != nil {
		return nil, fmt.Errorf("--freq must be a number when generating audio, got %q", opt.freq)
	}
	cfg := morse.EncoderConfig{
		SampleRate: opt.rate,
		WPM:        opt.wpm,
		Frequency:  freq,
		Amplitude:  opt.amp,
		RampMs:     opt.ramp,
		LeadIn:     0.3,
		Tail:       0.3,
		Noise:      opt.set["snr"],
		SNR:        opt.snr,
		Seed:       opt.seed,
		Language:   lang,
	}
	if opt.set["snr"] && !opt.set["seed"] {
		cfg.Seed = uint64(time.Now().UnixNano())
	}
	enc, err := morse.NewEncoder(cfg)
	if err != nil {
		return nil, err
	}
	// Morse text is keyed as written (it always parses: IsMorse checked it);
	// plain text can only fail with skipped characters, which is a warning.
	var samples []float32
	if morse.IsMorse(text) {
		samples, _ = enc.SynthesizeMorse(text)
	} else {
		samples, err = enc.Synthesize(text)
		warnPartial(err, stderr)
	}
	if opt.verbose {
		fmt.Fprintf(stderr, "morse-dsp: %d samples, %.2f s at %d Hz, %.0f WPM, %.0f Hz tone\n",
			len(samples), float64(len(samples))/float64(opt.rate), opt.rate, opt.wpm, freq)
	}
	return samples, nil
}

// writeRealtime writes 20 ms blocks of PCM on the wall clock, like a live
// receiver would.
func writeRealtime(out io.Writer, samples []float32, rate int, sf morse.SampleFormat) error {
	block := max(1, rate/50)
	start := time.Now()
	for i := 0; i < len(samples); i += block {
		end := min(i+block, len(samples))
		if err := morse.WritePCM(out, samples[i:end], sf); err != nil {
			return err
		}
		due := start.Add(time.Duration(float64(end) / float64(rate) * float64(time.Second)))
		time.Sleep(time.Until(due))
	}
	return nil
}

func decodeAudio(opt *options, lang morse.Language, format string, in io.Reader, out, stderr io.Writer) error {
	cfg, err := decoderConfig(opt, lang)
	if err != nil {
		return err
	}
	src, err := openSource(opt, format, in, &cfg, stderr)
	if err != nil {
		return err
	}
	if opt.inPath != "" && opt.inPath != "-" {
		return decodeFile(opt, src, cfg, out, stderr)
	}
	return decodeLive(opt, src, cfg, out, stderr)
}

// decodeFile decodes a recording as a whole, so that the alphabet is chosen
// from all of it.
func decodeFile(opt *options, src morse.SampleSource, cfg morse.DecoderConfig, out, stderr io.Writer) error {
	r, err := morse.DecodeSource(src, cfg)
	if err != nil {
		return err
	}
	if r.Text != "" {
		if _, err := fmt.Fprintln(out, r.Text); err != nil {
			return err
		}
	}
	reportDecode(opt, stderr, r.Frequency, r.WPM, r.Language)
	return nil
}

// decodeLive writes text as soon as it is decoded (stdin may be a receiver).
func decodeLive(opt *options, src morse.SampleSource, cfg morse.DecoderConfig, out, stderr io.Writer) error {
	dec, err := morse.NewDecoder(cfg)
	if err != nil {
		return err
	}
	// track the last byte so that the output ends with exactly one newline
	tw := &tailWriter{w: out}
	err = dec.DecodeFrom(src, tw)
	if err == nil && tw.n > 0 && tw.last != '\n' {
		_, err = io.WriteString(out, "\n")
	}
	if err != nil {
		return err
	}
	reportDecode(opt, stderr, dec.Frequency(), dec.WPM(), dec.Language())
	return nil
}

func reportDecode(opt *options, stderr io.Writer, freq, wpm float64, lang morse.Language) {
	if opt.verbose {
		fmt.Fprintf(stderr, "morse-dsp: tone %.1f Hz, speed ≈ %.1f WPM, language %s\n", freq, wpm, lang.Name())
	}
}

func decoderConfig(opt *options, lang morse.Language) (morse.DecoderConfig, error) {
	cfg := morse.DecoderConfig{
		SampleRate: opt.rate,
		WPM:        opt.wpm,
		Language:   lang,
		SquelchDB:  opt.squelch,
		LineBreak:  opt.lineBreak,
	}
	if strings.EqualFold(opt.freq, "auto") {
		cfg.AutoFrequency = true
		return cfg, nil
	}
	f, err := strconv.ParseFloat(opt.freq, 64)
	if err != nil {
		return cfg, fmt.Errorf("--freq must be a number or auto, got %q", opt.freq)
	}
	cfg.Frequency = f
	return cfg, nil
}

// openSource wraps in as a WAV or raw PCM sample source. For WAV the sample
// rate in cfg is taken from the header.
func openSource(opt *options, format string, in io.Reader, cfg *morse.DecoderConfig, stderr io.Writer) (morse.SampleSource, error) {
	if format != "wav" {
		sf, err := morse.ParseSampleFormat(opt.sampleFmt)
		if err != nil {
			return nil, err
		}
		return morse.NewPCMReader(in, sf), nil
	}
	wr, err := morse.NewWAVReader(in)
	if err != nil {
		return nil, err
	}
	info := wr.Info()
	cfg.SampleRate = info.SampleRate
	if opt.verbose {
		length := "streamed"
		if d := info.Duration(); d >= 0 {
			length = strconv.FormatFloat(d, 'f', 2, 64) + " s"
		}
		fmt.Fprintf(stderr, "morse-dsp: WAV %d Hz, %d ch, %d-bit, %s\n",
			info.SampleRate, info.Channels, info.BitsPerSample, length)
	}
	return wr, nil
}

type tailWriter struct {
	w    io.Writer
	n    int
	last byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		t.n += len(p)
		t.last = p[len(p)-1]
	}
	return t.w.Write(p)
}

// warnPartial prints recoverable codec errors (unsupported characters,
// unknown codes) as warnings and reports whether err was one of them.
func warnPartial(err error, stderr io.Writer) bool {
	var uc *morse.UnknownCharsError
	var uk *morse.UnknownCodesError
	if errors.As(err, &uc) || errors.As(err, &uk) {
		fmt.Fprintln(stderr, "morse-dsp: warning:", strings.TrimPrefix(err.Error(), "morse: "))
		return true
	}
	return false
}
