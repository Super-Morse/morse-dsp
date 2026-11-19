package morse

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

// Edge cases and error paths of the public API.

var errBoom = errors.New("boom")

type failWriter struct{ after int }

func (w *failWriter) Write(p []byte) (int, error) {
	if w.after <= 0 {
		return 0, errBoom
	}
	w.after--
	return len(p), nil
}

type failReader struct{ data []byte }

func (r *failReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, errBoom
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type failSource struct{}

func (failSource) ReadSamples([]float32) (int, error) { return 0, errBoom }

func TestLanguageStrings(t *testing.T) {
	for l, want := range map[Language]string{Auto: "auto", English: "en", Russian: "ru", Ukrainian: "uk", Turkish: "tr", Language(42): "auto"} {
		if got := l.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", l, got, want)
		}
	}
	for l, want := range map[Language]string{Auto: "Auto", Ukrainian: "Ukrainian", German: "German", Language(-1): "Auto"} {
		if got := l.Name(); got != want {
			t.Errorf("%d.Name() = %q, want %q", l, got, want)
		}
	}
	for _, l := range []Language{Auto, Language(42)} {
		if r, ok := Char(".-", l); !ok || r != 'A' {
			t.Errorf("Char with %d should use the international alphabet, got %q %v", l, r, ok)
		}
		if a := Alphabet(l); a["-.--"] != 'Y' {
			t.Errorf("Alphabet(%d) is not the international alphabet", l)
		}
	}
	if _, ok := Code('€', English); ok {
		t.Error("€ has no code")
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&UnknownCharsError{Chars: []rune{'€', '✓'}}, "'€', '✓'"},
		{&UnknownCodesError{Codes: []string{"........"}}, "........"},
		{&SyntaxError{Offset: 3, Char: 'x'}, "'x' at offset 3"},
	}
	for _, tt := range tests {
		if !strings.Contains(tt.err.Error(), tt.want) {
			t.Errorf("%T message %q lacks %q", tt.err, tt.err.Error(), tt.want)
		}
	}
	_, err := DecodeText("........ ........", English) // the same unknown code twice
	var uk *UnknownCodesError
	if !errors.As(err, &uk) || len(uk.Codes) != 1 {
		t.Errorf("duplicate unknown codes should be reported once, got %v", err)
	}
	if _, err := NormalizeMorse("... x"); err == nil {
		t.Error("NormalizeMorse accepted a letter")
	}
}

