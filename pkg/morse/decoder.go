package morse

import (
	"errors"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Decoder defaults.
const (
	DefaultWindowMs  = 16.0 // Goertzel window length
	DefaultHopMs     = 4.0  // envelope frame step
	DefaultSquelchDB = 12.0 // minimum tone level above the noise floor
)

// DecoderConfig controls the streaming decoder. Zero numeric fields are
// replaced by defaults (see DefaultDecoderConfig).
type DecoderConfig struct {
	SampleRate int     // input sample rate, Hz
	Frequency  float64 // tone to listen to, Hz (fallback when AutoFrequency finds nothing)

	// AutoFrequency searches [MinFrequency, MaxFrequency] for the strongest
	// tone before decoding. In streaming use the first ~1.5 s are buffered
	// for the search (and decoded right after), so output starts late once.
	AutoFrequency bool
	MinFrequency  float64
	MaxFrequency  float64

	WPM float64 // initial speed guess; the decoder adapts from there

	// Language is the alphabet used to turn codes into characters. Auto (the
	// zero value) detects it as the stream goes; see Decoder.
	Language Language

	WindowMs  float64 // Goertzel window, ms: longer = narrower filter, slower response
	HopMs     float64 // step between envelope frames, ms
	SquelchDB float64 // a tone must exceed the noise floor by this much to key

	// LineBreak emits "\n" after this many seconds of silence (0 disables).
	// Useful for streams: each transmission ends up on its own line.
	LineBreak float64

	// Record keeps a Morse transcript (Morse) and the detected key timeline
	// (Keying). Memory grows with the stream length, so leave it off for
	// endless streams.
	Record bool
}

// DefaultDecoderConfig returns 8 kHz, 700 Hz, 20 WPM and automatic language
// detection.
func DefaultDecoderConfig() DecoderConfig {
	return DecoderConfig{
		SampleRate:   DefaultSampleRate,
		Frequency:    DefaultFrequency,
		MinFrequency: DefaultMinFrequency,
		MaxFrequency: DefaultMaxFrequency,
		WPM:          DefaultWPM,
		Language:     Auto,
		WindowMs:     DefaultWindowMs,
		HopMs:        DefaultHopMs,
		SquelchDB:    DefaultSquelchDB,
	}
}

// DecodeResult is the outcome of DecodeSamples.
type DecodeResult struct {
	Text      string   // decoded text
	Morse     string   // what was heard, as Morse text
	Language  Language // alphabet used (the detected one with Auto)
	WPM       float64  // final speed estimate
	Frequency float64  // tone frequency that was decoded
	Keying    []KeyRun // detected key timeline (for re-synthesis of a clean signal)
}

// DecodeSamples decodes a complete buffer of samples. With Auto the whole
// recording is available, so the alphabet is chosen from all of it at once
// (a stream has to decide as it goes).
func DecodeSamples(samples []float32, cfg DecoderConfig) (DecodeResult, error) {
	cfg.Record = true
	d, err := NewDecoder(cfg)
	if err != nil {
		return DecodeResult{}, err
	}
	return d.result(d.Process(samples) + d.Flush()), nil
}

// DecodeSource decodes src until io.EOF and returns the same result as
// DecodeSamples, without keeping the audio in memory: use it for recordings
// (files) rather than live streams.
func DecodeSource(src SampleSource, cfg DecoderConfig) (DecodeResult, error) {
	cfg.Record = true
	d, err := NewDecoder(cfg)
	if err != nil {
		return DecodeResult{}, err
	}
	var text strings.Builder
	if err := d.DecodeFrom(src, &text); err != nil {
		return DecodeResult{}, err
	}
	return d.result(text.String()), nil
}

// result collects the outcome of a recorded decode. With Auto the text is
// decoded again from the transcript in the alphabet that fits it as a whole.
func (d *Decoder) result(text string) DecodeResult {
	r := DecodeResult{Text: text, Morse: d.Morse(), Language: d.Language(),
		WPM: d.WPM(), Frequency: d.Frequency(), Keying: d.Keying()}
	if d.auto {
		r.Language = DetectMorseLanguage(r.Morse)
		r.Text = decodeLines(r.Morse, r.Language)
	}
	return r
}

// decodeLines decodes a transcript line by line, keeping the line breaks.
func decodeLines(transcript string, lang Language) string {
	lines := strings.Split(transcript, "\n")
	for i, l := range lines {
		lines[i], _ = DecodeText(l, lang) // unknown codes become UnknownRune
	}
	return strings.Join(lines, "\n")
}

// Decoder is a streaming CW decoder:
//
//	samples → Goertzel filter (Hann window, sliding) → envelope in dB
//	        → adaptive threshold (noise-floor median, signal peak tracker,
//	          squelch, hysteresis) → debounced key state
//	        → mark/space durations → adaptive unit (WPM) estimate
//	        → dots/dashes, letter and word gaps → text
//
// Feed it with Process (or DecodeFrom / DecodeReader / DecodeChan) and call
// Flush at the end of the stream. Characters are emitted as soon as the gap
// after them is long enough, so output is real-time. A Decoder is not safe for
// concurrent use.
//
// With Language Auto the alphabet is detected as the stream goes, without
// delaying the output: characters are decoded in the active alphabet
// (English at first), and the choice is revised at every word gap. Only codes
// that alphabets disagree on beyond the script (Я/Ä/Ą, Ч/Ö/Ó, Ш, Ї) wait for
// the end of their word until an alphabet has been confirmed. The confirmed
// alphabet is kept for the session, so later short replies stay in it; it is
// forgotten after 30 s without signal or on ResetLanguage.
type Decoder struct {
	cfg  DecoderConfig
	fs   float64
	auto bool        // detect the alphabet
	lang langTracker // its state

	// automatic tone search
	tuned     bool
	afc       *freqEstimator
	afBuf     []float32
	afNext    int
	afDropped float64

	// sliding Goertzel tone detector
	freq     float64
	n, hop   int
	win      []float64
	coeff    float64
	norm     float64 // Goertzel power → amplitude²
	ring     []float32
	ringPos  int
	ringFill int
	sinceHop int
	frameDur float64

	// adaptive levels, dBFS
	warm        []float64
	warmFrames  int
	warmed      bool
	replaying   bool
	noiseRing   []float64
	noiseSorted []float64
	noisePos    int
	noiseN      int
	noiseTick   int
	noiseDB     float64
	sigDB       float64
	haveSig     bool
	idle        int
	raw         bool
	aSigDown    float64
	aFade       float64
	holdFrames  int
	decayFrame  float64

	// debounced key state
	state    bool
	run      int // frames in the current confirmed run
	pend     int // frames in a tentative opposite state
	seenMark bool

	speed   speedEstimator
	cfgUnit float64 // unit length implied by cfg.WPM
	// acquisition: the first elements of each transmission are held back
	// until the speed estimate is reliable, then replayed
	acq     bool
	acqRuns []timing

	// character assembly
	marks        []float64
	pendingSpace bool
	sinceSpace   bool
	sinceLine    bool
	lineStart    bool
	out          []byte
	morse        []byte
	keying       []KeyRun
}

// NewDecoder validates cfg and returns a Decoder.
func NewDecoder(cfg DecoderConfig) (*Decoder, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	fs := float64(cfg.SampleRate)
	d := &Decoder{cfg: cfg, fs: fs, lineStart: true, acq: true, auto: cfg.Language == Auto, lang: newLangTracker()}
	d.n = max(16, int(math.Round(cfg.WindowMs/1000*fs)))
	d.hop = min(d.n, max(1, int(math.Round(cfg.HopMs/1000*fs))))
	d.frameDur = float64(d.hop) / fs
	d.win = hann(d.n)
	d.ring = make([]float32, d.n)

	d.warmFrames = max(8, int(math.Round(0.4/d.frameDur)))
	nr := max(32, int(math.Round(1.0/d.frameDur)))
	d.noiseRing = make([]float64, nr)
	d.noiseSorted = make([]float64, nr)
	d.aSigDown = d.frameDur / 0.25
	d.aFade = d.frameDur / 0.15
	d.holdFrames = int(math.Round(2.0 / d.frameDur))
	d.decayFrame = 3.0 * d.frameDur // 3 dB/s once the band has been quiet for a while
	d.cfgUnit = UnitDuration(cfg.WPM)
	d.speed = newSpeedEstimator(d.cfgUnit)

	if cfg.AutoFrequency {
		d.afc = newFreqEstimator(fs, cfg.MinFrequency, cfg.MaxFrequency)
		d.afNext = int(1.5 * fs)
	} else {
		d.tune(cfg.Frequency)
	}
	return d, nil
}

// withDefaults replaces zero numeric fields with the defaults.
func (c DecoderConfig) withDefaults() DecoderConfig {
	def := DefaultDecoderConfig()
	orDefault := func(v *float64, d float64) {
		if *v == 0 {
			*v = d
		}
	}
	if c.SampleRate == 0 {
		c.SampleRate = def.SampleRate
	}
	orDefault(&c.Frequency, def.Frequency)
	orDefault(&c.MinFrequency, def.MinFrequency)
	orDefault(&c.MaxFrequency, def.MaxFrequency)
	orDefault(&c.WPM, def.WPM)
	orDefault(&c.WindowMs, def.WindowMs)
	orDefault(&c.HopMs, def.HopMs)
	orDefault(&c.SquelchDB, def.SquelchDB)
	return c
}

func (c DecoderConfig) validate() error {
	fs := float64(c.SampleRate)
	switch {
	case c.SampleRate < 1000 || c.SampleRate > 384000:
		return errors.New("morse: sample rate must be 1000..384000 Hz")
	case !(c.Frequency > 0 && c.Frequency < fs/2):
		return errors.New("morse: tone frequency must be between 0 and half the sample rate")
	case c.AutoFrequency && !(c.MinFrequency > 0 && c.MinFrequency < c.MaxFrequency && c.MinFrequency < fs/2):
		return errors.New("morse: invalid automatic frequency search range")
	case !(c.WPM >= 3 && c.WPM <= 80):
		return errors.New("morse: WPM must be 3..80")
	case !(c.WindowMs >= 2 && c.WindowMs <= 200):
		return errors.New("morse: window must be 2..200 ms")
	case !(c.HopMs > 0 && c.HopMs <= c.WindowMs):
		return errors.New("morse: hop must be positive and not longer than the window")
	case !(c.SquelchDB > 0 && c.SquelchDB < 60):
		return errors.New("morse: squelch must be 0..60 dB")
	case c.LineBreak < 0:
		return errors.New("morse: line break interval must not be negative")
	case c.Language != Auto && profileOf(c.Language) == nil:
		return errors.New("morse: unknown language")
	}
	return nil
}

func (d *Decoder) tune(f float64) {
	d.freq = f
	d.coeff = 2 * math.Cos(2*math.Pi*f/d.fs)
	sum := 0.0
	for _, w := range d.win {
		sum += w
	}
	d.norm = 4 / (sum * sum) // a tone of amplitude A yields |X| = A·Σw/2
	d.tuned = true
}

// Process feeds samples to the decoder and returns the text decoded since the
// previous call (often empty).
func (d *Decoder) Process(samples []float32) string {
	if !d.tuned {
		d.autoFeed(samples)
	} else {
		for _, s := range samples {
			d.push(s)
		}
	}
	return d.take()
}

// Flush finishes the stream: the character in progress is emitted and the
// remaining text returned.
func (d *Decoder) Flush() string {
	if !d.tuned {
		f, ok := d.afc.estimate()
		if !ok {
			f = d.cfg.Frequency
		}
		d.startTuned(f)
	}
	if !d.warmed {
		d.finishWarmup()
	}
	if d.state {
		d.run += d.pend
		d.endRun(true, d.run)
	} else if d.cfg.Record && d.run+d.pend > 0 {
		d.addKeying(false, float64(d.run+d.pend)*d.frameDur)
	}
	d.state, d.raw, d.run, d.pend = false, false, 0, 0
	if d.acq && len(d.acqRuns) > 0 {
		d.finishAcq()
	}
	if len(d.marks) > 0 {
		d.emitChar()
	}
	if d.auto {
		d.out = append(d.out, d.lang.flush()...)
	}
	return d.take()
}

// Language returns the alphabet in use: the configured one, or with Auto the
// one currently chosen by the detector.
func (d *Decoder) Language() Language {
	if d.auto {
		return d.lang.active
	}
	return d.cfg.Language
}

// ResetLanguage makes an Auto decoder forget the detected session language,
// as if the stream started afresh (for example after retuning to another
// station). Characters still waiting for their word to end are output first.
func (d *Decoder) ResetLanguage() {
	if d.auto {
		d.out = append(d.out, d.lang.flush()...)
		d.lang.forget()
	}
}

// WPM returns the current speed estimate.
func (d *Decoder) WPM() float64 { return 1.2 / d.speed.unit }

// Frequency returns the tone frequency being decoded, or 0 while the
// automatic search is still collecting audio.
func (d *Decoder) Frequency() float64 {
	if !d.tuned {
		return 0
	}
	return d.freq
}

// Morse returns the transcript of received Morse (only with Record).
func (d *Decoder) Morse() string { return string(d.morse) }

// Keying returns the detected key timeline (only with Record).
func (d *Decoder) Keying() []KeyRun { return append([]KeyRun(nil), d.keying...) }

// DecodeFrom decodes src until io.EOF, writing text to w as soon as it is
// decoded, then flushes.
func (d *Decoder) DecodeFrom(src SampleSource, w io.Writer) error {
	buf := make([]float32, 2048)
	for {
		n, err := src.ReadSamples(buf)
		if n > 0 {
			if s := d.Process(buf[:n]); s != "" {
				if _, werr := io.WriteString(w, s); werr != nil {
					return werr
				}
			}
		}
		if err == io.EOF {
			_, werr := io.WriteString(w, d.Flush())
			return werr
		}
		if err != nil {
			return err
		}
	}
}

// DecodeReader decodes a raw mono PCM stream (e.g. stdin fed by an SDR
// demodulator) in real time, writing text to w as it is decoded.
func (d *Decoder) DecodeReader(r io.Reader, f SampleFormat, w io.Writer) error {
	return d.DecodeFrom(NewPCMReader(r, f), w)
}

// DecodeChan consumes samples from in and delivers decoded text fragments on
// the returned channel, which is closed after in is closed and the decoder
// flushed.
func (d *Decoder) DecodeChan(in <-chan float32) <-chan string {
	out := make(chan string, 64)
	go func() {
		defer close(out)
		src := SamplesFromChan(in)
		buf := make([]float32, 1024)
		for {
			n, err := src.ReadSamples(buf)
			if s := d.Process(buf[:n]); s != "" {
				out <- s
			}
			if err != nil {
				if s := d.Flush(); s != "" {
					out <- s
				}
				return
			}
		}
	}()
	return out
}

// ---- automatic tone search ----------------------------------------------

func (d *Decoder) autoFeed(samples []float32) {
	d.afBuf = append(d.afBuf, samples...)
	d.afc.feed(samples)
	if len(d.afBuf) < d.afNext {
		return
	}
	if f, ok := d.afc.estimate(); ok {
		d.startTuned(f)
		return
	}
	d.afNext = len(d.afBuf) + int(0.5*d.fs)
	if limit := int(30 * d.fs); len(d.afBuf) > limit {
		// nothing but noise so far: keep only the last few seconds
		drop := len(d.afBuf) - int(5*d.fs)
		d.afDropped += float64(drop) / d.fs
		d.afBuf = append(d.afBuf[:0], d.afBuf[drop:]...)
		d.afNext -= drop
	}
}

func (d *Decoder) startTuned(f float64) {
	d.tune(f)
	if d.cfg.Record && d.afDropped > 0 {
		d.addKeying(false, d.afDropped)
	}
	buf := d.afBuf
	d.afBuf, d.afc = nil, nil
	for _, s := range buf {
		d.push(s)
	}
}

// ---- tone detector --------------------------------------------------------

func (d *Decoder) push(s float32) {
	d.ring[d.ringPos] = s
	d.ringPos++
	if d.ringPos == d.n {
		d.ringPos = 0
	}
	if d.ringFill < d.n {
		d.ringFill++
		if d.ringFill < d.n {
			return
		}
	} else {
		d.sinceHop++
		if d.sinceHop < d.hop {
			return
		}
	}
	d.sinceHop = 0
	d.frame(d.goertzel())
}

// goertzel runs the Goertzel recurrence over the Hann-windowed ring buffer
// (oldest sample first) and returns the tone amplitude in dBFS.
func (d *Decoder) goertzel() float64 {
	var s1, s2 float64
	c, w, k := d.coeff, d.win, 0
	for _, x := range d.ring[d.ringPos:] {
		s0 := float64(x)*w[k] + c*s1 - s2
		s2, s1 = s1, s0
		k++
	}
	for _, x := range d.ring[:d.ringPos] {
		s0 := float64(x)*w[k] + c*s1 - s2
		s2, s1 = s1, s0
		k++
	}
	p := math.Max(0, s1*s1+s2*s2-c*s1*s2) // rounding can make it slightly negative
	return 10 * math.Log10(p*d.norm+1e-20)
}

// ---- adaptive threshold ---------------------------------------------------

func (d *Decoder) frame(db float64) {
	if !d.warmed {
		d.warm = append(d.warm, db)
		if len(d.warm) >= d.warmFrames {
			d.finishWarmup()
		}
		return
	}
	d.step(db)
}

// finishWarmup seeds the noise floor from the first frames (a one-time
// 0.4 s look-ahead) and then replays them through the detector. Only frames
// within the spread of noise above the 10th percentile are used, so a
// transmission that is already running when the stream starts does not
// inflate the floor.
func (d *Decoder) finishWarmup() {
	d.warmed = true
	w := d.warm
	d.warm = nil
	if len(w) == 0 {
		d.noiseDB = -120
		return
	}
	sorted := append([]float64(nil), w...)
	sort.Float64s(sorted)
	limit := sorted[len(sorted)/10] + 13
	for _, v := range w {
		if v <= limit {
			d.pushNoise(v)
		}
	}
	d.replaying = true
	for _, v := range w {
		d.step(v)
	}
	d.replaying = false
}

func (d *Decoder) step(db float64) {
	on, off := d.thresholds()
	if d.raw {
		d.raw = db > off
	} else {
		d.raw = db > on
	}
	d.track(db)
	d.debounce()
}

// thresholds places the decision level halfway between the noise and signal
// amplitudes (in the linear domain, so that mark edges are not biased by the
// filter's smearing), never lower than the squelch level, with hysteresis.
func (d *Decoder) thresholds() (on, off float64) {
	sq := d.noiseDB + d.cfg.SquelchDB
	if !d.haveSig {
		return sq, sq - 1
	}
	mid := 20 * math.Log10((math.Pow(10, d.noiseDB/20)+math.Pow(10, d.sigDB/20))/2)
	h := math.Min(1.5, 0.1*(d.sigDB-d.noiseDB))
	return math.Max(mid+h, sq), mid - h
}

func (d *Decoder) track(db float64) {
	if d.raw {
		d.idle = 0
		switch {
		case !d.haveSig:
			d.sigDB, d.haveSig = db, true
		case db > d.sigDB: // fast attack towards the mark plateau
			d.sigDB += 0.5 * (db - d.sigDB)
		default:
			d.sigDB += d.aSigDown * (db - d.sigDB)
		}
		return
	}
	if !d.replaying {
		d.pushNoise(db)
	}
	if !d.haveSig {
		return
	}
	d.idle++
	if db > d.noiseDB+d.cfg.SquelchDB && db < d.sigDB {
		// a tone that no longer reaches the threshold: the signal is fading
		d.sigDB += d.aFade * (db - d.sigDB)
	}
	if d.idle > d.holdFrames {
		d.sigDB -= d.decayFrame
	}
	if d.sigDB < d.noiseDB+d.cfg.SquelchDB {
		d.haveSig = false
	}
}

// pushNoise adds a key-up frame to a ~1 s ring whose median is the noise
// floor. The median ignores the occasional filter-edge frame and adapts in
// both directions within about half a second.
func (d *Decoder) pushNoise(db float64) {
	d.noiseRing[d.noisePos] = db
	d.noisePos++
	if d.noisePos == len(d.noiseRing) {
		d.noisePos = 0
	}
	if d.noiseN < len(d.noiseRing) {
		d.noiseN++
	}
	d.noiseTick++
	if d.noiseTick >= 4 || d.noiseN < 16 {
		d.noiseTick = 0
		s := d.noiseSorted[:d.noiseN]
		copy(s, d.noiseRing[:d.noiseN])
		sort.Float64s(s)
		d.noiseDB = s[len(s)/2]
	}
}

// ---- key state and timing -----------------------------------------------

// debounce confirms a key change only after it persisted for ~0.3 unit; the
// confirmed run is back-dated to where the change began.
func (d *Decoder) debounce() {
	if d.raw != d.state {
		d.pend++
		if d.pend >= d.minRun() {
			d.endRun(d.state, d.run)
			d.state = d.raw
			d.run, d.pend = d.pend, 0
		}
	} else {
		d.run += d.pend + 1
		d.pend = 0
	}
	if d.state {
		if float64(d.run)*d.frameDur > math.Max(10*d.speed.unit, 1.0) {
			d.carrier()
		}
	} else {
		d.checkGap()
	}
}

// minRun is the glitch filter length: ~0.3 unit. While acquiring, the unit
// estimate may still be off by 2-3x, so it is capped by the configured speed
// to avoid filtering out genuine fast dots.
func (d *Decoder) minRun() int {
	u := d.speed.unit
	if d.acq {
		u = math.Min(u, d.cfgUnit)
	}
	return max(2, int(0.3*u/d.frameDur+0.5))
}

// carrier handles a key-down far longer than any Morse element: a steady
// carrier, a tuning signal or a jump of the noise floor. Its level becomes
// the new noise floor and the half-received character is dropped.
func (d *Decoder) carrier() {
	if d.cfg.Record {
		d.addKeying(false, float64(d.run)*d.frameDur)
	}
	d.marks = d.marks[:0]
	d.endTransmission()
	for i := range d.noiseRing {
		d.noiseRing[i] = d.sigDB
	}
	d.noiseN = len(d.noiseRing)
	d.noiseDB = d.sigDB
	d.haveSig = false
	d.seenMark = false
	d.acq, d.acqRuns = true, d.acqRuns[:0]
	d.speed.reset()
	d.state, d.raw, d.run, d.pend = false, false, 0, 0
}

func (d *Decoder) endRun(on bool, frames int) {
	dur := float64(frames) * d.frameDur
	if d.cfg.Record {
		d.addKeying(on, dur)
	}
	if on {
		d.seenMark = true
		d.speed.add(dur, true)
		if d.acq {
			d.acquire(timing{dur: dur, mark: true})
			return
		}
		d.addMark(dur)
		return
	}
	if !d.seenMark { // silence before the transmission
		return
	}
	if dur < 10*d.speed.unit {
		d.speed.add(dur, false)
	}
	if d.acq {
		d.acquire(timing{dur: dur, mark: false})
	}
}

// addMark appends a mark to the character being received.
func (d *Decoder) addMark(dur float64) {
	d.marks = append(d.marks, dur)
	if len(d.marks) >= 16 { // runaway symbol (noise?): flush it as unknown
		d.emitChar()
	}
}

// acquisition parameters
const (
	acqElements = 12  // marks + gaps held back at the start of a transmission
	acqMaxGap   = 1.0 // seconds; a pause this long ends acquisition early
	reacqGap    = 2.0 // seconds of silence after which the next transmission is re-acquired
)

func (d *Decoder) acquire(t timing) {
	d.acqRuns = append(d.acqRuns, t)
	if len(d.acqRuns) >= acqElements {
		d.finishAcq()
	}
}

// finishAcq refits the speed on everything held back (with only a weak pull
// towards the configured WPM) and replays it through the character logic.
func (d *Decoder) finishAcq() {
	d.acq = false
	d.speed.refit()
	for _, t := range d.acqRuns {
		if t.mark {
			d.addMark(t.dur)
		} else {
			d.gapReached(t.dur)
		}
	}
	d.acqRuns = d.acqRuns[:0]
}

// checkGap runs on every key-up frame and emits the pending character, word
// space or line break as soon as the gap is long enough to be sure.
func (d *Decoder) checkGap() {
	g := float64(d.run) * d.frameDur
	if d.auto && g >= sessionSilence && d.lang.session != Auto {
		d.lang.forget()
	}
	if d.acq {
		if !d.seenMark || g < math.Max(acqMaxGap, 10*d.speed.unit) {
			return
		}
		d.finishAcq()
	}
	d.gapReached(g)
	if g >= math.Max(reacqGap, 20*d.speed.unit) {
		// long silence: the next transmission may be another operator at
		// another speed
		d.acq, d.seenMark = true, false
		d.speed.reset()
		d.endTransmission()
	}
}

// gapReached applies the gap rules for a key-up of length g seconds; calling
// it repeatedly with a growing g is safe.
func (d *Decoder) gapReached(g float64) {
	u := d.speed.unit
	if len(d.marks) > 0 && g >= 2*u {
		d.emitChar()
	}
	if d.sinceSpace && g >= 5*u {
		d.pendingSpace, d.sinceSpace = true, false
		if d.auto {
			d.out = append(d.out, d.lang.endWord()...)
		}
	}
	if d.cfg.LineBreak > 0 && d.sinceLine && g >= d.cfg.LineBreak {
		d.endTransmission()
		d.out = append(d.out, '\n')
		if d.cfg.Record {
			d.morse = append(d.morse, '\n')
		}
		d.sinceLine, d.sinceSpace, d.pendingSpace, d.lineStart = false, false, false, true
	}
}

func (d *Decoder) emitChar() {
	u := d.speed.unit
	code := make([]byte, len(d.marks))
	for i, m := range d.marks {
		if m < 2*u {
			code[i] = '.'
		} else {
			code[i] = '-'
		}
	}
	d.marks = d.marks[:0]
	if d.auto {
		d.out = append(d.out, d.lang.char(string(code), d.pendingSpace)...)
	} else {
		d.appendChar(string(code))
	}
	if d.cfg.Record {
		if !d.lineStart {
			if d.pendingSpace {
				d.morse = append(d.morse, WordSep...)
			} else {
				d.morse = append(d.morse, LetterSep...)
			}
		}
		d.morse = append(d.morse, code...)
	}
	d.pendingSpace, d.lineStart = false, false
	d.sinceSpace, d.sinceLine = true, true
}

// appendChar outputs a character in the configured alphabet.
func (d *Decoder) appendChar(code string) {
	r, ok := Char(code, d.cfg.Language)
	if !ok {
		r = UnknownRune
	}
	if d.pendingSpace {
		d.out = append(d.out, ' ')
	}
	d.out = utf8.AppendRune(d.out, r)
}

// endTransmission resolves the word in progress and starts a new phrase for
// the language detector (after a long gap, a line break or a carrier).
func (d *Decoder) endTransmission() {
	if d.auto {
		d.out = append(d.out, d.lang.flush()...)
		d.lang.newTransmission()
	}
}

func (d *Decoder) addKeying(on bool, dur float64) {
	if n := len(d.keying); n > 0 && d.keying[n-1].On == on {
		d.keying[n-1].Dur += dur
		return
	}
	d.keying = append(d.keying, KeyRun{On: on, Dur: dur})
}

func (d *Decoder) take() string {
	if len(d.out) == 0 {
		return ""
	}
	s := string(d.out)
	d.out = d.out[:0]
	return s
}

// ---- speed (unit length) estimation ---------------------------------------

const (
	speedHist = 32   // recent marks and gaps considered
	recency   = 0.93 // weight decay per element, newest first
	truncSq   = 0.16 // robust loss: relative errors beyond 40 % count as outliers
	inlierSq  = 0.09 // elements within 30 % refine the estimate
	priorW    = 0.5  // pull towards the previous estimate (log-ratio squared)
	acqPriorW = 0.05 // the same during acquisition: just a tie-breaker
	minUnit   = 1.2 / 80
	maxUnit   = 1.2 / 3
)

var (
	markMultiples = []float64{1, 3}    // dot, dash
	gapMultiples  = []float64{1, 3, 7} // element, letter, word gap
)

type timing struct {
	dur  float64
	mark bool
}

// speedEstimator fits the unit length u to recent marks (u, 3u) and gaps
// (u, 3u, 7u) with a robust loss. Unlike a plain running average it cannot
// get stuck when the speed jumps by 2× (where old dashes look like new dots):
// the candidate set contains every element divided by every multiple, so the
// hypothesis that explains *all* recent durations wins.
type speedEstimator struct {
	unit float64
	hist [speedHist]timing
	n    int
	pos  int
}

func newSpeedEstimator(unit float64) speedEstimator {
	return speedEstimator{unit: math.Min(maxUnit, math.Max(minUnit, unit))}
}

func (s *speedEstimator) at(age int) timing {
	return s.hist[(s.pos-1-age+2*speedHist)%speedHist]
}

func (s *speedEstimator) add(dur float64, mark bool) {
	s.hist[s.pos] = timing{dur: dur, mark: mark}
	s.pos = (s.pos + 1) % speedHist
	if s.n < speedHist {
		s.n++
	}
	s.fit(priorW)
}

// refit re-estimates from the whole history with only a weak prior.
func (s *speedEstimator) refit() {
	if s.n > 0 {
		s.fit(acqPriorW)
	}
}

// reset forgets the history but keeps the current estimate as the prior.
func (s *speedEstimator) reset() { s.n, s.pos = 0, 0 }

func (s *speedEstimator) fit(prior float64) {
	best, bestCost := s.unit, s.cost(s.unit, prior)
	for i := 0; i < s.n; i++ {
		e := s.at(i)
		for _, k := range multiples(e.mark) {
			c := e.dur / k
			if c < minUnit || c > maxUnit {
				continue
			}
			if cost := s.cost(c, prior); cost < bestCost {
				best, bestCost = c, cost
			}
		}
	}
	// refine: weighted mean of the unit implied by every inlier
	var sw, su float64
	w := 1.0
	for i := 0; i < s.n; i++ {
		e := s.at(i)
		k, r := nearestMultiple(e, best)
		if r*r < inlierSq {
			sw += w
			su += w * e.dur / k
		}
		w *= recency
	}
	if sw > 0 {
		best = su / sw
	}
	s.unit = math.Min(maxUnit, math.Max(minUnit, best))
}

func (s *speedEstimator) cost(c, prior float64) float64 {
	total, w := 0.0, 1.0
	for i := 0; i < s.n; i++ {
		_, r := nearestMultiple(s.at(i), c)
		total += w * math.Min(r*r, truncSq)
		w *= recency
	}
	l := math.Log(c / s.unit)
	return total + prior*l*l
}

func multiples(mark bool) []float64 {
	if mark {
		return markMultiples
	}
	return gapMultiples
}

// nearestMultiple returns the multiple of c that best explains e and the
// relative error.
func nearestMultiple(e timing, c float64) (k, rel float64) {
	rel = math.Inf(1)
	for _, m := range multiples(e.mark) {
		r := (e.dur - m*c) / (m * c)
		if math.Abs(r) < math.Abs(rel) {
			k, rel = m, r
		}
	}
	return k, rel
}
