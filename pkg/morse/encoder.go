package morse

import (
	"errors"
	"math"
	"math/rand/v2"
)

// Default signal parameters shared by the encoder and the decoder.
const (
	DefaultSampleRate = 8000  // Hz
	DefaultWPM        = 20.0  // words per minute, PARIS standard
	DefaultFrequency  = 700.0 // Hz
	DefaultAmplitude  = 0.5   // linear, full scale = 1
	DefaultRampMs     = 5.0   // raised-cosine rise/fall time
)

// UnitDuration returns the length of one Morse unit (one dot) in seconds for
// the PARIS standard: 1.2 / WPM.
func UnitDuration(wpm float64) float64 { return 1.2 / wpm }

// KeyRun is an interval of constant key state: tone on (mark) or off (space).
type KeyRun struct {
	On  bool
	Dur float64 // seconds
}

// EncoderConfig controls audio synthesis. Start from DefaultEncoderConfig;
// zero SampleRate, WPM, Frequency or Amplitude are replaced by defaults, the
// remaining fields are used as given.
type EncoderConfig struct {
	SampleRate int      // Hz
	WPM        float64  // speed, PARIS words per minute
	Frequency  float64  // tone frequency, Hz
	Amplitude  float64  // peak amplitude of the tone, 0..1
	RampMs     float64  // raised-cosine rise/fall time in ms (anti-click)
	LeadIn     float64  // silence before the first element, seconds
	Tail       float64  // silence after the last element, seconds
	Language   Language // Cyrillic variant for «И» (see EncodeText)

	// Noise adds white Gaussian noise at SNR dB. SNR is the ratio of the tone
	// power during key-down (A²/2) to the noise power over the whole sampled
	// band 0..SampleRate/2.
	Noise bool
	SNR   float64
	Seed  uint64 // noise seed; equal seeds give identical noise
}

// DefaultEncoderConfig returns 20 WPM, 700 Hz, 8 kHz, 5 ms ramps, 0.3 s of
// silence on both ends and no noise.
func DefaultEncoderConfig() EncoderConfig {
	return EncoderConfig{
		SampleRate: DefaultSampleRate,
		WPM:        DefaultWPM,
		Frequency:  DefaultFrequency,
		Amplitude:  DefaultAmplitude,
		RampMs:     DefaultRampMs,
		LeadIn:     0.3,
		Tail:       0.3,
	}
}

// Encoder synthesizes keyed sine-wave CW audio.
type Encoder struct {
	cfg EncoderConfig
}

// NewEncoder validates cfg and returns an Encoder.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = DefaultSampleRate
	}
	if cfg.WPM == 0 {
		cfg.WPM = DefaultWPM
	}
	if cfg.Frequency == 0 {
		cfg.Frequency = DefaultFrequency
	}
	if cfg.Amplitude == 0 {
		cfg.Amplitude = DefaultAmplitude
	}
	switch {
	case cfg.SampleRate < 1000 || cfg.SampleRate > 384000:
		return nil, errors.New("morse: sample rate must be 1000..384000 Hz")
	case !(cfg.WPM >= 1 && cfg.WPM <= 100):
		return nil, errors.New("morse: WPM must be 1..100")
	case !(cfg.Frequency > 0 && cfg.Frequency < float64(cfg.SampleRate)/2):
		return nil, errors.New("morse: tone frequency must be between 0 and half the sample rate")
	case !(cfg.Amplitude > 0 && cfg.Amplitude <= 1):
		return nil, errors.New("morse: amplitude must be in (0, 1]")
	case cfg.RampMs < 0 || cfg.LeadIn < 0 || cfg.Tail < 0:
		return nil, errors.New("morse: ramp, lead-in and tail must not be negative")
	case cfg.Noise && math.IsNaN(cfg.SNR):
		return nil, errors.New("morse: SNR is NaN")
	}
	return &Encoder{cfg: cfg}, nil
}

// Config returns the effective configuration.
func (e *Encoder) Config() EncoderConfig { return e.cfg }

// Synthesize converts plain text into audio samples in [-1, 1].
// Unsupported characters are skipped and reported via *UnknownCharsError, as
// in EncodeText; the samples are valid in that case too.
func (e *Encoder) Synthesize(text string) ([]float32, error) {
	code, err := EncodeText(text, e.cfg.Language) // at most *UnknownCharsError
	runs, _ := e.Timeline(code)                   // canonical Morse always parses
	return e.Render(runs), err
}

