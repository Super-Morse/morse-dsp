package morse

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"testing/iotest"
)

// ---- helpers ----------------------------------------------------------------

type synthOpts struct {
	wpm, freq, snr float64
	noise          bool
	seed           uint64
	rate           int
	lang           Language
}

func synth(t *testing.T, text string, o synthOpts) []float32 {
	t.Helper()
	cfg := DefaultEncoderConfig()
	if o.wpm != 0 {
		cfg.WPM = o.wpm
	}
	if o.freq != 0 {
		cfg.Frequency = o.freq
	}
	if o.rate != 0 {
		cfg.SampleRate = o.rate
	}
	cfg.Noise, cfg.SNR, cfg.Seed, cfg.Language = o.noise, o.snr, o.seed, o.lang
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := enc.Synthesize(text)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func whiteNoise(n int, sigma float64, seed uint64) []float32 {
	r := rand.New(rand.NewPCG(seed, 99))
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(sigma * r.NormFloat64())
	}
	return out
}

func toS16LE(samples []float32) []byte {
	var b bytes.Buffer
	if err := WritePCM(&b, samples, S16LE); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// chunkReader returns data in random, often odd-sized pieces, like a pipe fed
// by another process would.
type chunkReader struct {
	data []byte
	rng  *rand.Rand
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(c.data), 1+c.rng.IntN(1500))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// ---- alphabet & text codec ----------------------------------------------------

func TestAlphabetCodesAreUniquePerLanguage(t *testing.T) {
	for lang := English; lang < numLanguages; lang++ {
		seen := map[string]rune{}
		for code, r := range Alphabet(lang) {
			if prev, dup := seen[code]; dup {
				t.Errorf("%v: code %q used by %q and %q", lang, code, prev, r)
			}
			seen[code] = r
			c, ok := Code(r, lang)
			if !ok || c != code {
				t.Errorf("%v: Code(%q) = %q, %v; want %q", lang, r, c, ok, code)
			}
		}
		p := profileOf(lang)
		if want := len(scriptLetters(p.Script)) + 10 + 18 + len(uniqueCodes(p.Letters)); len(seen) != want {
			t.Errorf("%v: table size %d, want %d", lang, len(seen), want)
		}
	}
}

func uniqueCodes(ls []Letter) map[string]bool {
	m := map[string]bool{}
	for _, l := range ls {
		m[l.Code] = true
	}
	return m
}

func TestEncodeText(t *testing.T) {
	tests := []struct {
		in   string
		lang Language
		want string
	}{
		{"SOS", Auto, "... --- ..."},
		{"sos", English, "... --- ..."},
		{"Hello World", Auto, ".... . .-.. .-.. --- / .-- --- .-. .-.. -.."},
		{"  CQ\tDE\n\nUR5ABC  ", Auto, "-.-. --.- / -.. . / ..- .-. ..... .- -... -.-."},
		{"ПРИВІТ МИР", Auto, ".--. .-. -.-- .-- .. - / -- -.-- .-."}, // І: Ukrainian И
		{"ПРИВІТ МИР", English, ".--. .-. -.-- .-- .. - / -- -.-- .-."},
		{"ЧТО ТЫ ДЕЛАЕШЬ", Auto, "---. - --- / - -.-- / -.. . .-.. .- . ---- -..-"}, // Ы: Russian
		{"МИР ВАМ", Auto, "-- -.-- .-. / .-- .- --"},                                // ambiguous: Ukrainian
		{"МИР ВАМ", Russian, "-- .. .-. / .-- .- --"},                               // forced
		{"Ёж", Auto, ". ...-"},
		{"Їжак, ґанок, п’ять, ЄС", Auto, ".---. ...- .- -.- --..-- / --. .- -. --- -.- --..-- / .--. .----. .-.- - -..- --..-- / ..-.. ..."},
		{"Grüße aus München", Auto, "--. .-. ..-- ...--.. . / .- ..- ... / -- ..-- -. -.-. .... . -."},
		{"ÇA VA À PARIS, ÉTÉ", Auto, "-.-.. .- / ...- .- / .--.- / .--. .- .-. .. ... --..-- / ..-.. - ..-.."},
		{"MAÑANA", Auto, "-- .- --.-- .- -. .-"},
		{"ŁÓDŹ, ŻÓŁW, ŚĆ", Auto, ".-..- ---. -.. --..-. --..-- / --..- ---. .-..- .-- --..-- / ...-... -.-.."},
		{"SKÅL, SMØR, ÆBLE", Auto, "... -.- .--.- .-.. --..-- / ... -- ---. .-. --..-- / .-.- -... .-.. ."},
		{"istanbul, şehir", Auto, ".-..- ... - .- -. -... ..- .-.. --..-- / .--.. . .... .-..- .-."}, // Turkish casing: i → İ
		{"ĞÜÇÖ", Turkish, "--.-. ..-- -.-.. ---."},
		{"73!", Auto, "--... ...-- -.-.--"},
		{"", Auto, ""},
	}
	for _, tt := range tests {
		got, err := EncodeText(tt.in, tt.lang)
		if err != nil || got != tt.want {
			t.Errorf("EncodeText(%q, %v) = %q, %v; want %q", tt.in, tt.lang, got, err, tt.want)
		}
	}
}

func TestEncodeTextUnknownCharacters(t *testing.T) {
	got, err := EncodeText("A€B ✓ C€", Auto)
	if got != ".- -... / -.-." {
		t.Errorf("got %q", got)
	}
	var unk *UnknownCharsError
	if !errors.As(err, &unk) || string(unk.Chars) != "€✓" {
		t.Fatalf("want UnknownCharsError{€✓}, got %v", err)
	}
}

func TestDecodeText(t *testing.T) {
	tests := []struct {
		in   string
		lang Language
		want string
	}{
		{"... --- ...", Auto, "SOS"},
		{".... . .-.. .-.. --- / .-- --- .-. .-.. -..", Auto, "HELLO WORLD"},
		{".... . .-.. .-.. ---   .-- --- .-. .-.. -..", English, "HELLO WORLD"}, // 3 spaces
		{".... .|.-- ---", English, "HE WO"},
		{"·−·· ·−", English, "LA"},               // typographic dots and dashes
		{"..- ._", English, "UA"},                // underscore as dash
		{"  / ... /  ", English, "S"},            // stray separators
		{"-- .. .-. / -.--", Russian, "МИР Ы"},   // same codes, other alphabets
		{"-- .. .-. / -.--", Ukrainian, "МІР И"}, //
		{"-- .. .-. / -.--", English, "MIR Y"},   //
		{".--. .-. -.-- .-- .. - / -.. .-. ..- ...- . / .-.- -.-", Auto, "ПРИВІТ ДРУЖЕ ЯК"},
		{"---. - --- / - -.-- / -.. . .-.. .- . ---- -..-", Auto, "ЧТО ТЫ ДЕЛАЕШЬ"},
		{"--. .-. ..-- ...--.. . / .- ..- ... / -- ..-- -. -.-. .... . -.", Auto, "GRÜẞE AUS MÜNCHEN"},
		{"-.-. --.-\n-.. .", Auto, "CQ DE"}, // newline separates words
		{"", Auto, ""},
	}
	for _, tt := range tests {
		got, err := DecodeText(tt.in, tt.lang)
		if err != nil || got != tt.want {
			t.Errorf("DecodeText(%q, %v) = %q, %v; want %q", tt.in, tt.lang, got, err, tt.want)
		}
	}
}

func TestDecodeTextErrors(t *testing.T) {
	got, err := DecodeText("... ........ ...", English)
	var unk *UnknownCodesError
	if got != "S*S" || !errors.As(err, &unk) || len(unk.Codes) != 1 || unk.Codes[0] != "........" {
		t.Errorf("unknown code: got %q, %v", got, err)
	}
	_, err = DecodeText("... abc", Auto)
	var syn *SyntaxError
	if !errors.As(err, &syn) || syn.Char != 'a' || syn.Offset != 4 {
		t.Errorf("syntax: got %v", err)
	}
}

func TestTextRoundTrip(t *testing.T) {
	tests := []struct {
		in   string
		lang Language
	}{
		{"The quick brown fox jumps over the lazy dog 0123456789", English},
		{"Hello, World? (5/9) = \"OK\" & 73! a@b.c $10 + x_y - z; k: 'q'", English},
		{"Съешь же ещё этих мягких французских булок, да выпей чаю", Russian},
		{"Эй, жлоб! Где туз? Прячь юных съёмщиц в шкаф.", Russian},
		{"Чуєш їх, доцю, га? Кумедна ж ти, прощайся без ґольфів!", Ukrainian},
		{"Жебракують філософи при ґанку церкви в Гадячі, ще й шатро їхнє п’яне знаємо.", Ukrainian},
		{"Victor jagt zwölf Boxkämpfer quer über den großen Sylter Deich", German},
		{"À Noël, l'élève français a mangé des crêpes très chaudes près de la fenêtre", French},
		{"El pingüino Wenceslao hizo kilómetros bajo exhaustiva lluvia y frío, añoraba a su querido cachorro", Spanish},
		{"Pchnąć w tę łódź jeża lub ośm skrzyń fig", Polish},
		{"Flygande bäckasiner söka hwila på mjuka tuvor", Swedish},
		{"Pijamalı hasta yağız şoföre çabucak güvendi", Turkish},
	}
	for _, tt := range tests {
		code, err := EncodeText(tt.in, tt.lang)
		if err != nil {
			t.Fatalf("EncodeText(%q): %v", tt.in, err)
		}
		got, err := DecodeText(code, tt.lang)
		if err != nil {
			t.Fatalf("DecodeText: %v", err)
		}
		if want := NormalizeText(tt.in); got != want {
			t.Errorf("round trip:\n got  %q\n want %q", got, want)
		}
		if auto, _ := DecodeText(code, Auto); auto != got {
			t.Errorf("%v pangram with Auto:\n got  %q\n want %q", tt.lang, auto, got)
		}
		norm, err := NormalizeMorse(strings.ReplaceAll(strings.ReplaceAll(code, " / ", "   "), "-", "_"))
		if err != nil || norm != code {
			t.Errorf("NormalizeMorse = %q, %v; want %q", norm, err, code)
		}
	}
}

func TestNormalizeText(t *testing.T) {
	for in, want := range map[string]string{
		"  ёлка   € hi\tthere ": "ЕЛКА HI THERE",
		"Ґанок":                 "ГАНОК",
		"smør og æble":          "SMÖR OG ÄBLE",
		"Straße, Fußball":       "STRAẞE, FUẞBALL",
		"où êtes-vous":          "OU ETES-VOUS",
		"ılık şehir":            "ILIK ŞEHİR",
	} {
		if got := NormalizeText(in); got != want {
			t.Errorf("NormalizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsMorse(t *testing.T) {
	yes := []string{"... --- ...", ".-", "-", ".... . / .-- ---", "·−·· ·−", "..- ._", " .\n- "}
	no := []string{"", "   ", "/", "SOS", "HELLO", "... --- ... x", "..- ._ 📟 .", "5"}
	for _, s := range yes {
		if !IsMorse(s) {
			t.Errorf("IsMorse(%q) = false", s)
		}
	}
	for _, s := range no {
		if IsMorse(s) {
			t.Errorf("IsMorse(%q) = true", s)
		}
	}
}

func TestParseLanguage(t *testing.T) {
	for in, want := range map[string]Language{
		"": Auto, "auto": Auto, " AUTO ": Auto, "en": English, "EN": English, "latin": English, "ru": Russian, "Russian": Russian,
		"ua": Ukrainian, "uk": Ukrainian, "UA": Ukrainian, "de": German, "Deutsch": German, "fr": French, "es": Spanish,
		"español": Spanish, "pl": Polish, "sv": Swedish, "nordic": Swedish, "tr": Turkish, "türkçe": Turkish,
	} {
		if got, err := ParseLanguage(in); err != nil || got != want {
			t.Errorf("ParseLanguage(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLanguage("xx"); err == nil || !strings.Contains(err.Error(), "auto, en, ua") {
		t.Errorf("expected error listing the languages, got %v", err)
	}
}

// ---- encoder ---------------------------------------------------------------

func TestEncoderTimelineParis(t *testing.T) {
	cfg := DefaultEncoderConfig()
	cfg.LeadIn, cfg.Tail = 0, 0
	enc, _ := NewEncoder(cfg)
	code, _ := EncodeText("PARIS PARIS", English)
	runs, err := enc.Timeline(code)
	if err != nil {
		t.Fatal(err)
	}
	// "PARIS" is 43 units, the word gap 7: PARIS standard = 50 units per word.
	u := UnitDuration(20)
	if got, want := TimelineDuration(runs), 93*u; math.Abs(got-want) > 1e-9 {
		t.Errorf("duration %.4f s, want %.4f s", got, want)
	}
	if !runs[0].On || !runs[len(runs)-1].On {
		t.Error("timeline without lead-in/tail should start and end with a mark")
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].On == runs[i-1].On {
			t.Fatal("adjacent runs with equal state")
		}
	}
	samples := enc.Render(runs)
	if want := int(math.Round(93 * u * 8000)); len(samples) != want {
		t.Errorf("rendered %d samples, want %d", len(samples), want)
	}
}

func TestEncoderRampsPreventClicks(t *testing.T) {
	cfg := DefaultEncoderConfig()
	enc, _ := NewEncoder(cfg)
	runs, _ := enc.Timeline("- .")
	s := enc.Render(runs)
	fs := float64(cfg.SampleRate)
	ramp := int(cfg.RampMs / 1000 * fs)
	t0 := 0.0
	for _, r := range runs {
		start, end := int(math.Round(t0*fs)), int(math.Round((t0+r.Dur)*fs))
		t0 += r.Dur
		if !r.On {
			for _, v := range s[start:end] {
				if v != 0 {
					t.Fatal("non-zero sample during key-up without noise")
				}
			}
			continue
		}
		peak := func(a, b int) (m float64) {
			for _, v := range s[a:b] {
				m = math.Max(m, math.Abs(float64(v)))
			}
			return m
		}
		edge := ramp / 8
		if p := peak(start, start+edge); p > 0.05*cfg.Amplitude {
			t.Errorf("attack starts too abruptly: %.3f in first %d samples", p, edge)
		}
		if p := peak(end-edge, end); p > 0.05*cfg.Amplitude {
			t.Errorf("release ends too abruptly: %.3f in last %d samples", p, edge)
		}
		if p := peak(start+ramp, end-ramp); p < 0.98*cfg.Amplitude {
			t.Errorf("plateau peak %.3f, want ≈ %.2f", p, cfg.Amplitude)
		}
	}
}

func TestEncoderNoiseHasRequestedSNR(t *testing.T) {
	clean := synth(t, "PARIS PARIS", synthOpts{})
	for _, snr := range []float64{20, 10, 0} {
		noisy := synth(t, "PARIS PARIS", synthOpts{noise: true, snr: snr, seed: 1})
		var pn float64
		for i := range clean {
			d := float64(noisy[i] - clean[i])
			pn += d * d
		}
		pn /= float64(len(clean))
		ps := DefaultAmplitude * DefaultAmplitude / 2
		if got := 10 * math.Log10(ps/pn); math.Abs(got-snr) > 0.2 {
			t.Errorf("SNR %.2f dB, want %.0f dB", got, snr)
		}
	}
	a := synth(t, "E", synthOpts{noise: true, snr: 10, seed: 5})
	b := synth(t, "E", synthOpts{noise: true, snr: 10, seed: 5})
	if !slicesEqual(a, b) {
		t.Error("same seed must give identical noise")
	}
}

func TestEncoderValidation(t *testing.T) {
	bad := []EncoderConfig{
		{SampleRate: 100},
		{WPM: 500},
		{Frequency: 5000, SampleRate: 8000},
		{Amplitude: 2},
		{RampMs: -1},
	}
	for _, c := range bad {
		if _, err := NewEncoder(c); err == nil {
			t.Errorf("NewEncoder(%+v) accepted invalid config", c)
		}
	}
}

func slicesEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- WAV & PCM -------------------------------------------------------------

func TestWAVRoundTrip(t *testing.T) {
	in := synth(t, "CQ", synthOpts{noise: true, snr: 5, seed: 2, rate: 11025})
	data := EncodeWAV(in, 11025)
	if len(data) != 44+2*len(in) || string(data[:4]) != "RIFF" {
		t.Fatalf("bad WAV layout: %d bytes", len(data))
	}
	out, rate, err := DecodeWAV(data)
	if err != nil || rate != 11025 || len(out) != len(in) {
		t.Fatalf("DecodeWAV: rate %d, %d samples, %v", rate, len(out), err)
	}
	for i := range in {
		if math.Abs(float64(in[i]-out[i])) > 2.0/32767 {
			t.Fatalf("sample %d: %v != %v", i, out[i], in[i])
		}
	}
}

// buildWAV assembles a WAV file by hand to exercise the reader.
func buildWAV(format uint16, channels, rate, bits int, data []byte, extensible, extraChunk bool, dataSize uint32) []byte {
	var fmtChunk bytes.Buffer
	tag := format
	if extensible {
		tag = wavFormatExtensible
	}
	le := binary.LittleEndian
	w := func(v any) { _ = binary.Write(&fmtChunk, le, v) }
	w(tag)
	w(uint16(channels))
	w(uint32(rate))
	w(uint32(rate * channels * bits / 8))
	w(uint16(channels * bits / 8))
	w(uint16(bits))
	if extensible {
		w(uint16(22))
		w(uint16(bits))
		w(uint32(0))
		w(format)
		fmtChunk.Write(make([]byte, 14))
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, le, uint32(0))
	b.WriteString("WAVE")
	if extraChunk {
		b.WriteString("LIST")
		_ = binary.Write(&b, le, uint32(3))
		b.Write([]byte{1, 2, 3, 0}) // odd size + pad byte
	}
	b.WriteString("fmt ")
	_ = binary.Write(&b, le, uint32(fmtChunk.Len()))
	b.Write(fmtChunk.Bytes())
	b.WriteString("data")
	_ = binary.Write(&b, le, dataSize)
	b.Write(data)
	return b.Bytes()
}

func TestWAVReaderFormats(t *testing.T) {
	want := []float32{0, 0.5, -0.5, 0.25}
	le := binary.LittleEndian
	var s8, s16st, s24, f32, f64 []byte
	for _, v := range want {
		s8 = append(s8, byte(int(v*128)+128))
		s16st = le.AppendUint16(s16st, uint16(int16(v*32768)))
		s16st = le.AppendUint16(s16st, uint16(int16(v*32768))) // identical right channel
		x := int32(v * 8388608)
		s24 = append(s24, byte(x), byte(x>>8), byte(x>>16))
		f32 = le.AppendUint32(f32, math.Float32bits(v))
		f64 = le.AppendUint64(f64, math.Float64bits(float64(v)))
	}
	cases := map[string][]byte{
		"pcm8":            buildWAV(1, 1, 8000, 8, s8, false, false, uint32(len(s8))),
		"pcm16 stereo":    buildWAV(1, 2, 8000, 16, s16st, false, true, uint32(len(s16st))),
		"pcm24 ext":       buildWAV(1, 1, 8000, 24, s24, true, false, uint32(len(s24))),
		"float32":         buildWAV(3, 1, 8000, 32, f32, false, true, uint32(len(f32))),
		"float64 ext":     buildWAV(3, 1, 8000, 64, f64, true, false, uint32(len(f64))),
		"streamed (size)": buildWAV(3, 1, 8000, 32, f32, false, false, 0xFFFFFFFF),
	}
	for name, data := range cases {
		got, rate, err := ReadWAV(iotest.HalfReader(bytes.NewReader(data)))
		if err != nil || rate != 8000 || len(got) != len(want) {
			t.Errorf("%s: %v, rate %d, %d samples", name, err, rate, len(got))
			continue
		}
		for i := range want {
			if math.Abs(float64(got[i]-want[i])) > 1.0/120 {
				t.Errorf("%s: sample %d = %v, want %v", name, i, got[i], want[i])
			}
		}
	}
}

func TestWAVReaderErrors(t *testing.T) {
	bad := map[string][]byte{
		"empty":   nil,
		"not wav": []byte("ID3\x03\x00\x00\x00\x00\x00\x00\x00\x00 mp3 data"),
		"alaw":    buildWAV(6, 1, 8000, 8, []byte{1, 2}, false, false, 2),
		"no data": []byte("RIFF\x00\x00\x00\x00WAVE"),
	}
	for name, data := range bad {
		if _, _, err := DecodeWAV(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPCMReaderHandlesPartialReads(t *testing.T) {
	in := []float32{0, 0.25, -0.5, 0.999, -1}
	for _, f := range []SampleFormat{S16LE, F32LE} {
		var b bytes.Buffer
		_ = WritePCM(&b, in, f)
		r := NewPCMReader(iotest.OneByteReader(&b), f)
		var got []float32
		buf := make([]float32, 3)
		for {
			n, err := r.ReadSamples(buf)
			got = append(got, buf[:n]...)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(got) != len(in) {
			t.Fatalf("%v: got %d samples", f, len(got))
		}
		for i := range in {
			if math.Abs(float64(got[i]-in[i])) > 2.0/32767 {
				t.Errorf("%v: sample %d = %v, want %v", f, i, got[i], in[i])
			}
		}
	}
}

// ---- tone estimation ----------------------------------------------------------

func TestEstimateFrequency(t *testing.T) {
	for _, f := range []float64{450, 700, 913, 1200} {
		s := synth(t, "CQ CQ TEST", synthOpts{freq: f, noise: true, snr: 0, seed: 4})
		got, ok := EstimateFrequency(s, 8000, DefaultMinFrequency, DefaultMaxFrequency)
		if !ok || math.Abs(got-f) > 3 {
			t.Errorf("tone %v Hz: estimated %.1f Hz (ok=%v)", f, got, ok)
		}
	}
	if f, ok := EstimateFrequency(whiteNoise(3*8000, 0.2, 1), 8000, 300, 1500); ok {
		t.Errorf("found a tone at %.1f Hz in pure noise", f)
	}
}

// ---- decoder: end-to-end -------------------------------------------------------

var e2eTexts = []string{
	"CQ CQ DE MORSE DSP TEST K",
	"THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG",
	"SOS 73 0123456789",
	"HELLO, WORLD? 5/9 = OK",
}

// TestEndToEndNoisyStreaming is the acceptance test: text → audio with white
// noise at 10 dB SNR at 15 and 25 WPM → streaming decode → exact text.
// The decoder always starts from its default 20 WPM guess, so both speeds
// exercise the automatic speed tracking. Every case is decoded twice: from an
// io.Reader delivering raw s16le PCM in random odd-sized chunks (the SDR pipe
// scenario) and from a <-chan float32.
func TestEndToEndNoisyStreaming(t *testing.T) {
	for _, wpm := range []float64{15, 25} {
		for i, text := range e2eTexts {
			for seed := uint64(1); seed <= 3; seed++ {
				samples := synth(t, text, synthOpts{wpm: wpm, noise: true, snr: 10, seed: seed*100 + uint64(i)})
				want := NormalizeText(text)

				dec, err := NewDecoder(DefaultDecoderConfig())
				if err != nil {
					t.Fatal(err)
				}
				var out strings.Builder
				src := &chunkReader{data: toS16LE(samples), rng: rand.New(rand.NewPCG(seed, 1))}
				if err := dec.DecodeReader(src, S16LE, &out); err != nil {
					t.Fatal(err)
				}
				if out.String() != want {
					t.Errorf("reader, %v WPM, seed %d:\n got  %q\n want %q", wpm, seed, out.String(), want)
				}
				if est := dec.WPM(); math.Abs(est-wpm)/wpm > 0.08 {
					t.Errorf("%v WPM: speed estimate %.1f", wpm, est)
				}

				dec2, _ := NewDecoder(DefaultDecoderConfig())
				ch := make(chan float32, 256)
				go func() {
					for _, s := range samples {
						ch <- s
					}
					close(ch)
				}()
				var got strings.Builder
				for frag := range dec2.DecodeChan(ch) {
					got.WriteString(frag)
				}
				if got.String() != want {
					t.Errorf("chan, %v WPM, seed %d:\n got  %q\n want %q", wpm, seed, got.String(), want)
				}
			}
		}
	}
}

func TestDecoderCyrillic(t *testing.T) {
	cases := []struct {
		lang Language
		text string
	}{
		{Ukrainian, "ПРИВІТ, ЕФІРЕ! 73 ЇЖАК ЄНОТ ДИМ ЗВ'ЯЗОК"},
		{Russian, "ЭТО ПРОВЕРКА СВЯЗИ 73 ЩУКА ЭХО ЮЛА"},
	}
	for _, c := range cases {
		for _, wpm := range []float64{15, 25} {
			s := synth(t, c.text, synthOpts{wpm: wpm, noise: true, snr: 10, seed: 3, lang: c.lang})
			for _, lang := range []Language{c.lang, Auto} {
				cfg := DefaultDecoderConfig()
				cfg.Language = lang
				r, err := DecodeSamples(s, cfg)
				if err != nil || r.Text != c.text || r.Language != c.lang {
					t.Errorf("%v as %v, %v WPM: got %q (%v), %v", c.lang, lang, wpm, r.Text, r.Language, err)
				}
			}
		}
	}
}

func TestDecoderAdaptsToSpeed(t *testing.T) {
	text := "PARIS CQ DE TEST 599"
	for _, wpm := range []float64{10, 12, 18, 30, 35, 40} {
		s := synth(t, text, synthOpts{wpm: wpm, noise: true, snr: 15, seed: uint64(wpm)})
		r, err := DecodeSamples(s, DefaultDecoderConfig()) // always starts at 20 WPM
		if err != nil || r.Text != text {
			t.Errorf("%v WPM: got %q, %v", wpm, r.Text, err)
		}
		if math.Abs(r.WPM-wpm)/wpm > 0.1 {
			t.Errorf("%v WPM: estimate %.1f", wpm, r.WPM)
		}
	}
}

func TestDecoderTracksSpeedChangesBetweenTransmissions(t *testing.T) {
	var all []float32
	var want []string
	for i, wpm := range []float64{14, 28, 20} {
		all = append(all, synth(t, "CQ CQ DE TEST K", synthOpts{wpm: wpm, noise: true, snr: 10, seed: uint64(i)})...)
		all = append(all, whiteNoise(int(2.5*8000), DefaultAmplitude/math.Sqrt2*math.Pow(10, -0.5), uint64(i))...)
		want = append(want, "CQ CQ DE TEST K")
	}
	r, _ := DecodeSamples(all, DefaultDecoderConfig())
	if w := strings.Join(want, " "); r.Text != w {
		t.Errorf("got  %q\nwant %q", r.Text, w)
	}
}

func TestDecoderIgnoresPureNoise(t *testing.T) {
	for seed := uint64(1); seed <= 3; seed++ {
		for _, sigma := range []float64{1e-4, 0.05, 0.3} {
			r, _ := DecodeSamples(whiteNoise(30*8000, sigma, seed), DefaultDecoderConfig())
			if r.Text != "" {
				t.Errorf("noise σ=%v seed %d produced %q", sigma, seed, r.Text)
			}
		}
	}
	if r, _ := DecodeSamples(make([]float32, 5*8000), DefaultDecoderConfig()); r.Text != "" {
		t.Errorf("digital silence produced %q", r.Text)
	}
}

func TestDecoderFrequencyToleranceAndAutoTune(t *testing.T) {
	text := "CQ DE MORSE DSP"
	for _, f := range []float64{640, 760} { // ±60 Hz: inside the Goertzel pass band
		s := synth(t, text, synthOpts{freq: f, noise: true, snr: 10, seed: 1})
		if r, _ := DecodeSamples(s, DefaultDecoderConfig()); r.Text != text {
			t.Errorf("tone %v Hz, decoder at 700 Hz: got %q", f, r.Text)
		}
	}
	for _, f := range []float64{430, 980, 1300} { // far off: needs the automatic search
		s := synth(t, text, synthOpts{freq: f, noise: true, snr: 10, seed: 2})
		cfg := DefaultDecoderConfig()
		if r, _ := DecodeSamples(s, cfg); r.Text == text {
			t.Errorf("tone %v Hz should not decode at 700 Hz", f)
		}
		cfg.AutoFrequency = true
		r, _ := DecodeSamples(s, cfg)
		if r.Text != text || math.Abs(r.Frequency-f) > 3 {
			t.Errorf("auto, tone %v Hz: got %q at %.1f Hz", f, r.Text, r.Frequency)
		}
	}
}

func TestDecoderSampleRates(t *testing.T) {
	text := "CQ CQ DE TEST"
	for _, rate := range []int{6000, 12000, 22050, 44100, 48000} {
		s := synth(t, text, synthOpts{rate: rate, wpm: 25, noise: true, snr: 10, seed: 7})
		cfg := DefaultDecoderConfig()
		cfg.SampleRate = rate
		if r, _ := DecodeSamples(s, cfg); r.Text != text {
			t.Errorf("%d Hz: got %q", rate, r.Text)
		}
	}
}

func TestDecoderChunkingDoesNotMatter(t *testing.T) {
	s := synth(t, "THE QUICK BROWN FOX 123", synthOpts{wpm: 22, noise: true, snr: 6, seed: 9})
	whole, _ := DecodeSamples(s, DefaultDecoderConfig())
	rng := rand.New(rand.NewPCG(3, 4))
	d, _ := NewDecoder(DefaultDecoderConfig())
	var got strings.Builder
	for i := 0; i < len(s); {
		n := min(len(s)-i, rng.IntN(900))
		got.WriteString(d.Process(s[i : i+n]))
		i += n
	}
	got.WriteString(d.Flush())
	if got.String() != whole.Text || whole.Text == "" {
		t.Errorf("chunked %q != whole %q", got.String(), whole.Text)
	}
}

func TestDecoderIsRealTime(t *testing.T) {
	text := "CQ CQ CQ DE MORSE DSP TEST TEST K"
	s := synth(t, text, synthOpts{wpm: 20, noise: true, snr: 10, seed: 1})
	d, _ := NewDecoder(DefaultDecoderConfig())
	var got strings.Builder
	firstAt, wordsBeforeEnd := -1, 0
	const chunk = 160 // 20 ms
	for i := 0; i < len(s); i += chunk {
		out := d.Process(s[i:min(i+chunk, len(s))])
		if out != "" && firstAt < 0 {
			firstAt = i
		}
		got.WriteString(out)
	}
	wordsBeforeEnd = len(strings.Fields(got.String()))
	got.WriteString(d.Flush())
	if got.String() != text {
		t.Fatalf("got %q", got.String())
	}
	if firstAt < 0 || float64(firstAt) > 0.2*float64(len(s)) {
		t.Errorf("first character only after %.1f of %.1f s", float64(firstAt)/8000, float64(len(s))/8000)
	}
	if wordsBeforeEnd != len(strings.Fields(text)) {
		t.Errorf("only %d words were emitted before Flush", wordsBeforeEnd)
	}
}

func TestDecoderLineBreaks(t *testing.T) {
	a := synth(t, "CQ", synthOpts{noise: true, snr: 10, seed: 1})
	b := synth(t, "TEST", synthOpts{noise: true, snr: 10, seed: 2})
	gap := whiteNoise(3*8000, DefaultAmplitude/math.Sqrt2*math.Pow(10, -0.5), 3)
	all := append(append(append([]float32{}, a...), gap...), b...)
	cfg := DefaultDecoderConfig()
	cfg.LineBreak = 1.5
	r, _ := DecodeSamples(all, cfg)
	if r.Text != "CQ\nTEST" || r.Morse != "-.-. --.-\n- . ... -" {
		t.Errorf("got %q / %q", r.Text, r.Morse)
	}
}

func TestDecoderTranscriptAndKeyingReplay(t *testing.T) {
	text := "MORSE DSP 73"
	s := synth(t, text, synthOpts{wpm: 18, noise: true, snr: 8, seed: 5})
	r, err := DecodeSamples(s, DefaultDecoderConfig())
	if err != nil || r.Text != text {
		t.Fatalf("got %q, %v", r.Text, err)
	}
	if want, _ := EncodeText(text, English); r.Morse != want {
		t.Errorf("morse transcript %q, want %q", r.Morse, want)
	}
	if d := TimelineDuration(r.Keying); math.Abs(d-float64(len(s))/8000) > 0.05 {
		t.Errorf("keying covers %.3f s of %.3f s", d, float64(len(s))/8000)
	}
	// re-key the detected timeline as a clean tone: it must decode identically
	enc, _ := NewEncoder(EncoderConfig{LeadIn: 0, Tail: 0, RampMs: 5})
	clean := enc.Render(r.Keying)
	r2, _ := DecodeSamples(clean, DefaultDecoderConfig())
	if r2.Text != text {
		t.Errorf("clean replay decoded as %q", r2.Text)
	}
}

func TestDecoderConfigValidation(t *testing.T) {
	bad := []DecoderConfig{
		{SampleRate: 500},
		{Frequency: 4500},
		{WPM: 1},
		{WindowMs: 1000},
		{HopMs: 50, WindowMs: 10},
		{SquelchDB: -3},
		{LineBreak: -1},
		{AutoFrequency: true, MinFrequency: 900, MaxFrequency: 800},
		{Language: Language(42)},
	}
	for _, c := range bad {
		if _, err := NewDecoder(c); err == nil {
			t.Errorf("NewDecoder(%+v) accepted invalid config", c)
		}
	}
}

func BenchmarkDecoder8kHz(b *testing.B) {
	enc, _ := NewEncoder(EncoderConfig{Noise: true, SNR: 10, WPM: 25})
	s, _ := enc.Synthesize("THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG")
	b.SetBytes(int64(len(s) * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, _ := NewDecoder(DefaultDecoderConfig())
		d.Process(s)
		d.Flush()
	}
	b.ReportMetric(float64(len(s))/8000*float64(b.N)/b.Elapsed().Seconds(), "audio-s/s")
}
