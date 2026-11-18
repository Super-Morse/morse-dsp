// Package morse is the pure-Go core of morse-dsp: Morse alphabets, a
// bidirectional text codec, a CW audio synthesizer, a streaming DSP decoder
// and a minimal WAV reader/writer.
//
// The package depends only on the Go standard library and never touches the
// operating system (no os, syscall or cgo), so it compiles unchanged for
// GOOS=js GOARCH=wasm.
package morse

import (
	"errors"
	"strings"
	"unicode"
)

// Language selects the Morse alphabet. Latin, Russian and Ukrainian letters
// share most codes, so decoding always needs to know which one is meant.
// Encoding needs it only for Cyrillic «И», which is ".." in Russian but
// "-.--" in Ukrainian.
type Language int

const (
	// English is the international (ITU) Latin alphabet, "EN".
	English Language = iota
	// Russian is the Russian Cyrillic alphabet, "RU".
	Russian
	// Ukrainian is the Ukrainian Cyrillic alphabet, "UA".
	Ukrainian
)

// UnknownRune is emitted by decoders for a code that has no character in the
// selected alphabet.
const UnknownRune = '*'

// String returns the language tag: "en", "ru" or "uk".
func (l Language) String() string {
	switch l {
	case Russian:
		return "ru"
	case Ukrainian:
		return "uk"
	}
	return "en"
}

// ParseLanguage accepts "en", "ru" and "uk"/"ua" plus a few long forms
// (case-insensitive). The empty string means English.
func ParseLanguage(s string) (Language, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "en", "eng", "english", "latin", "lat":
		return English, nil
	case "ru", "rus", "russian":
		return Russian, nil
	case "uk", "ua", "ukr", "ukrainian":
		return Ukrainian, nil
	}
	return English, errors.New("morse: unknown language " + quote(s) + " (want en, ru or ua)")
}

var latinLetters = map[rune]string{
	'A': ".-", 'B': "-...", 'C': "-.-.", 'D': "-..", 'E': ".", 'F': "..-.",
	'G': "--.", 'H': "....", 'I': "..", 'J': ".---", 'K': "-.-", 'L': ".-..",
	'M': "--", 'N': "-.", 'O': "---", 'P': ".--.", 'Q': "--.-", 'R': ".-.",
	'S': "...", 'T': "-", 'U': "..-", 'V': "...-", 'W': ".--", 'X': "-..-",
	'Y': "-.--", 'Z': "--..",
}

var russianLetters = map[rune]string{
	'А': ".-", 'Б': "-...", 'В': ".--", 'Г': "--.", 'Д': "-..", 'Е': ".",
	'Ж': "...-", 'З': "--..", 'И': "..", 'Й': ".---", 'К': "-.-", 'Л': ".-..",
	'М': "--", 'Н': "-.", 'О': "---", 'П': ".--.", 'Р': ".-.", 'С': "...",
	'Т': "-", 'У': "..-", 'Ф': "..-.", 'Х': "....", 'Ц': "-.-.", 'Ч': "---.",
	'Ш': "----", 'Щ': "--.-", 'Ъ': "--.--", 'Ы': "-.--", 'Ь': "-..-",
	'Э': "..-..", 'Ю': "..--", 'Я': ".-.-",
}

// Ukrainian Morse reuses the Russian codes: І takes the code of Russian И,
// И takes Ы, Є takes Э, and Ї gets its own ".---.". Ґ has no code of its own
// and is sent as Г (see aliases).
var ukrainianLetters = map[rune]string{
	'А': ".-", 'Б': "-...", 'В': ".--", 'Г': "--.", 'Д': "-..", 'Е': ".",
	'Є': "..-..", 'Ж': "...-", 'З': "--..", 'И': "-.--", 'І': "..", 'Ї': ".---.",
	'Й': ".---", 'К': "-.-", 'Л': ".-..", 'М': "--", 'Н': "-.", 'О': "---",
	'П': ".--.", 'Р': ".-.", 'С': "...", 'Т': "-", 'У': "..-", 'Ф': "..-.",
	'Х': "....", 'Ц': "-.-.", 'Ч': "---.", 'Ш': "----", 'Щ': "--.-", 'Ь': "-..-",
	'Ю': "..--", 'Я': ".-.-",
}

