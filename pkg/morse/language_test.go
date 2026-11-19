package morse

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"math"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---- registry ----------------------------------------------------------------

func TestProfileRegistry(t *testing.T) {
	if len(profiles) != int(numLanguages)-1 {
		t.Fatalf("%d profiles for %d languages", len(profiles), numLanguages-1)
	}
	tags := map[string]bool{}
	for i, p := range Profiles() {
		if p.Lang != Language(i+1) {
			t.Errorf("profile %d is %v", i, p.Lang)
		}
		if tags[p.Tag] {
			t.Errorf("duplicate tag %q", p.Tag)
		}
		tags[p.Tag] = true
		for _, name := range append([]string{p.Tag, p.Name}, p.Aliases...) {
			if got, err := ParseLanguage(name); err != nil || got != p.Lang {
				t.Errorf("ParseLanguage(%q) = %v, %v; want %v", name, got, err, p.Lang)
			}
		}
		checkLetters(t, p)
		for v, to := range p.Variants {
			c, ok := Code(v, p.Lang)
			if want, _ := Code(to, p.Lang); !ok || c != want || c == "" {
				t.Errorf("%v: variant %q is sent as %q, want the code of %q (%q)", p.Lang, v, c, to, want)
			}
		}
	}
}

// checkLetters verifies that a profile's own letters have well-formed codes
// that do not clash with the letters, digits and punctuation every profile
// of the script shares.
func checkLetters(t *testing.T, p LanguageProfile) {
	t.Helper()
	shared := map[string]rune{}
	for _, m := range []map[rune]string{scriptLetters(p.Script), digits, punctuation} {
		for r, c := range m {
			shared[c] = r
		}
	}
	for _, l := range p.Letters {
		if strings.Trim(l.Code, ".-") != "" || l.Code == "" {
			t.Errorf("%v: %q has the malformed code %q", p.Lang, l.Char, l.Code)
		}
		if r, clash := shared[l.Code]; clash {
			t.Errorf("%v: %q takes the code of %q", p.Lang, l.Char, r)
		}
		if r, _ := Char(l.Code, p.Lang); r != l.Char && !sharesEarlierCode(p, l) {
			t.Errorf("%v: %q decodes as %q", p.Lang, l.Code, r)
		}
	}
}

func sharesEarlierCode(p LanguageProfile, l Letter) bool {
	for _, e := range p.Letters {
		if e == l {
			return false
		}
		if e.Code == l.Code {
			return true
		}
	}
	return false
}

// Every character is sent with the same code whatever the language, except
// Cyrillic И, which is the one letter Russian and Ukrainian Morse disagree on.
func TestCodesAgreeAcrossLanguages(t *testing.T) {
	for r, e := range encodeTable {
		for lang := English; lang < numLanguages; lang++ {
			c, ok := Code(r, lang)
			if !ok {
				t.Errorf("Code(%q, %v) failed", r, lang)
				continue
			}
			want := e.code
			if r == 'И' && lang == Russian {
				want = ".."
			}
			if c != want && !(lang == Turkish && r == 'i') {
				t.Errorf("Code(%q, %v) = %q, want %q", r, lang, c, want)
			}
		}
	}
	if c, _ := Code('И', Auto); c != "-.--" {
		t.Errorf("И defaults to the Ukrainian code, got %q", c)
	}
}

func TestProfilesReturnsACopy(t *testing.T) {
	ps := Profiles()
	ps[0].Name = "changed"
	ps[1].Letters[0].Code = "......."
	ps[1].Variants['Ґ'] = 'X'
	ps[1].Aliases[0] = "zz"
	if p := profiles[1]; profiles[0].Name != "English" || p.Letters[0].Code != "-.--" || p.Variants['Ґ'] != 'Г' || p.Aliases[0] != "ua" {
		t.Error("Profiles() exposes the registry")
	}
}