func TestSampleFormats(t *testing.T) {
	for in, want := range map[string]SampleFormat{"": S16LE, "s16": S16LE, "INT16": S16LE, "f32le": F32LE, "float": F32LE} {
		if got, err := ParseSampleFormat(in); err != nil || got != want {
			t.Errorf("ParseSampleFormat(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseSampleFormat("u8"); err == nil {
		t.Error("u8 should be rejected")
	}
	if S16LE.String() != "s16le" || F32LE.String() != "f32le" {
		t.Error("unexpected SampleFormat names")
	}
}

func TestPCMClampingAndWriteErrors(t *testing.T) {
	var b bytes.Buffer
	if err := WritePCM(&b, []float32{2, -2}, S16LE); err != nil {
		t.Fatal(err)
	}
	if v := int16(binary.LittleEndian.Uint16(b.Bytes())); v != 32767 {
		t.Errorf("+2 clamped to %d", v)
	}
	if v := int16(binary.LittleEndian.Uint16(b.Bytes()[2:])); v != -32768 {
		t.Errorf("-2 clamped to %d", v)
	}
	if err := WritePCM(&failWriter{}, []float32{0}, F32LE); !errors.Is(err, errBoom) {
		t.Errorf("WritePCM error = %v", err)
	}
	if err := WriteWAV(&failWriter{}, []float32{0}, 8000); !errors.Is(err, errBoom) {
		t.Errorf("WriteWAV header error = %v", err)
	}
	if err := WriteWAV(&failWriter{after: 1}, []float32{0}, 8000); !errors.Is(err, errBoom) {
		t.Errorf("WriteWAV data error = %v", err)
	}
	if err := WriteWAV(io.Discard, nil, 0); err == nil {
		t.Error("WriteWAV accepted a zero sample rate")
	}
}

func TestPCMReaderPropagatesErrors(t *testing.T) {
	r := NewPCMReader(&failReader{data: []byte{1, 0, 2}}, S16LE) // one sample + a stray byte
	buf := make([]float32, 4)
	if n, err := r.ReadSamples(buf); n != 1 || err != nil {
		t.Fatalf("first read: %d, %v", n, err)
	}
	if _, err := r.ReadSamples(buf); !errors.Is(err, errBoom) {
		t.Errorf("second read error = %v", err)
	}
	if n, err := r.ReadSamples(nil); n != 0 || err != nil {
		t.Errorf("empty read: %d, %v", n, err)
	}
	if n, err := SamplesFromChan(make(chan float32)).ReadSamples(nil); n != 0 || err != nil {
		t.Errorf("empty chan read: %d, %v", n, err)
	}
}

func TestWAVInfoAndValidation(t *testing.T) {
	data := EncodeWAV(make([]float32, 8000), 8000)
	wr, err := NewWAVReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if info := wr.Info(); info.Frames() != 8000 || info.Duration() != 1 {
		t.Errorf("frames %d, duration %v", info.Frames(), info.Duration())
	}
	if (WAVInfo{DataBytes: -1}).Duration() != -1 {
		t.Error("unknown length should report -1")
	}
	// 32-bit integer PCM
	var s32 []byte
	for _, v := range []int32{0, 1 << 30, -1 << 30} {
		s32 = binary.LittleEndian.AppendUint32(s32, uint32(v))
	}
	got, _, err := DecodeWAV(buildWAV(1, 1, 8000, 32, s32, false, false, uint32(len(s32))))
	if err != nil || len(got) != 3 || got[1] != 0.5 || got[2] != -0.5 {
		t.Errorf("pcm32: %v, %v", got, err)
	}
	bad := map[string][]byte{
		"channels":  buildWAV(1, 0, 8000, 16, nil, false, false, 0),
		"rate":      buildWAV(1, 1, 100, 16, nil, false, false, 0),
		"bits":      buildWAV(1, 1, 8000, 12, nil, false, false, 0),
		"float16":   buildWAV(3, 1, 8000, 16, nil, false, false, 0),
		"short fmt": []byte("RIFF\x00\x00\x00\x00WAVEfmt \x04\x00\x00\x00\x01\x00\x01\x00"),
		"cut fmt":   []byte("RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00"),
		"cut chunk": []byte("RIFF\x00\x00\x00\x00WAVELIST\x10\x00\x00\x00\x01"),
		"no fmt":    []byte("RIFF\x00\x00\x00\x00WAVEdata\x00\x00\x00\x00"),
	}
	for name, d := range bad {
		if _, _, err := DecodeWAV(d); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestEncoderEdges(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{LeadIn: 0.3, Tail: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	if c := enc.Config(); c.SampleRate != DefaultSampleRate || c.WPM != DefaultWPM || c.Frequency != DefaultFrequency {
		t.Errorf("defaults not applied: %+v", c)
	}
	if runs, _ := enc.Timeline(""); len(runs) != 1 || runs[0].On || runs[0].Dur != 0.6 {
		t.Errorf("empty timeline = %+v, want one 0.6 s gap", runs)
	}
	if _, err := enc.SynthesizeMorse(".- x"); err == nil {
		t.Error("SynthesizeMorse accepted a letter")
	}
	s, err := enc.Synthesize("E€")
	var uc *UnknownCharsError
	if !errors.As(err, &uc) || len(s) == 0 {
		t.Errorf("Synthesize with an unknown char: %d samples, %v", len(s), err)
	}
	if _, err := NewEncoder(EncoderConfig{Noise: true, SNR: math.NaN()}); err == nil {
		t.Error("NaN SNR accepted")
	}
}

func TestEstimatorEdges(t *testing.T) {
	if _, ok := EstimateFrequency(make([]float32, 100), 8000, 300, 1500); ok {
		t.Error("estimate from too little audio")
	}
	f := newFreqEstimator(8000, 300, 9000) // max above Nyquist is clamped
	if hi := float64(f.hi) * 8000 / float64(f.nfft); hi > 4000 {
		t.Errorf("search band ends at %.0f Hz", hi)
	}
}

func TestDecoderEdges(t *testing.T) {
	if _, err := DecodeSamples(nil, DecoderConfig{SampleRate: 10}); err == nil {
		t.Error("DecodeSamples accepted an invalid config")
	}
	// fewer samples than one Goertzel window: nothing to decode, no panic
	d, _ := NewDecoder(DecoderConfig{})
	if out := d.Process(make([]float32, 10)) + d.Flush(); out != "" {
		t.Errorf("tiny input decoded as %q", out)
	}
	// automatic search before any audio
	auto, _ := NewDecoder(DecoderConfig{AutoFrequency: true})
	if auto.Frequency() != 0 {
		t.Error("frequency should be unknown before the search")
	}
	auto.Process(whiteNoise(8000, 0.1, 1))
	if out := auto.Flush(); out != "" || auto.Frequency() != DefaultFrequency {
		t.Errorf("noise only: %q at %.0f Hz (want fallback %v Hz)", out, auto.Frequency(), DefaultFrequency)
	}
}

func TestDecoderAutoFrequencyAfterLongNoise(t *testing.T) {
	// streamed in 0.5 s chunks, the search buffer overflows (30 s) and drops
	// old noise before the transmission starts
	sigma := DefaultAmplitude / math.Sqrt2 * math.Pow(10, -0.5) // same level as SNR 10 dB
	all := append(whiteNoise(35*8000, sigma, 2), synth(t, "CQ DE TEST", synthOpts{freq: 820, noise: true, snr: 10, seed: 3})...)
	d, _ := NewDecoder(DecoderConfig{AutoFrequency: true, Record: true})
	var got strings.Builder
	for i := 0; i < len(all); i += 4000 {
		got.WriteString(d.Process(all[i:min(i+4000, len(all))]))
	}
	got.WriteString(d.Flush())
	if got.String() != "CQ DE TEST" || math.Abs(d.Frequency()-820) > 3 {
		t.Errorf("got %q at %.1f Hz", got.String(), d.Frequency())
	}
	if total := TimelineDuration(d.Keying()); math.Abs(total-float64(len(all))/8000) > 0.1 {
		t.Errorf("keying covers %.2f s of %.2f s", total, float64(len(all))/8000)
	}
}

func TestDecoderShortTransmissions(t *testing.T) {
	// "IT" has only 4 elements: the pause after it must end acquisition
	sigma := DefaultAmplitude / math.Sqrt2 * math.Pow(10, -0.75)
	var all []float32
	all = append(all, synth(t, "IT", synthOpts{noise: true, snr: 15, seed: 7})...)
	all = append(all, whiteNoise(12000, sigma, 8)...)
	all = append(all, synth(t, "TEST", synthOpts{noise: true, snr: 15, seed: 9})...)
	d, _ := NewDecoder(DefaultDecoderConfig())
	var got strings.Builder
	for i := 0; i < len(all); i += 800 {
		got.WriteString(d.Process(all[i:min(i+800, len(all))]))
	}
	got.WriteString(d.Flush())
	if got.String() != "IT TEST" {
		t.Errorf("got %q", got.String())
	}
}

func TestReadWAVPropagatesErrors(t *testing.T) {
	data := EncodeWAV(make([]float32, 100), 8000)
	if _, _, err := ReadWAV(&failReader{data: data[:60]}); !errors.Is(err, errBoom) {
		t.Errorf("ReadWAV error = %v", err)
	}
}

func TestDecoderIgnoresCarrier(t *testing.T) {
	enc, _ := NewEncoder(EncoderConfig{LeadIn: 0.5, Tail: 0.5, Noise: true, SNR: 15, Seed: 4})
	runs := []KeyRun{{On: false, Dur: 0.5}, {On: true, Dur: 3}, {On: false, Dur: 1}}
	carrier := enc.Render(runs)
	text := synth(t, "TEST K", synthOpts{noise: true, snr: 15, seed: 5})
	r, _ := DecodeSamples(append(carrier, text...), DefaultDecoderConfig())
	if r.Text != "TEST K" {
		t.Errorf("after a 3 s carrier got %q", r.Text)
	}
}

func TestDecoderRunawaySymbol(t *testing.T) {
	// 20 dots with element gaps: no letter has that many elements
	enc, _ := NewEncoder(EncoderConfig{LeadIn: 0.3, Tail: 0.3})
	s, _ := enc.SynthesizeMorse("-- --- " + strings.Repeat(".", 20))
	r, _ := DecodeSamples(s, DefaultDecoderConfig())
	if !strings.ContainsRune(r.Text, UnknownRune) {
		t.Errorf("runaway symbol decoded as %q", r.Text)
	}
}

func TestDecodeFromErrors(t *testing.T) {
	d, _ := NewDecoder(DefaultDecoderConfig())
	if err := d.DecodeFrom(failSource{}, io.Discard); !errors.Is(err, errBoom) {
		t.Errorf("source error = %v", err)
	}
	s := synth(t, "TEST TEST TEST", synthOpts{})
	d2, _ := NewDecoder(DefaultDecoderConfig())
	if err := d2.DecodeReader(bytes.NewReader(toS16LE(s)), S16LE, &failWriter{}); !errors.Is(err, errBoom) {
		t.Errorf("writer error = %v", err)
	}
}

func TestDecoderStreamEndsMidMark(t *testing.T) {
	// no tail: the stream stops while the decoder still hears the last dash
	enc, _ := NewEncoder(EncoderConfig{LeadIn: 0.3, Noise: true, SNR: 15, Seed: 10})
	s, _ := enc.Synthesize("TEST")
	if r, _ := DecodeSamples(s, DefaultDecoderConfig()); r.Text != "TEST" {
		t.Errorf("DecodeSamples: %q", r.Text)
	}
	ch := make(chan float32, len(s))
	for _, v := range s {
		ch <- v
	}
	close(ch)
	d, _ := NewDecoder(DefaultDecoderConfig())
	var got strings.Builder
	for frag := range d.DecodeChan(ch) {
		got.WriteString(frag)
	}
	if got.String() != "TEST" {
		t.Errorf("DecodeChan: %q", got.String())
	}
}

func TestDecoderForgetsSignalLevelAfterLongSilence(t *testing.T) {
	// a strong station, 20 s of noise, then a station 15 dB weaker: the
	// remembered level must decay, or the weak one stays below threshold
	sigma := DefaultAmplitude / math.Sqrt2 * math.Pow(10, -2.0)
	strong := synth(t, "QRZ", synthOpts{noise: true, snr: 40, seed: 11})
	enc, _ := NewEncoder(EncoderConfig{Amplitude: DefaultAmplitude / 5.6, LeadIn: 0.3, Tail: 0.3})
	weak, _ := enc.Synthesize("TEST")
	weak = addNoiseTo(weak, sigma, 12)
	all := append(append(strong, whiteNoise(20*8000, sigma, 13)...), weak...)
	if r, _ := DecodeSamples(all, DefaultDecoderConfig()); r.Text != "QRZ TEST" {
		t.Errorf("got %q", r.Text)
	}
}

func addNoiseTo(s []float32, sigma float64, seed uint64) []float32 {
	n := whiteNoise(len(s), sigma, seed)
	for i := range s {
		s[i] += n[i]
	}
	return s
}
