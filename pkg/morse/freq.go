package morse

import (
	"math"
	"sort"
)

// Defaults for the automatic tone search.
const (
	DefaultMinFrequency = 300.0
	DefaultMaxFrequency = 1500.0
	// minimum ratio of the spectral peak to the median of the search band
	// for a tone to be accepted, dB
	freqPeakDB = 12.0
)

// EstimateFrequency finds the dominant CW tone in samples, searching
// [minHz, maxHz]. It averages Hann-windowed power spectra (Welch's method)
// and refines the peak by parabolic interpolation. ok is false when no
// sufficiently prominent narrow-band peak exists (e.g. only noise).
func EstimateFrequency(samples []float32, sampleRate int, minHz, maxHz float64) (freq float64, ok bool) {
	fe := newFreqEstimator(float64(sampleRate), minHz, maxHz)
	fe.feed(samples)
	return fe.estimate()
}

// freqEstimator accumulates a Welch power spectrum incrementally.
type freqEstimator struct {
	fs     float64
	nfft   int
	hop    int
	win    []float64
	buf    []float64 // sliding window of the last nfft samples
	fill   int
	psd    []float64
	frames int
	re, im []float64
	lo, hi int
}

func newFreqEstimator(fs, minHz, maxHz float64) *freqEstimator {
	// ~4 Hz resolution: enough to place the Goertzel filter, cheap to compute.
	nfft := 256
	for float64(nfft) < fs/4 {
		nfft *= 2
	}
	f := &freqEstimator{
		fs:   fs,
		nfft: nfft,
		hop:  nfft / 2,
		win:  hann(nfft),
		buf:  make([]float64, nfft),
		psd:  make([]float64, nfft/2+1),
		re:   make([]float64, nfft),
		im:   make([]float64, nfft),
	}
	if maxHz <= 0 || maxHz > fs/2*0.95 {
		maxHz = fs / 2 * 0.95
	}
	f.lo = max(2, int(math.Ceil(minHz*float64(nfft)/fs)))
	f.hi = min(nfft/2-2, int(math.Floor(maxHz*float64(nfft)/fs)))
	return f
}

func (f *freqEstimator) feed(x []float32) {
	for _, s := range x {
		f.buf[f.fill] = float64(s)
		f.fill++
		if f.fill == f.nfft {
			f.analyze()
			copy(f.buf, f.buf[f.hop:]) // 50 % overlap
			f.fill = f.nfft - f.hop
		}
	}
}

func (f *freqEstimator) analyze() {
	for i := range f.re {
		f.re[i] = f.buf[i] * f.win[i]
		f.im[i] = 0
	}
	fft(f.re, f.im)
	for k := range f.psd {
		f.psd[k] += f.re[k]*f.re[k] + f.im[k]*f.im[k]
	}
	f.frames++
}

func (f *freqEstimator) estimate() (float64, bool) {
	if f.frames < 3 || f.hi <= f.lo {
		return 0, false
	}
	band := f.psd[f.lo : f.hi+1]
	peak := f.lo
	for k := f.lo; k <= f.hi; k++ {
		if f.psd[k] > f.psd[peak] {
			peak = k
		}
	}
	sorted := append([]float64(nil), band...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if median <= 0 || 10*math.Log10(f.psd[peak]/median) < freqPeakDB {
		return 0, false
	}
	a := math.Log(f.psd[peak-1] + 1e-30)
	b := math.Log(f.psd[peak] + 1e-30)
	c := math.Log(f.psd[peak+1] + 1e-30)
	delta := 0.0
	if den := a - 2*b + c; den < 0 {
		delta = 0.5 * (a - c) / den
	}
	return (float64(peak) + delta) * f.fs / float64(f.nfft), true
}

// fft is an in-place iterative radix-2 complex FFT; len(re) must be a power
// of two.
func fft(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := -2 * math.Pi / float64(size)
		wr, wi := math.Cos(ang), math.Sin(ang)
		half := size / 2
		for start := 0; start < n; start += size {
			cr, ci := 1.0, 0.0
			for k := 0; k < half; k++ {
				a, b := start+k, start+k+half
				tr := re[b]*cr - im[b]*ci
				ti := re[b]*ci + im[b]*cr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// hann returns a symmetric-periodic Hann window of length n.
func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/float64(n))
	}
	return w
}
