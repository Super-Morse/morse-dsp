package morse

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
)

// SampleSource is a stream of mono audio samples in [-1, 1].
// ReadSamples returns io.EOF when the stream ends.
type SampleSource interface {
	ReadSamples(dst []float32) (n int, err error)
}

// SampleFormat is the encoding of a headerless (raw) PCM stream.
type SampleFormat int

const (
	// S16LE is signed 16-bit little-endian PCM, the format of
	// `sox -t raw -e signed -b 16`, `aplay -f S16_LE` and numpy int16.tobytes().
	S16LE SampleFormat = iota
	// F32LE is 32-bit IEEE float little-endian, numpy float32.tobytes().
	F32LE
)

// ParseSampleFormat accepts "s16le"/"s16"/"int16" and "f32le"/"f32"/"float32".
func ParseSampleFormat(s string) (SampleFormat, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "s16le", "s16", "int16", "pcm16":
		return S16LE, nil
	case "f32le", "f32", "float32", "float":
		return F32LE, nil
	}
	return S16LE, errors.New("morse: unknown sample format " + quote(s) + " (want s16le or f32le)")
}

func (f SampleFormat) String() string {
	if f == F32LE {
		return "f32le"
	}
	return "s16le"
}

// BytesPerSample returns the size of one sample.
func (f SampleFormat) BytesPerSample() int {
	if f == F32LE {
		return 4
	}
	return 2
}

// PCMReader decodes a raw mono PCM byte stream. It returns samples as soon as
// the underlying reader delivers them, which keeps latency low on pipes.
type PCMReader struct{ fr *frameReader }

// NewPCMReader wraps r, a headerless mono stream in the given format.
func NewPCMReader(r io.Reader, f SampleFormat) *PCMReader {
	return &PCMReader{fr: newFrameReader(r, 1, f.BytesPerSample(), f == F32LE)}
}

// ReadSamples implements SampleSource.
func (p *PCMReader) ReadSamples(dst []float32) (int, error) { return p.fr.read(dst) }

// WritePCM writes samples as raw mono PCM.
func WritePCM(w io.Writer, samples []float32, f SampleFormat) error {
	buf := make([]byte, 0, 8192)
	for i, s := range samples {
		if f == F32LE {
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(s))
		} else {
			buf = binary.LittleEndian.AppendUint16(buf, uint16(floatToS16(s)))
		}
		if len(buf)+4 > cap(buf) || i == len(samples)-1 {
			if _, err := w.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	return nil
}

// SamplesFromChan adapts a channel of samples to a SampleSource. The source
// ends when the channel is closed.
func SamplesFromChan(ch <-chan float32) SampleSource { return chanSource(ch) }

type chanSource <-chan float32

func (c chanSource) ReadSamples(dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	s, ok := <-c
	if !ok {
		return 0, io.EOF
	}
	dst[0] = s
	n := 1
	for n < len(dst) { // drain whatever is already buffered, without blocking
		select {
		case s, ok := <-c:
			if !ok {
				return n, nil
			}
			dst[n] = s
			n++
		default:
			return n, nil
		}
	}
	return n, nil
}

// frameReader converts interleaved PCM frames into mono float samples,
// carrying partial frames across short reads.
type frameReader struct {
	r        io.Reader
	channels int
	width    int // bytes per sample
	float    bool
	buf      []byte
	have     int // valid bytes at the start of buf
	err      error
}

func newFrameReader(r io.Reader, channels, width int, float bool) *frameReader {
	return &frameReader{r: r, channels: channels, width: width, float: float}
}

func (f *frameReader) read(dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	frame := f.channels * f.width
	if need := len(dst) * frame; cap(f.buf) < need {
		nb := make([]byte, need)
		copy(nb, f.buf[:f.have])
		f.buf = nb
	}
	f.buf = f.buf[:len(dst)*frame]
	for f.have < frame && f.err == nil {
		var n int
		n, f.err = f.r.Read(f.buf[f.have:])
		f.have += n
	}
	frames := f.have / frame
	if frames == 0 {
		if f.err == nil || f.err == io.EOF {
			return 0, io.EOF // a trailing partial frame is dropped
		}
		return 0, f.err
	}
	inv := 1 / float32(f.channels)
	for i := 0; i < frames; i++ {
		var acc float32
		base := i * frame
		for c := 0; c < f.channels; c++ {
			acc += f.sample(f.buf[base+c*f.width:])
		}
		dst[i] = acc * inv
	}
	used := frames * frame
	f.have = copy(f.buf, f.buf[used:f.have])
	return frames, nil
}

func (f *frameReader) sample(b []byte) float32 {
	switch {
	case f.float && f.width == 4:
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	case f.float && f.width == 8:
		return float32(math.Float64frombits(binary.LittleEndian.Uint64(b)))
	case f.width == 1:
		return (float32(b[0]) - 128) / 128
	case f.width == 2:
		return float32(int16(binary.LittleEndian.Uint16(b))) / 32768
	case f.width == 3:
		v := int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24) >> 8
		return float32(v) / 8388608
	default: // 4
		return float32(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
}