func TestMalformedProfileData(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	mustPanic("bad frequency", func() { parseFreq("E12.7 Tx") })
	mustPanic("too many tiers", func() { buildTables(&LanguageProfile{Name: "X", NGrams: "A | B | C | D"}) })
}

// ---- detection: plain text ---------------------------------------------------

func TestDetectLanguage(t *testing.T) {
	tests := map[string]Language{
		"":                       English,
		"Hello":                  English,
		"73 !":                   English,
		"Guten Tag":              English, // Latin without letters of its own reads as ITU
		"Это эфир":               Russian, // Э
		"Что ты делаешь?":        Russian, // Ы
		"Привіт, ефіре":          Ukrainian,
		"Ґанок":                  Ukrainian,
		"Дим":                    Ukrainian, // ambiguous Cyrillic: Ukrainian
		"їжак і ёж ы":            Ukrainian, // two letters each: a tie goes to Ukrainian
		"Що ти робиш":            Ukrainian, // no telltale letters, but Ukrainian words
		"Grüße aus München":      German,
		"Ça va très bien":        French,
		"Mañana":                 Spanish,
		"Dziękuję za łączność":   Polish,
		"Vädret är fint idag":    Swedish,
		"Smør og æble":           Swedish, // Nordic letters
		"Çok güzel, teşekkürler": Turkish,
		"ılık":                   Turkish, // dotless ı
	}
	for in, want := range tests {
		if got := DetectLanguage(in); got != want {
			t.Errorf("DetectLanguage(%q) = %v, want %v", in, got, want)
		}
	}
}

// ---- detection: Morse --------------------------------------------------------

// detectionCorpus holds ordinary phrases in every language. Received as
// Morse, each must decode exactly as in its own alphabet.
var detectionCorpus = map[Language][]string{
	English: {"THE WEATHER IS FINE TODAY AND THE ANTENNA WORKS WELL", "HELLO WORLD", "THANK YOU FOR THE NICE CONTACT",
		"I AM RUNNING ONE HUNDRED WATTS INTO A DIPOLE", "PLEASE SEND YOUR NAME AND LOCATION", "CQ CQ DE UR5ABC K",
		"UR RST 599 5NN QTH KYIV", "TNX FER QSO 73", "GOOD MORNING", "WHERE ARE YOU", "PSE QSL", "SOS", "MORSE DSP"},
	Ukrainian: {"ПРИВІТ ДРУЖЕ ЯК СПРАВИ", "ДЯКУЮ ЗА ЗВ'ЯЗОК", "СЬОГОДНІ ГАРНА ПОГОДА І СИГНАЛ ДУЖЕ ДОБРИЙ",
		"МОЯ АНТЕНА ЦЕ ДИПОЛЬ НА ДАХУ", "Я ЖИВУ У КИЄВІ", "ЩО ТИ РОБИШ", "ДОБРИЙ ДЕНЬ", "ВІН СКАЗАВ ЩО ПРИЙДЕ ЗАВТРА",
		"ПРИВІТ, ЕФІРЕ! 73", "ЧУЄШ ЇХ, ДОЦЮ, ГА? КУМЕДНА Ж ТИ, ПРОЩАЙСЯ БЕЗ ГОЛЬФІВ!", "МИ ЧЕКАЄМО НА ВАШУ ВІДПОВІДЬ",
		"ПЕРЕВІРКА ЗВ'ЯЗКУ 73", "ЇЖАК"},
	Russian: {"СПАСИБО ЗА СВЯЗЬ", "СЕГОДНЯ ХОРОШАЯ ПОГОДА И СИГНАЛ ОЧЕНЬ ГРОМКИЙ", "МОЯ АНТЕННА ЭТО ДИПОЛЬ НА КРЫШЕ",
		"ЧТО ТЫ ДЕЛАЕШЬ", "ОН СКАЗАЛ ЧТО ПРИДЕТ ЗАВТРА", "СЪЕШЬ ЖЕ ЕЩЕ ЭТИХ МЯГКИХ ФРАНЦУЗСКИХ БУЛОК", "МЫ ЖДЕМ ВАШ ОТВЕТ",
		"ПРОВЕРКА СВЯЗИ 73"},
	German: {"DAS WETTER IST HEUTE SCHÖN UND DIE ANTENNE FUNKTIONIERT GUT", "VIELEN DANK FÜR DAS GESPRÄCH",
		"ICH WOHNE IN MÜNCHEN", "DIE STRAẞE IST GROẞ", "WIR HÖREN EUCH SEHR GUT", "ÜBER DEN WOLKEN"},
	French: {"BONJOUR À TOUS LES AMIS", "LE TEMPS EST TRÈS BEAU AUJOURD'HUI", "JE SUIS À PARIS AVEC MA FAMILLE",
		"C'EST UNE BELLE JOURNÉE D'ÉTÉ", "LA RÉCEPTION EST EXCELLENTE ICI", "ÇA VA"},
	Spanish: {"EL SEÑOR VIVE EN ESPAÑA", "LA CANCIÓN ES MUY BONITA", "MAÑANA VAMOS A LA PLAYA", "DÓNDE ESTÁ LA ESTACIÓN?"},
	Polish: {"DZIEŃ DOBRY WSZYSTKIM", "DZIĘKUJĘ ZA ŁĄCZNOŚĆ", "ZAŻÓŁĆ GĘŚLĄ JAŹŃ", "POGODA JEST DZIŚ BARDZO ŁADNA",
		"SŁYSZĘ CIĘ BARDZO DOBRZE", "CZEŚĆ JAK SIĘ MASZ"},
	Swedish: {"HEJ ALLA VÄNNER", "TACK FÖR KONTAKTEN", "VÄDRET ÄR FINT IDAG", "VI HÖR DIG MYCKET BRA", "GÅ TILL SKOLAN"},
	Turkish: {"MERHABA ARKADAŞLAR", "TEŞEKKÜR EDERİM", "BUGÜN HAVA ÇOK GÜZEL", "İSTANBUL'DA YAŞIYORUM", "SİNYAL ÇOK GÜÇLÜ",
		"GÖRÜŞÜRÜZ"},
}

