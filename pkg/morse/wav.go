package morse

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strconv"
)

// WAV format tags.
const (
	wavFormatPCM        = 1
	wavFormatFloat      = 3
	wavFormatExtensible = 0xFFFE
)

// WAVInfo describes the audio stream of a WAV file.
type WAVInfo struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
	Float         bool  // IEEE float samples instead of integer PCM
	DataBytes     int64 // size of the data chunk, -1 if unknown (streamed WAV)
}

// Frames returns the number of sample frames, or -1 if unknown.
func (i WAVInfo) Frames() int64 {
	if i.DataBytes < 0 {
		return -1
	}
	return i.DataBytes / int64(i.Channels*i.BitsPerSample/8)
}

// Duration returns the stream length in seconds, or -1 if unknown.
func (i WAVInfo) Duration() float64 {
	f := i.Frames()
	if f < 0 {
		return -1
	}
	return float64(f) / float64(i.SampleRate)
}

// WriteWAV writes mono samples as a canonical 16-bit PCM WAV file.
// Samples are clamped to [-1, 1].
func WriteWAV(w io.Writer, samples []float32, sampleRate int) error {
	if sampleRate <= 0 {
		return errors.New("morse: invalid sample rate")
	}
	dataLen := uint32(len(samples) * 2)
	var h [44]byte
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+dataLen)
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], wavFormatPCM)
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], dataLen)
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	buf := make([]byte, 0, 8192)
	for i, s := range samples {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(floatToS16(s)))
		if len(buf) == cap(buf) || i == len(samples)-1 {
			if _, err := w.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	return nil
}

// EncodeWAV returns samples as an in-memory 16-bit PCM WAV file.
func EncodeWAV(samples []float32, sampleRate int) []byte {
	var b bytes.Buffer
	b.Grow(44 + 2*len(samples))
	_ = WriteWAV(&b, samples, sampleRate) // bytes.Buffer never fails
	return b.Bytes()
}

// ReadWAV reads a whole WAV file and returns its samples mixed down to mono.
// Integer PCM (8/16/24/32 bit) and IEEE float (32/64 bit) are supported.
func ReadWAV(r io.Reader) ([]float32, int, error) {
	wr, err := NewWAVReader(r)
	if err != nil {
		return nil, 0, err
	}
	var out []float32
	if f := wr.Info().Frames(); f > 0 && f < 1<<28 {
		out = make([]float32, 0, f)
	}
	buf := make([]float32, 4096)
	for {
		n, err := wr.ReadSamples(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out, wr.Info().SampleRate, nil
		}
		if err != nil {
			return out, wr.Info().SampleRate, err
		}
	}
}

// DecodeWAV parses an in-memory WAV file (see ReadWAV).
func DecodeWAV(data []byte) ([]float32, int, error) {
	return ReadWAV(bytes.NewReader(data))
}

// WAVReader streams samples from a WAV file without seeking, so it also works
// on pipes. It implements SampleSource.
type WAVReader struct {
	info WAVInfo
	fr   *frameReader
}

// NewWAVReader parses the RIFF header up to the start of the data chunk.
func NewWAVReader(r io.Reader) (*WAVReader, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, errors.New("morse: not a WAV file (short header)")
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("morse: not a WAV file (missing RIFF/WAVE signature)")
	}
	var info WAVInfo
	haveFmt := false
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return nil, errors.New("morse: WAV file has no data chunk")
		}
		id := string(ch[0:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		var err error
		switch id {
		case "fmt ":
			info, err = readFmtChunk(r, size)
			haveFmt = err == nil
		case "data":
			return newDataReader(r, info, haveFmt, size)
		default: // LIST, fact, cue … are skipped; chunks are padded to even sizes
			if _, cerr := io.CopyN(io.Discard, r, size+size&1); cerr != nil {
				err = errors.New("morse: truncated WAV chunk " + quote(id))
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

// readFmtChunk decodes a "fmt " chunk body of the given size.
func readFmtChunk(r io.Reader, size int64) (WAVInfo, error) {
	var info WAVInfo
	if size < 16 || size > 1<<16 {
		return info, errors.New("morse: malformed WAV fmt chunk")
	}
	b := make([]byte, size+size&1)
	if _, err := io.ReadFull(r, b); err != nil {
		return info, errors.New("morse: truncated WAV fmt chunk")
	}
	format := binary.LittleEndian.Uint16(b[0:])
	info.Channels = int(binary.LittleEndian.Uint16(b[2:]))
	info.SampleRate = int(binary.LittleEndian.Uint32(b[4:]))
	info.BitsPerSample = int(binary.LittleEndian.Uint16(b[14:]))
	if format == wavFormatExtensible && size >= 26 {
		format = binary.LittleEndian.Uint16(b[24:]) // first two bytes of SubFormat GUID
	}
	switch format {
	case wavFormatPCM:
	case wavFormatFloat:
		info.Float = true
	default:
		return info, errors.New("morse: unsupported WAV encoding (format tag " + strconv.Itoa(int(format)) + "); only PCM and IEEE float are supported")
	}
	return info, nil
}

// newDataReader positions a WAVReader at the start of the data chunk.
func newDataReader(r io.Reader, info WAVInfo, haveFmt bool, size int64) (*WAVReader, error) {
	if !haveFmt {
		return nil, errors.New("morse: WAV data chunk precedes fmt chunk")
	}
	if err := info.validate(); err != nil {
		return nil, err
	}
	info.DataBytes = size
	var src io.Reader = io.LimitReader(r, size)
	if size == 0 || size == 0xFFFFFFFF { // written by a streaming encoder: read to EOF
		info.DataBytes = -1
		src = r
	}
	return &WAVReader{info: info, fr: newFrameReader(src, info.Channels, info.BitsPerSample/8, info.Float)}, nil
}

func (i WAVInfo) validate() error {
	if i.Channels < 1 || i.Channels > 32 {
		return errors.New("morse: unsupported WAV channel count")
	}
	if i.SampleRate < 1000 || i.SampleRate > 384000 {
		return errors.New("morse: unsupported WAV sample rate")
	}
	switch {
	case i.Float && (i.BitsPerSample == 32 || i.BitsPerSample == 64):
	case !i.Float && (i.BitsPerSample == 8 || i.BitsPerSample == 16 || i.BitsPerSample == 24 || i.BitsPerSample == 32):
	default:
		return errors.New("morse: unsupported WAV bit depth " + strconv.Itoa(i.BitsPerSample))
	}
	return nil
}

// Info returns the stream format.
func (w *WAVReader) Info() WAVInfo { return w.info }

// ReadSamples reads up to len(dst) mono samples. It returns io.EOF at the end
// of the data chunk.
func (w *WAVReader) ReadSamples(dst []float32) (int, error) { return w.fr.read(dst) }

func floatToS16(s float32) int16 {
	v := math.Round(float64(s) * 32767)
	if v > 32767 {
		v = 32767
	} else if v < -32768 {
		v = -32768
	}
	return int16(v)
}
