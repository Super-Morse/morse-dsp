package morse

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Decision thresholds of the language detector.
const (
	defaultMargin    = 3.0 // another alphabet must beat the default one by this much
	confidenceMargin = 4.0 // a lead that confirms an alphabet as the session language
	maxWordCodes     = 32  // a "word" this long is resolved without waiting for a gap
)

// evidence accumulates the score of every language over the words of a
// phrase. All scores are additive, so a stream can add word after word.
//
// Per word, each alphabet earns:
//   - a heavy penalty for every code (or letter) it does not have, so an
//     alphabet that cannot spell the phrase drops out (one noise-garbled code
//     cannot flip a long transmission, though);
//   - a bonus for codes and letters that only some alphabets have, split
//     between them (".---." is Ukrainian Ї, "...-..." Polish Ś, ß German);
//   - n-gram, frequent-word and signal-word scores of the text it decodes to;
//   - for English, a bonus for callsign-like words (UR5ABC, 5NN).
//
// A Latin alphabet other than English that has not seen any of its own
// letters reads exactly like English, so its score counts for English: the
// language of a Latin text is ITU English until a letter with a diacritic
// says otherwise (see effective).
type evidence struct {
	score  [numLanguages]float64
	marked [numLanguages]bool // the alphabet has seen a letter or code of its own
}

// addCodes scores one received word, given as Morse codes.
func (e *evidence) addCodes(codes []string) {
	for i := range profiles {
		p := &profiles[i]
		s, marked := p.scoreCodes(codes)
		e.add(p.Lang, s, marked)
	}
	if callsignLike(codes) {
		e.score[English] += callsignBonus
	}
}

// addText scores one word of plain text.
func (e *evidence) addText(word []rune) {
	for i := range profiles {
		p := &profiles[i]
		s, marked := p.scoreText(word)
		e.add(p.Lang, s, marked)
	}
}

func (e *evidence) add(l Language, s float64, marked bool) {
	e.score[l] += s
	e.marked[l] = e.marked[l] || marked
}

// scale weakens older evidence (streams use it so that a long transmission
// can still change language).
func (e *evidence) scale(f float64) {
	for i := range e.score {
		e.score[i] *= f
	}
}

// effective returns the scores the decision is made on: a Latin alphabet
// without letters of its own in the phrase is not a candidate, and its score
// counts for English, which reads the same.
func (e *evidence) effective() [numLanguages]float64 {
	eff := e.score
	for i := range profiles {
		p := &profiles[i]
		if p.Script == Latin && p.Lang != English && !e.marked[p.Lang] {
			eff[English] = math.Max(eff[English], eff[p.Lang])
			eff[p.Lang] = math.Inf(-1)
		}
	}
	return eff
}

// best returns the most likely language with English, the international
// default, as the fallback.
func (e *evidence) best() Language { return e.bestOr(English) }

// bestOr returns the most likely language. The default keeps the lead unless
// another alphabet scores clearly higher, so short or ambiguous input stays in
// it. Within a script the first profile of the registry is the default in the
// same way: ambiguous Latin text is English, ambiguous Cyrillic Ukrainian.
func (e *evidence) bestOr(def Language) Language {
	eff := e.effective()
	if math.IsInf(eff[def], -1) { // a Latin default without its letters reads as English
		def = English
	}
	best := def
	for l := English; l < numLanguages; l++ {
		if eff[l] > eff[best] {
			best = l
		}
	}
	if d := scriptDefault(best); eff[best]-eff[d] < defaultMargin {
		best = d
	}
	if eff[best]-eff[def] < defaultMargin {
		return def
	}
	return best
}

// scriptDefault is the first registered language of l's script.
func scriptDefault(l Language) Language { return scriptDefaults[profileOf(l).Script] }

// confident reports whether lang leads every alphabet that would decode the
// words differently by at least confidenceMargin. Alphabets that agree on the
// words so far (English and German without umlauts) need no separating.
func (e *evidence) confident(lang Language, words [][]string) bool {
	eff := e.effective()
	ref := decodeWords(words, lang)
	for l := English; l < numLanguages; l++ {
		if l != lang && eff[lang]-eff[l] < confidenceMargin && decodeWords(words, l) != ref {
			return false
		}
	}
	return true
}