var digits = map[rune]string{
	'0': "-----", '1': ".----", '2': "..---", '3': "...--", '4': "....-",
	'5': ".....", '6': "-....", '7': "--...", '8': "---..", '9': "----.",
}

// ITU-R M.1677 punctuation plus the common amateur extras. The same codes are
// used in every language, so none of them collides with a Cyrillic letter.
var punctuation = map[rune]string{
	'.': ".-.-.-", ',': "--..--", '?': "..--..", '\'': ".----.", '!': "-.-.--",
	'/': "-..-.", '(': "-.--.", ')': "-.--.-", '&': ".-...", ':': "---...",
	';': "-.-.-.", '=': "-...-", '+': ".-.-.", '-': "-....-", '_': "..--.-",
	'"': ".-..-.", '$': "...-..-", '@': ".--.-.",
}

// aliases are accepted on input and folded to a canonical character.
var aliases = map[rune]rune{
	'Ё': 'Е',
	'Ґ': 'Г',
	'’': '\'', // ’ typographic apostrophe
	'ʼ': '\'', // ʼ Ukrainian apostrophe (modifier letter)
	'‘': '\'', // ‘
}

var (
	common       map[rune]string // Latin letters, digits, punctuation
	decodeTables [3]map[string]rune
)

func init() {
	common = make(map[rune]string, 64)
	for _, m := range []map[rune]string{latinLetters, digits, punctuation} {
		for r, c := range m {
			common[r] = c
		}
	}
	build := func(letters map[rune]string) map[string]rune {
		t := make(map[string]rune, 80)
		for _, m := range []map[rune]string{letters, digits, punctuation} {
			for r, c := range m {
				t[c] = r
			}
		}
		return t
	}
	decodeTables[English] = build(latinLetters)
	decodeTables[Russian] = build(russianLetters)
	decodeTables[Ukrainian] = build(ukrainianLetters)
}

func validLanguage(l Language) Language {
	if l == Russian || l == Ukrainian {
		return l
	}
	return English
}

// canonicalRune upper-cases r and folds aliases (Ё → Е, Ґ → Г, ’ → ').
func canonicalRune(r rune) rune {
	r = unicode.ToUpper(r)
	if a, ok := aliases[r]; ok {
		return a
	}
	return r
}

// Code returns the Morse code ("." and "-") for a character. Letters are
// case-insensitive; Latin, Russian and Ukrainian letters are all accepted in
// every language. lang only decides the code of Cyrillic «И»: Ukrainian
// "-.--", otherwise Russian "..".
func Code(r rune, lang Language) (string, bool) {
	r = canonicalRune(r)
	if c, ok := common[r]; ok {
		return c, true
	}
	primary, secondary := russianLetters, ukrainianLetters
	if lang == Ukrainian {
		primary, secondary = ukrainianLetters, russianLetters
	}
	if c, ok := primary[r]; ok {
		return c, true
	}
	c, ok := secondary[r]
	return c, ok
}

// Char returns the character for a Morse code in the given alphabet.
func Char(code string, lang Language) (rune, bool) {
	r, ok := decodeTables[validLanguage(lang)][code]
	return r, ok
}

// Alphabet returns a copy of the code table used to decode the given
// language (code → character).
func Alphabet(lang Language) map[string]rune {
	src := decodeTables[validLanguage(lang)]
	out := make(map[string]rune, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// DetectLanguage guesses the language of plain text from its letters:
// Ukrainian for the letters І, Ї, Є, Ґ; Russian for Ы, Э, Ъ, Ё (whichever
// group is more frequent wins); Russian for other Cyrillic text and English
// for text without Cyrillic letters.
func DetectLanguage(text string) Language {
	var ua, ru, cyr int
	for _, r := range text {
		switch unicode.ToUpper(r) {
		case 'І', 'Ї', 'Є', 'Ґ':
			ua++
		case 'Ы', 'Э', 'Ъ', 'Ё':
			ru++
		}
		if unicode.Is(unicode.Cyrillic, r) {
			cyr++
		}
	}
	switch {
	case ua > ru:
		return Ukrainian
	case cyr > 0:
		return Russian
	}
	return English
}

func quote(s string) string { return "\"" + s + "\"" }
