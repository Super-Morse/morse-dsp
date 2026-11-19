package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func cli(t *testing.T, stdin []byte, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, bytes.NewReader(stdin), &out, &errb)
	return out.String(), errb.String(), code
}

func TestScenarioA_TextToMorse(t *testing.T) {
	for _, args := range [][]string{
		{"encode", "HELLO", "WORLD"},
		{"--mode", "encode", "--out-format", "text", "hello world"},
		{"hello", "world"}, // mode guessed from plain text
	} {
		out, errOut, code := cli(t, nil, args...)
		if code != 0 || out != ".... . .-.. .-.. --- / .-- --- .-. .-.. -..\n" {
			t.Errorf("%v: code %d, out %q, err %q", args, code, out, errOut)
		}
	}
	out, _, _ := cli(t, []byte("SOS\n"), "encode")
	if out != "... --- ...\n" {
		t.Errorf("stdin: %q", out)
	}
	out, errOut, code := cli(t, nil, "encode", "A€")
	if code != 0 || out != ".-\n" || !strings.Contains(errOut, "warning") {
		t.Errorf("unknown char: code %d, out %q, err %q", code, out, errOut)
	}
}

func TestScenarioB_MorseToText(t *testing.T) {
	cases := []struct {
		args  []string
		stdin string
		want  string
	}{
		{[]string{"decode", "--in-format", "text", ".... . .-.. .-.. ---"}, "", "HELLO\n"},
		{[]string{"--mode", "decode", "--in-format", "text"}, "-.-. --.- / -.. .\n", "CQ DE\n"},
		{[]string{"decode", "-.-.", "--.-"}, "", "CQ\n"},       // Morse that looks like flags
		{[]string{"decode", "---", "--lang", "en"}, "", "O\n"}, // flags after text
		{[]string{}, "... --- ...", "SOS\n"},                   // mode guessed from Morse
		{[]string{"decode", "--lang", "ru", "-- .. .-."}, "", "МИР\n"},
		{[]string{"decode", ".--. .-. -.-- .-- .. - / -.. .-. ..- ...- ."}, "", "ПРИВІТ ДРУЖЕ\n"}, // detected
		{[]string{"decode", "--. .-. ..-- ...--.. . / .- ..- ..."}, "", "GRÜẞE AUS\n"},
		{[]string{"decode", "--", "-- ---"}, "", "MO\n"},
	}
	for _, c := range cases {
		out, errOut, code := cli(t, []byte(c.stdin), c.args...)
		if code != 0 || out != c.want {
			t.Errorf("%v: code %d, out %q, err %q", c.args, code, out, errOut)
		}
	}
	if _, _, code := cli(t, nil, "decode", "--in-format", "text", "HELLO"); code != 1 {
		t.Errorf("decoding plain text should fail, code %d", code)
	}
}

func TestScenarioC_D_WAVRoundTrip(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "cq.wav")
	for _, wpm := range []string{"15", "25"} {
		_, errOut, code := cli(t, nil, "encode", "-o", wav, "--wpm", wpm, "--snr", "10", "--seed", "7", "CQ CQ DE MORSE DSP K")
		if code != 0 {
			t.Fatalf("encode: %s", errOut)
		}
		data, _ := os.ReadFile(wav)
		if !bytes.HasPrefix(data, []byte("RIFF")) {
			t.Fatal("-o *.wav did not produce a WAV file")
		}
		out, errOut, code := cli(t, nil, "decode", "-i", wav, "-v")
		if code != 0 || out != "CQ CQ DE MORSE DSP K\n" {
			t.Errorf("%s WPM file: code %d, out %q, err %q", wpm, code, out, errOut)
		}
		out, _, _ = cli(t, data) // WAV on stdin, everything auto-detected
		if out != "CQ CQ DE MORSE DSP K\n" {
			t.Errorf("%s WPM stdin: %q", wpm, out)
		}
	}
}

func TestScenarioC_D_RawPCMPipe(t *testing.T) {
	for _, tc := range []struct{ format, rate string }{{"s16le", "12000"}, {"f32le", "8000"}} {
		pcm, errOut, code := cli(t, nil, "encode", "--out-format", "pcm", "--sample-format", tc.format,
			"--rate", tc.rate, "--freq", "640", "--snr", "10", "--seed", "1", "TEST PIPE 73")
		if code != 0 || len(pcm) == 0 {
			t.Fatalf("encode pcm: %s", errOut)
		}
		out, errOut, code := cli(t, []byte(pcm), "decode", "--in-format", "pcm", "--sample-format", tc.format,
			"--rate", tc.rate, "--freq", "auto")
		if code != 0 || out != "TEST PIPE 73\n" {
			t.Errorf("%s: code %d, out %q, err %q", tc.format, code, out, errOut)
		}
	}
}

func TestBadArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--mode", "transmit"},
		{"--in-format", "mp3"},
		{"decode", "--out-format", "wav"},
		{"encode", "--in-format", "pcm"},
		{"encode", "--nope"},
	} {
		if _, _, code := cli(t, nil, args...); code != 2 {
			t.Errorf("%v: exit code %d, want 2", args, code)
		}
	}
	if _, _, code := cli(t, nil, "-h"); code != 0 {
		t.Errorf("-h: exit code %d", code)
	}
}

func TestLanguages(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"encode", "Дим"}, "-.. -.-- --\n"},               // ambiguous Cyrillic: Ukrainian И
		{[]string{"encode", "--lang", "ru", "Дим"}, "-.. .. --\n"}, // forced
		{[]string{"encode", "Привіт"}, ".--. .-. -.-- .-- .. -\n"}, // І gives the language away
		{[]string{"encode", "Mañana, Łódź"}, "-- .- --.-- .- -. .- --..-- / .-..- ---. -.. --..-.\n"},
		{[]string{"decode", "--lang", "ua", "-.. -.-- --"}, "ДИМ\n"}, // a lone "--" would end the flags
		{[]string{"decode", "-.. -.-- --"}, "DYM\n"},                 // a short burst reads as ITU
	} {
		if out, errOut, code := cli(t, nil, c.args...); code != 0 || out != c.want {
			t.Errorf("%v: code %d, out %q, err %q", c.args, code, out, errOut)
		}
	}
	if _, errOut, _ := cli(t, nil, "encode", "-v", "--lang", "ru", "Дим"); !strings.Contains(errOut, "language Russian (fixed)") {
		t.Errorf("verbose fixed language: %q", errOut)
	}
	if _, errOut, _ := cli(t, nil, "decode", "-v", ".-.- -.-"); !strings.Contains(errOut, "language Ukrainian (detected)") {
		t.Errorf("verbose detected language: %q", errOut)
	}

	dir := t.TempDir()
	for i, text := range []string{"ПЕРЕВІРКА ЗВ’ЯЗКУ 73", "GRÜẞE AUS MÜNCHEN", "ЧУЄШ, ЯК ГАРНО СПІВАЄ ПТАХ?"} {
		wav := filepath.Join(dir, strconv.Itoa(i)+".wav")
		if _, errOut, code := cli(t, nil, "encode", "--snr", "10", "--seed", "3", "-o", wav, text); code != 0 {
			t.Fatal(errOut)
		}
		want := strings.ReplaceAll(text, "’", "'") + "\n"
		// a file is decoded as a whole: the language fits the whole recording
		if out, errOut, _ := cli(t, nil, "decode", "-v", "-i", wav); out != want || !strings.Contains(errOut, "language") {
			t.Errorf("file: %q (%q), want %q", out, errOut, want)
		}
		// stdin is decoded live; these phrases open with a word that settles the alphabet
		if i > 0 {
			data, _ := os.ReadFile(wav)
			if out, _, _ := cli(t, data, "decode"); out != want {
				t.Errorf("live: %q, want %q", out, want)
			}
		}
	}
}