// scoreCodes decodes a word with the profile's alphabet and scores it; marked
// tells whether the word has a code that only some alphabets have.
func (p *LanguageProfile) scoreCodes(codes []string) (s float64, marked bool) {
	t := p.tables
	word := make([]rune, len(codes))
	for i, c := range codes {
		r, ok := t.decode[c]
		switch {
		case !ok:
			s -= unknownPenalty
			r = UnknownRune
		case codeOwners[c] < len(profiles):
			s += markerWeight / float64(codeOwners[c])
			marked = true
		}
		word[i] = r
	}
	return s + t.textScore(word), marked
}

// scoreText scores a word of plain text as written in the profile's language;
// marked tells whether the word has a letter that only some languages write.
func (p *LanguageProfile) scoreText(word []rune) (s float64, marked bool) {
	t := p.tables
	up := make([]rune, len(word))
	for i, r := range word {
		up[i] = unicode.ToUpper(r)
		if unicode.IsLetter(r) {
			ls, m := t.letterScore(r, up[i])
			s += ls
			marked = marked || m
		}
	}
	return s + t.textScore(up), marked
}

// letterScore rates one written letter: a penalty if the language does not
// write it, a bonus if only some languages do (Є, Ñ, ı).
func (t *langTables) letterScore(r, u rune) (float64, bool) {
	for _, x := range [2]rune{r, u} { // raw first: dotless ı upper-cases to I
		if t.native[x] && letterOwners[x] > 0 {
			return markerWeight / float64(letterOwners[x]), true
		}
	}
	if t.native[u] {
		return 0, false
	}
	return -unknownPenalty, false
}

// textScore rates an upper-case word as text of the language: the letter
// frequencies (log-likelihood), the frequent words and the n-grams found in
// it. Punctuation around the word is ignored.
func (t *langTables) textScore(word []rune) float64 {
	word = trimPunct(word)
	key := make([]rune, 0, len(word)+2)
	key = append(key, '^')
	s := 0.0
	for _, r := range word {
		r = matchFold(r)
		key = append(key, r)
		if unicode.IsLetter(r) {
			s += t.letterFreq(r)
		}
	}
	key = append(key, '$')
	s += t.words[string(key[1:len(key)-1])]
	for i := range key {
		for n := 2; n <= 4 && i+n <= len(key); n++ {
			s += t.ngrams[string(key[i:i+n])]
		}
	}
	return s
}

// letterFreq is the log-likelihood ratio of a letter in the language.
func (t *langTables) letterFreq(r rune) float64 {
	if v, ok := t.variants[r]; ok {
		r = v
	}
	if f, ok := t.freq[r]; ok {
		return f
	}
	return rareScore
}

// matchFold treats Turkish İ as I when matching n-grams, so that Turkish
// statistics apply equally to typed text (where İ and I depend on casing)
// and to decoded Morse.
func matchFold(r rune) rune {
	if r == 'İ' {
		return 'I'
	}
	return r
}

func trimPunct(w []rune) []rune {
	isP := func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }
	for len(w) > 0 && isP(w[0]) {
		w = w[1:]
	}
	for len(w) > 0 && isP(w[len(w)-1]) {
		w = w[:len(w)-1]
	}
	return w
}

// callsignLike reports whether a word mixes letters and digits like a
// callsign (UR5ABC, DL1XYZ) or a signal report (5NN).
func callsignLike(codes []string) bool {
	if len(codes) < 3 || len(codes) > 8 {
		return false
	}
	nd := 0
	for _, c := range codes {
		if isDigitCode(c) {
			nd++
		}
	}
	return nd > 0 && nd < len(codes)
}

func isDigitCode(c string) bool {
	r, ok := profiles[0].tables.decode[c]
	return ok && r >= '0' && r <= '9'
}

// DetectLanguage guesses the language of plain text. Letters that only some
// alphabets have decide most cases (І Ї Є Ґ → Ukrainian, Ы Э Ъ Ё → Russian,
// ß → German, Ñ → Spanish, Ł Ś Ź → Polish); otherwise frequent letter pairs
// and words do. Text without letters, or Latin text without a clear winner,
// is English. The result is never Auto.
func DetectLanguage(text string) Language {
	var e evidence
	for _, w := range strings.Fields(text) {
		e.addText([]rune(w))
	}
	return e.best()
}

// DetectMorseLanguage guesses which alphabet Morse text was sent in. The
// codes are decoded with every alphabet and each reading is scored: codes an
// alphabet lacks count against it, codes only some alphabets have count for
// them, and the n-gram and word statistics of each reading decide the rest.
// Short or ambiguous input is English. Unparsable input is English too.
func DetectMorseLanguage(code string) Language {
	words, err := splitMorse(code)
	if err != nil {
		return English
	}
	return detectWords(words)
}

