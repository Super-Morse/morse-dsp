// Package morse is the pure-Go core of morse-dsp: Morse alphabets, a
// bidirectional text codec with automatic language detection, a CW audio
// synthesizer, a streaming DSP decoder and a minimal WAV reader/writer.
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

// Language selects a Morse alphabet (see LanguageProfile). The zero value,
// Auto, lets the codec and the decoder pick the alphabet from the content.
type Language int

// The supported alphabets. The order is also the tie-break order of the
// language detector: English first, Ukrainian before Russian.
const (
	Auto      Language = iota // detect from the content
	English                   // international (ITU) Latin alphabet, "en"
	Ukrainian                 // Ukrainian Cyrillic, "uk" (also accepted as "ua")
	Russian                   // Russian Cyrillic, "ru"
	German                    // ITU + Ä Ö Ü ẞ, "de"
	French                    // ITU + À Ç È É, "fr"
	Spanish                   // ITU + Á É Ñ Ó Ü, "es"
	Polish                    // ITU + Ą Ć Ę Ł Ń Ó Ś Ź Ż, "pl"
	Swedish                   // ITU + Å Ä Ö É (Æ, Ø accepted), "sv"
	Turkish                   // ITU + Ç Ğ İ Ö Ş Ü, "tr"
	numLanguages
)

// Script is the writing system of an alphabet.
type Script int

// Scripts.
const (
	Latin Script = iota
	Cyrillic
)

// UnknownRune is emitted by decoders for a code that has no character in the
// selected alphabet.
const UnknownRune = '*'

// String returns the language tag ("auto", "en", "uk", "ru", "de", ...).
func (l Language) String() string {
	if p := profileOf(l); p != nil {
		return p.Tag
	}
	return "auto"
}

// Name returns the English name of the language ("Ukrainian"), or "Auto".
func (l Language) Name() string {
	if p := profileOf(l); p != nil {
		return p.Name
	}
	return "Auto"
}

// ParseLanguage accepts "auto" (or ""), a language tag such as "en", "uk",
// "ua", "ru", "de", "fr", "es", "pl", "sv", "tr", or an English language name;
// case-insensitive.
func ParseLanguage(s string) (Language, error) {
	key := strings.ToLower(strings.TrimSpace(s))
	if key == "" || key == "auto" {
		return Auto, nil
	}
	for i := range profiles {
		p := &profiles[i]
		if key == p.Tag || key == strings.ToLower(p.Name) || containsString(p.Aliases, key) {
			return p.Lang, nil
		}
	}
	return Auto, errors.New("morse: unknown language " + quote(s) + " (want auto, en, ua, ru, de, fr, es, pl, sv or tr)")
}

// Base alphabets shared by every profile of a script.
var latinLetters = map[rune]string{
	'A': ".-", 'B': "-...", 'C': "-.-.", 'D': "-..", 'E': ".", 'F': "..-.",
	'G': "--.", 'H': "....", 'I': "..", 'J': ".---", 'K': "-.-", 'L': ".-..",
	'M': "--", 'N': "-.", 'O': "---", 'P': ".--.", 'Q': "--.-", 'R': ".-.",
	'S': "...", 'T': "-", 'U': "..-", 'V': "...-", 'W': ".--", 'X': "-..-",
	'Y': "-.--", 'Z': "--..",
}

// cyrillicLetters are the letters Russian and Ukrainian Morse code alike;
// И, І, Ї, Є, Ы, Э and Ъ live in the language profiles.
var cyrillicLetters = map[rune]string{
	'А': ".-", 'Б': "-...", 'В': ".--", 'Г': "--.", 'Д': "-..", 'Е': ".",
	'Ж': "...-", 'З': "--..", 'Й': ".---", 'К': "-.-", 'Л': ".-..", 'М': "--",
	'Н': "-.", 'О': "---", 'П': ".--.", 'Р': ".-.", 'С': "...", 'Т': "-",
	'У': "..-", 'Ф': "..-.", 'Х': "....", 'Ц': "-.-.", 'Ч': "---.", 'Ш': "----",
	'Щ': "--.-", 'Ь': "-..-", 'Ю': "..--", 'Я': ".-.-",
}

var digits = map[rune]string{
	'0': "-----", '1': ".----", '2': "..---", '3': "...--", '4': "....-",
	'5': ".....", '6': "-....", '7': "--...", '8': "---..", '9': "----.",
}

// ITU-R M.1677 punctuation plus the common amateur extras. The same codes are
// used in every language, and none of them collides with a letter.
var punctuation = map[rune]string{
	'.': ".-.-.-", ',': "--..--", '?': "..--..", '\'': ".----.", '!': "-.-.--",
	'/': "-..-.", '(': "-.--.", ')': "-.--.-", '&': ".-...", ':': "---...",
	';': "-.-.-.", '=': "-...-", '+': ".-.-.", '-': "-....-", '_': "..--.-",
	'"': ".-..-.", '$': "...-..-", '@': ".--.-.",
}

// punctAliases are typographic variants accepted on input.
var punctAliases = map[rune]rune{
	'’': '\'', // typographic apostrophe
	'ʼ': '\'', // Ukrainian apostrophe (modifier letter)
	'‘': '\'',
}

func scriptLetters(s Script) map[rune]string {
	if s == Cyrillic {
		return cyrillicLetters
	}
	return latinLetters
}

// upper upper-cases r; Turkish uses its own casing (i → İ, ı → I).
func upper(r rune, lang Language) rune {
	if lang == Turkish {
		return unicode.TurkishCase.ToUpper(r)
	}
	return unicode.ToUpper(r)
}

// Code returns the Morse code ("." and "-") for a character. Letters are
// case-insensitive, and every supported letter is accepted in every language:
// lang only decides between the codes of a letter that differs between
// alphabets (Cyrillic «И» is ".." in Russian but "-.--" in Ukrainian, the
// default) and selects Turkish casing.
func Code(r rune, lang Language) (string, bool) {
	e, ok := encodeRune(r, lang)
	return e.code, ok
}

// encodeRune looks a character up in the language's own letters first, then
// in the table of every supported character.
func encodeRune(r rune, lang Language) (encodeEntry, bool) {
	r = upper(r, lang)
	if p := profileOf(lang); p != nil {
		if c, ok := p.tables.encode[r]; ok {
			return encodeEntry{c, canonical(p, r)}, true
		}
	}
	e, ok := encodeTable[r]
	return e, ok
}

// Char returns the character for a Morse code in the given alphabet. Auto
// decodes with the international (English) alphabet: a single code carries
// no language information.
func Char(code string, lang Language) (rune, bool) {
	r, ok := profileOrEnglish(lang).tables.decode[code]
	return r, ok
}

// Alphabet returns a copy of the code table used to decode the given
// language (code → character); Auto returns the international alphabet.
func Alphabet(lang Language) map[string]rune {
	src := profileOrEnglish(lang).tables.decode
	out := make(map[string]rune, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func quote(s string) string { return "\"" + s + "\"" }