func TestDetectMorseLanguage(t *testing.T) {
	for lang, phrases := range detectionCorpus {
		for _, s := range phrases {
			code, _ := EncodeText(s, lang)
			want, _ := DecodeText(code, lang)
			if got, _ := DecodeText(code, Auto); got != want {
				t.Errorf("%v %q decoded as %q", lang, s, got)
			}
			// Latin phrases without letters of their own are plain ITU
			if got := DetectMorseLanguage(code); got != lang && (profileOf(lang).Script != Latin || got != English) {
				t.Errorf("%v %q detected as %v", lang, s, got)
			}
		}
	}
}

// Very short Cyrillic with nothing that tells the alphabets apart falls back
// to the international alphabet; ambiguous Cyrillic reads as Ukrainian.
func TestDetectMorseLanguageFallbacks(t *testing.T) {
	tests := []struct{ text, want string }{
		{"ДИМ", "DIM"},
		{"ДА", "DA"},
		{"ДОБРЫЙ ДЕНЬ", "ДОБРИЙ ДЕНЬ"}, // the same Morse in both languages
	}
	for _, tt := range tests {
		code, _ := EncodeText(tt.text, Russian)
		if got, _ := DecodeText(code, Auto); got != tt.want {
			t.Errorf("%q decoded as %q, want %q", tt.text, got, tt.want)
		}
	}
	if got := DetectMorseLanguage("not morse"); got != English {
		t.Errorf("unparsable input detected as %v", got)
	}
}