func TestRuntimeErrors(t *testing.T) {
	dir := t.TempDir()
	wav, _, _ := cli(t, nil, "encode", "--out-format", "wav", "E")
	wavFile := filepath.Join(dir, "e.wav")
	if err := os.WriteFile(wavFile, []byte(wav), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		stdin []byte
		args  []string
	}{
		{"bad language", nil, []string{"--lang", "xx", "E"}},
		{"file decoder rejects wpm", nil, []string{"decode", "--wpm", "500", "-i", wavFile}},
		{"missing input file", nil, []string{"-i", filepath.Join(dir, "nope.wav")}},
		{"bad output path", nil, []string{"-o", filepath.Join(dir, "no", "such", "dir.txt"), "E"}},
		{"freq not a number", nil, []string{"encode", "--out-format", "wav", "--freq", "abc", "E"}},
		{"encoder rejects wpm", nil, []string{"encode", "--out-format", "wav", "--wpm", "500", "E"}},
		{"encode sample format", nil, []string{"encode", "--out-format", "pcm", "--sample-format", "u8", "E"}},
		{"decode sample format", []byte{1, 2}, []string{"decode", "--in-format", "pcm", "--sample-format", "u8"}},
		{"decode freq", []byte{1, 2}, []string{"decode", "--in-format", "pcm", "--freq", "abc"}},
		{"decoder rejects rate", []byte{1, 2}, []string{"decode", "--in-format", "pcm", "--rate", "10"}},
		{"not a wav", []byte("hello"), []string{"decode", "--in-format", "wav"}},
		{"encode audio input", []byte(wav), []string{"encode"}},
		{"invalid utf-8", []byte{0xff, 0xfe, 'a'}, []string{"--in-format", "text"}},
	}
	for _, c := range cases {
		if _, errOut, code := cli(t, c.stdin, c.args...); code != 1 || errOut == "" {
			t.Errorf("%s: exit code %d, stderr %q", c.name, code, errOut)
		}
	}
	for _, args := range [][]string{
		{"decode", "--mode", "encode"},
		{"-i", "x.wav", "HELLO"},
	} {
		if _, _, code := cli(t, nil, args...); code != 2 {
			t.Errorf("%v: exit code %d, want 2", args, code)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestOutputErrors(t *testing.T) {
	pcm, _, _ := cli(t, nil, "encode", "--out-format", "pcm", "TEST")
	wav := filepath.Join(t.TempDir(), "test.wav")
	if _, errOut, code := cli(t, nil, "encode", "-o", wav, "TEST"); code != 0 {
		t.Fatal(errOut)
	}
	for _, c := range []struct {
		stdin []byte
		args  []string
	}{
		{nil, []string{"encode", "E"}},
		{nil, []string{"decode", "."}},
		{nil, []string{"encode", "--out-format", "wav", "E"}},
		{nil, []string{"encode", "--out-format", "pcm", "E"}},
		{nil, []string{"encode", "--out-format", "pcm", "--realtime", "--wpm", "60", "E"}},
		{[]byte(pcm), []string{"decode", "--in-format", "pcm"}},
		{nil, []string{"decode", "-i", wav}},
	} {
		var errb bytes.Buffer
		if code := run(c.args, bytes.NewReader(c.stdin), brokenWriter{}, &errb); code != 1 {
			t.Errorf("%v: exit code %d with a broken stdout", c.args, code)
		}
	}
}

func TestRealtimeVerboseAndSniffing(t *testing.T) {
	start := time.Now()
	pcm, errOut, code := cli(t, nil, "encode", "--out-format", "pcm", "--realtime", "--wpm", "60", "-v", "E")
	if code != 0 || len(pcm) == 0 || !strings.Contains(errOut, "samples") {
		t.Fatalf("realtime: code %d, %q", code, errOut)
	}
	if d := time.Since(start); d < 500*time.Millisecond {
		t.Errorf("realtime output took only %v for 0.62 s of audio", d)
	}
	// raw PCM on stdin is recognised without --in-format
	full, _, _ := cli(t, nil, "encode", "--out-format", "pcm", "--snr", "15", "--seed", "1", "SNIFF")
	if out, _, _ := cli(t, []byte(full), "decode"); out != "SNIFF\n" {
		t.Errorf("sniffed pcm: %q", out)
	}
	// streamed WAV (unknown data size) with -v; "-" means stdin/stdout
	wav, _, _ := cli(t, nil, "encode", "--out-format", "wav", "-o", "-", "TEST")
	b := []byte(wav)
	copy(b[40:44], []byte{0xff, 0xff, 0xff, 0xff})
	out, errOut, _ := cli(t, b, "decode", "-i", "-", "-v")
	if out != "TEST\n" || !strings.Contains(errOut, "streamed") {
		t.Errorf("streamed wav: %q / %q", out, errOut)
	}
	// long Cyrillic text: the 512-byte sniff window cuts a rune in half
	long := strings.Repeat("Ж", 300)
	if out, _, code := cli(t, []byte(long), "--lang", "ru"); code != 0 || !strings.HasPrefix(out, "...- ...-") {
		t.Errorf("long text: code %d, %.20q", code, out)
	}
	// text with control characters is treated as audio
	if _, _, code := cli(t, []byte("AB\x01CD"), "decode"); code != 0 {
		t.Errorf("control chars: exit code %d", code)
	}
	// unknown Morse code: a warning, not a failure
	if out, errOut, code := cli(t, nil, "decode", "........"); code != 0 || out != "*\n" || !strings.Contains(errOut, "warning") {
		t.Errorf("unknown code: %d %q %q", code, out, errOut)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestMoreModes(t *testing.T) {
	// --out-format alone implies encode; Morse input is keyed as written;
	// --snr without --seed picks a random seed
	for _, args := range [][]string{
		{"--out-format", "pcm", "E"},
		{"--out-format", "wav", "... --- ..."},
		{"encode", "--out-format", "wav", "--snr", "10", "E"},
	} {
		if out, errOut, code := cli(t, nil, args...); code != 0 || len(out) == 0 {
			t.Errorf("%v: code %d, %q", args, code, errOut)
		}
	}
	if _, _, code := cli(t, nil, "--out-format", "mp3", "E"); code != 2 {
		t.Errorf("--out-format mp3: exit code %d", code)
	}
	var errb bytes.Buffer
	if code := run([]string{"--in-format", "text"}, brokenReader{}, &bytes.Buffer{}, &errb); code != 1 {
		t.Errorf("broken stdin: exit code %d", code)
	}
}