// SynthesizeMorse keys Morse text (see DecodeText for accepted notation)
// directly into audio. Unknown codes are keyed as written.
func (e *Encoder) SynthesizeMorse(code string) ([]float32, error) {
	runs, err := e.Timeline(code)
	if err != nil {
		return nil, err
	}
	return e.Render(runs), nil
}

// Timeline returns the key-down/key-up sequence for Morse text using PARIS
// timing (dot 1, dash 3, intra-letter gap 1, letter gap 3, word gap 7 units),
// framed by the configured lead-in and tail silence.
func (e *Encoder) Timeline(code string) ([]KeyRun, error) {
	words, err := splitMorse(code)
	if err != nil {
		return nil, err
	}
	u := UnitDuration(e.cfg.WPM)
	var tl timeline
	tl.add(false, e.cfg.LeadIn)
	for wi, word := range words {
		if wi > 0 {
			tl.add(false, 7*u)
		}
		for li, letter := range word {
			if li > 0 {
				tl.add(false, 3*u)
			}
			tl.addLetter(letter, u)
		}
	}
	tl.add(false, e.cfg.Tail)
	return tl.runs, nil
}

// timeline accumulates key runs, merging neighbours with the same state.
type timeline struct{ runs []KeyRun }

func (t *timeline) add(on bool, d float64) {
	if d <= 0 {
		return
	}
	if n := len(t.runs); n > 0 && t.runs[n-1].On == on {
		t.runs[n-1].Dur += d
		return
	}
	t.runs = append(t.runs, KeyRun{On: on, Dur: d})
}

// addLetter keys one letter code ("." and "-") with 1-unit inner gaps.
func (t *timeline) addLetter(code string, u float64) {
	for i := 0; i < len(code); i++ {
		if i > 0 {
			t.add(false, u)
		}
		if code[i] == '.' {
			t.add(true, u)
		} else {
			t.add(true, 3*u)
		}
	}
}

// Render turns a key timeline into audio: a continuous-phase sine whose marks
// are shaped by raised-cosine ramps, plus optional white noise. Samples are
// clamped to [-1, 1].
func (e *Encoder) Render(runs []KeyRun) []float32 {
	fs := float64(e.cfg.SampleRate)
	out := make([]float32, int(math.Round(TimelineDuration(runs)*fs)))
	t := 0.0
	for _, r := range runs {
		start := int(math.Round(t * fs))
		t += r.Dur
		end := min(int(math.Round(t*fs)), len(out))
		if r.On && end > start {
			e.renderMark(out, start, end)
		}
	}
	if e.cfg.Noise {
		e.addNoise(out)
	}
	for i, v := range out {
		out[i] = max(-1, min(1, v))
	}
	return out
}

// renderMark writes one key-down of samples [start, end): the carrier keeps a
// continuous phase (as a real transmitter does) and the envelope rises and
// falls with raised-cosine ramps.
func (e *Encoder) renderMark(out []float32, start, end int) {
	fs := float64(e.cfg.SampleRate)
	omega := 2 * math.Pi * e.cfg.Frequency / fs
	length := end - start
	ramp := math.Min(e.cfg.RampMs/1000*fs, float64(length)/2) // in samples
	for i := 0; i < length; i++ {
		edge := math.Min(float64(i)+0.5, float64(length-i)-0.5) // distance to the nearer edge
		env := 1.0
		if edge < ramp {
			env = 0.5 * (1 - math.Cos(math.Pi*edge/ramp))
		}
		n := start + i
		out[n] = float32(e.cfg.Amplitude * env * math.Sin(omega*float64(n)))
	}
}

// addNoise adds white Gaussian noise at the configured SNR.
func (e *Encoder) addNoise(out []float32) {
	sigma := e.cfg.Amplitude / math.Sqrt2 * math.Pow(10, -e.cfg.SNR/20)
	rng := rand.New(rand.NewPCG(e.cfg.Seed, e.cfg.Seed^0x9e3779b97f4a7c15))
	for i := range out {
		out[i] += float32(sigma * rng.NormFloat64())
	}
}

// TimelineDuration returns the total length of runs in seconds.
func TimelineDuration(runs []KeyRun) float64 {
	d := 0.0
	for _, r := range runs {
		d += r.Dur
	}
	return d
}