// Ham abbreviations, Q-codes, reports and callsigns read as ITU English even
// in a short burst, and in a stream they override a non-Latin session.
func TestHamTokensFavorEnglish(t *testing.T) {
	bursts := []string{"CQ", "DE", "UR", "RST", "599", "QTH", "PSE", "K", "73", "QRZ?", "TNX FB OM", "DE UR5ABC",
		"RST 5NN", "UR5ABC", "DL1XYZ DE UR5ABC K"}
	for _, b := range bursts {
		code, _ := EncodeText(b, Auto)
		if got := DetectMorseLanguage(code); got != English {
			t.Errorf("%q detected as %v", b, got)
		}
		if got, _ := DecodeText(code, Auto); got != b {
			t.Errorf("%q decoded as %q", b, got)
		}
	}
	tr := sessionIn(t, "ПРИВІТ ДРУЖЕ ЯК СПРАВИ", Ukrainian)
	feedWords(&tr, "QTH", English)
	if tr.active != English {
		t.Errorf("QTH in a Ukrainian session: active %v", tr.active)
	}
}

// One noise-garbled code ("----" exists only in Cyrillic) must not switch a
// long English transmission to Cyrillic.
func TestGarbledCodeDoesNotFlipScript(t *testing.T) {
	code := "- .... . / ---- .-- . .- - .... . .-. / .. ... / ..-. .. -. . / - --- -.. .- -.--"
	if got, _ := DecodeText(code, Auto); got != "THE *WEATHER IS FINE TODAY" {
		t.Errorf("got %q", got)
	}
}

// ---- streaming tracker -------------------------------------------------------

// feedWords sends text through a tracker as a stream of codes and returns
// the output of every char and endWord call.
func feedWords(tr *langTracker, text string, lang Language) []string {
	code, _ := EncodeText(text, lang)
	words, _ := splitMorse(code)
	var out []string
	for wi, w := range words {
		for ci, c := range w {
			out = append(out, tr.char(c, wi > 0 && ci == 0))
		}
		out = append(out, tr.endWord())
	}
	return out
}

func sessionIn(t *testing.T, text string, lang Language) langTracker {
	t.Helper()
	tr := newLangTracker()
	feedWords(&tr, text, lang)
	if tr.session != lang {
		t.Fatalf("%q did not confirm %v (session %v)", text, lang, tr.session)
	}
	tr.newTransmission()
	return tr
}