func detectWords(words [][]string) Language {
	var e evidence
	for _, w := range words {
		e.addCodes(w)
	}
	return e.best()
}

// ---- streaming ---------------------------------------------------------------

// Stream behaviour.
const (
	evidenceDecay  = 0.8  // per word: older words weigh less, so a transmission can change language
	recentWords    = 12   // words kept for the confidence check
	sessionSilence = 30.0 // seconds without signal after which the session language is forgotten
)

// langTracker chooses the alphabet of a stream in Auto mode without delaying
// the output: every character goes out on its letter gap, decoded in the
// active alphabet. Only codes that alphabets disagree on beyond the script
// (Я/Ä/Ą, Ч/Ö/Ó, Ш, Ї) wait for the end of their word while the active
// alphabet is not yet confirmed. At every word gap the evidence is updated,
// waiting characters are resolved, and the active alphabet may change for
// the following words.
//
// The last confidently detected alphabet is remembered as the session
// language. A new transmission starts in it, and a short reply with little
// evidence of its own ("ТАК", "JA") stays in it instead of falling back to
// English. It is forgotten after sessionSilence seconds without signal or on
// an explicit reset.
type langTracker struct {
	ev      evidence   // decayed scores of the current transmission
	recent  [][]string // its latest words, for the confidence check
	cur     word       // the word being received
	active  Language   // the alphabet used for output
	session Language   // the last confidently detected alphabet; Auto if none
}

type word struct {
	codes   []string
	space   bool // a word space precedes the word
	emitted int  // leading codes already output
}

func newLangTracker() langTracker { return langTracker{active: English} }

// char takes one received character; space tells whether a word space
// preceded it. It returns the text to output now.
func (t *langTracker) char(code string, space bool) string {
	w := &t.cur
	if len(w.codes) == 0 {
		w.space = space
	}
	w.codes = append(w.codes, code)
	out := ""
	if w.emitted == len(w.codes)-1 && t.emitNow(code) {
		if w.space && w.emitted == 0 {
			out = " "
		}
		r, _ := Char(code, t.active)
		out += string(r)
		w.emitted++
	}
	if len(w.codes) >= maxWordCodes {
		out += t.endWord()
	}
	return out
}

// emitNow reports whether a character can go out before its word ends: the
// active alphabet has it, and either every alphabet has the code (it is
// only a matter of script) or the active alphabet is confirmed.
func (t *langTracker) emitNow(code string) bool {
	_, ok := Char(code, t.active)
	return ok && (codeOwners[code] == len(profiles) || t.active == t.session)
}

// endWord is called at a word gap; it returns the characters that were
// waiting, decoded in the alphabet chosen with this word's evidence.
func (t *langTracker) endWord() string {
	w := t.cur
	if len(w.codes) == 0 {
		return ""
	}
	t.cur = word{}
	t.ev.scale(evidenceDecay)
	t.ev.addCodes(w.codes)
	if len(t.recent) == recentWords {
		t.recent = append(t.recent[:0], t.recent[1:]...)
	}
	t.recent = append(t.recent, w.codes)
	t.active = t.ev.bestOr(t.fallback())
	if t.ev.confident(t.active, t.recent) {
		t.session = t.active
	}
	var b []byte
	if w.space && w.emitted == 0 {
		b = append(b, ' ')
	}
	return string(appendDecoded(b, w.codes[w.emitted:], t.active))
}

// fallback is the alphabet to assume without evidence.
func (t *langTracker) fallback() Language {
	if t.session != Auto {
		return t.session
	}
	return English
}

// flush resolves the word in progress (end of stream).
func (t *langTracker) flush() string { return t.endWord() }

// newTransmission starts afresh after a long gap: the evidence is cleared,
// the session language is kept. Call flush first.
func (t *langTracker) newTransmission() {
	t.ev = evidence{}
	t.recent = t.recent[:0]
	t.active = t.fallback()
}

// forget drops the session language as well. Call flush first.
func (t *langTracker) forget() {
	t.session = Auto
	t.newTransmission()
}

func decodeWords(words [][]string, lang Language) string {
	var b []byte
	for _, w := range words {
		b = appendDecoded(append(b, ' '), w, lang)
	}
	return string(b)
}

func appendDecoded(b []byte, codes []string, lang Language) []byte {
	for _, c := range codes {
		r, ok := Char(c, lang)
		if !ok {
			r = UnknownRune
		}
		b = utf8.AppendRune(b, r)
	}
	return b
}