func TestTrackerEmitsCharactersImmediately(t *testing.T) {
	tr := newLangTracker()
	got := feedWords(&tr, "CQ DE UR5ABC", English)
	want := []string{"C", "Q", "", " D", "E", "", " U", "R", "5", "A", "B", "C", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestTrackerResolvesExtendedCodesAtWordEnd(t *testing.T) {
	tr := newLangTracker()
	got := feedWords(&tr, "GRÜẞE AUS MÜNCHEN", German)
	// Ü is held (Ü, Ю and Ü's other readings disagree) until "GRÜẞE" ends;
	// once German is confirmed, the second Ü goes out immediately
	want := []string{"G", "R", "", "", "", "ÜẞE", " A", "U", "S", "", " M", "Ü", "N", "C", "H", "E", "N", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if tr.session != German || tr.active != German {
		t.Errorf("session %v, active %v", tr.session, tr.active)
	}
}

func TestTrackerSessionLanguage(t *testing.T) {
	tr := sessionIn(t, "ПРИВІТ ДРУЖЕ ЯК СПРАВИ", Ukrainian)
	if tr.active != Ukrainian {
		t.Errorf("a new transmission starts in %v", tr.active)
	}
	if got := strings.Join(feedWords(&tr, "ТАК", Ukrainian), ""); got != "ТАК" {
		t.Errorf("short reply in the session: %q", got)
	}
	tr.newTransmission()
	tr.forget()
	if got := strings.Join(feedWords(&tr, "ТАК", Ukrainian), ""); got != "TAK" {
		t.Errorf("short reply after forget: %q", got)
	}
	// a Latin session without its own letters reads as English
	tr = sessionIn(t, "GRÜẞE AUS MÜNCHEN", German)
	if got := strings.Join(feedWords(&tr, "JA", German), ""); got != "JA" || tr.session != German {
		t.Errorf("JA: %q, session %v", got, tr.session)
	}
}

// Evidence decays word by word, so a long transmission can change language.
func TestTrackerFollowsLanguageChange(t *testing.T) {
	tr := newLangTracker()
	feedWords(&tr, "THE WEATHER IS FINE TODAY AND THE ANTENNA WORKS WELL", English)
	feedWords(&tr, "ПРИВІТ ДРУЖЕ ЯК СПРАВИ СЬОГОДНІ ГАРНА ПОГОДА", Ukrainian)
	if tr.active != Ukrainian {
		t.Errorf("active %v", tr.active)
	}
}

func TestTrackerResolvesRunawayWords(t *testing.T) {
	tr := newLangTracker()
	var out string
	for i := 0; i < maxWordCodes; i++ {
		out += tr.char(".-.-", false) // Я or Ä, never ended by a gap
	}
	if len(tr.cur.codes) != 0 || len([]rune(out)) != maxWordCodes {
		t.Errorf("runaway word not resolved: %q, %d pending", out, len(tr.cur.codes))
	}
	if tr.flush() != "" {
		t.Error("flush of an empty word returned text")
	}
}

// ---- streaming decoder -------------------------------------------------------

// streamDecode decodes samples with Process in 20 ms blocks.
func streamDecode(d *Decoder, s []float32) string {
	var b strings.Builder
	for i := 0; i < len(s); i += 160 {
		b.WriteString(d.Process(s[i:min(i+160, len(s))]))
	}
	return b.String()
}

func TestDecoderKeepsSessionLanguage(t *testing.T) {
	first := synth(t, "ПРИВІТ ДРУЖЕ ЯК СПРАВИ", synthOpts{noise: true, snr: 15, seed: 1})
	reply := synth(t, "ТАК", synthOpts{noise: true, snr: 15, seed: 2})
	sigma := DefaultAmplitude / math.Sqrt2 * math.Pow(10, -0.75)
	for _, c := range []struct {
		gap  float64
		want string
	}{{4, " ТАК"}, {35, " TAK"}} { // 35 s of silence forgets the session
		d, _ := NewDecoder(DefaultDecoderConfig())
		streamDecode(d, first)
		streamDecode(d, whiteNoise(int(c.gap*8000), sigma, 3))
		out := streamDecode(d, reply) + d.Flush()
		if out != c.want {
			t.Errorf("after %v s: reply %q, want %q", c.gap, out, c.want)
		}
	}

	d, _ := NewDecoder(DefaultDecoderConfig())
	streamDecode(d, first)
	if d.Language() != Ukrainian {
		t.Errorf("Language() = %v", d.Language())
	}
	d.ResetLanguage()
	if out := streamDecode(d, reply) + d.Flush(); out != " TAK" {
		t.Errorf("after ResetLanguage: %q", out)
	}
	fixed, _ := NewDecoder(DecoderConfig{Language: Russian})
	fixed.ResetLanguage() // no-op
	if fixed.Language() != Russian {
		t.Errorf("fixed decoder reports %v", fixed.Language())
	}
}

// DecodeSource reads a recording to the end and, like DecodeSamples, chooses
// the alphabet from all of it: even the first word comes out right.
func TestDecodeSource(t *testing.T) {
	text := "ПЕРЕВІРКА ЗВ'ЯЗКУ 73"
	s := synth(t, text, synthOpts{noise: true, snr: 10, seed: 4})
	r, err := DecodeSource(NewPCMReader(bytes.NewReader(toS16LE(s)), S16LE), DefaultDecoderConfig())
	if err != nil || r.Text != text || r.Language != Ukrainian {
		t.Errorf("got %q as %v, %v", r.Text, r.Language, err)
	}
	if _, err := DecodeSource(failSource{}, DefaultDecoderConfig()); !errors.Is(err, errBoom) {
		t.Errorf("source error: %v", err)
	}
	if _, err := DecodeSource(failSource{}, DecoderConfig{WPM: 500}); err == nil {
		t.Error("invalid config accepted")
	}
}

// With a fixed alphabet, a code it does not have becomes UnknownRune.
func TestFixedAlphabetUnknownCode(t *testing.T) {
	enc, _ := NewEncoder(DefaultEncoderConfig())
	s, _ := enc.SynthesizeMorse("-- --- ---- / -.-")
	for lang, want := range map[Language]string{English: "MO* K", Russian: "МОШ К"} {
		cfg := DefaultDecoderConfig()
		cfg.Language = lang
		if r, _ := DecodeSamples(s, cfg); r.Text != want || r.Language != lang {
			t.Errorf("%v: %q as %v, want %q", lang, r.Text, r.Language, want)
		}
	}
}

// multilingualCases are decoded from noisy audio, streamed and in one piece.
// Each phrase opens with a word that settles its alphabet on its own, so a
// fresh stream (no session language yet) gets even the first word right.
var multilingualCases = []struct {
	lang Language
	text string
}{
	{English, "CQ CQ DE UR5ABC UR5ABC PSE K"},
	{Ukrainian, "ЧУЄШ, ЯК ГАРНО СПІВАЄ ПТАХ У САДУ? 73"},
	{Russian, "ЧТО ТЫ СЛЫШИШЬ? ЭТО ПРОВЕРКА СВЯЗИ"},
	{German, "GRÜẞE AUS MÜNCHEN, WIR HÖREN EUCH GUT"},
	{French, "À BIENTÔT, LA RÉCEPTION EST TRÈS BONNE"},
	{Spanish, "AÑO NUEVO, EL SEÑOR LLEGARÁ A ESPAÑA"},
	{Polish, "ŁĄCZNOŚĆ JEST DOBRA, DZIĘKUJĘ I POZDRAWIAM"},
	{Swedish, "ÅTTA VÄNNER HÖR DIG BRA PÅ BANDET"},
	{Turkish, "ŞİMDİ SİNYAL ÇOK GÜÇLÜ, TEŞEKKÜRLER"},
}

func TestMultilingualNoisyAudio(t *testing.T) {
	for i, c := range multilingualCases {
		want := NormalizeText(c.text)
		for _, wpm := range []float64{15, 25} {
			s := synth(t, c.text, synthOpts{wpm: wpm, noise: true, snr: 10, seed: uint64(10 + i), lang: c.lang})
			r, err := DecodeSamples(s, DefaultDecoderConfig())
			if err != nil || r.Text != want || r.Language != c.lang {
				t.Errorf("%v, %v WPM: %q as %v, want %q", c.lang, wpm, r.Text, r.Language, want)
			}
			d, _ := NewDecoder(DefaultDecoderConfig())
			var out strings.Builder
			src := &chunkReader{data: toS16LE(s), rng: rand.New(rand.NewPCG(uint64(i), 7))}
			if err := d.DecodeReader(src, S16LE, &out); err != nil || out.String() != want {
				t.Errorf("%v, %v WPM, streamed: %q, want %q", c.lang, wpm, out.String(), want)
			}
		}
	}
}

// ---- SDR-ready core ----------------------------------------------------------

// The core must stay free of the operating system, js and CLI packages, so it
// builds for js/wasm and can be wrapped as a c-shared library unchanged.
func TestCoreImportsOnlyPortablePackages(t *testing.T) {
	allowed := map[string]bool{"bytes": true, "encoding/binary": true, "errors": true, "io": true, "math": true,
		"math/rand/v2": true, "sort": true, "strconv": true, "strings": true, "unicode": true, "unicode/utf8": true}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		ast, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range ast.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); !allowed[path] {
				t.Errorf("%s imports %q", f, path)
			}
		}
		checked++
	}
	if checked < 8 {
		t.Errorf("only %d source files checked", checked)
	}
}
