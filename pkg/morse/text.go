package morse

import (
	"strconv"
	"strings"
	"unicode"
)

// Canonical separators produced by EncodeText and NormalizeMorse.
const (
	LetterSep = " "   // between letters of a word
	WordSep   = " / " // between words
)

// UnknownCharsError reports input characters that have no Morse code.
// EncodeText skips them and still returns a usable result.
type UnknownCharsError struct {
	Chars []rune // distinct characters, in order of first appearance
}

func (e *UnknownCharsError) Error() string {
	parts := make([]string, len(e.Chars))
	for i, r := range e.Chars {
		parts[i] = strconv.QuoteRune(r)
	}
	return "morse: unsupported characters skipped: " + strings.Join(parts, ", ")
}

// UnknownCodesError reports Morse codes that have no character in the selected
// alphabet. DecodeText replaces them with UnknownRune and still returns a
// usable result.
type UnknownCodesError struct {
	Codes []string // distinct codes, in order of first appearance
}

func (e *UnknownCodesError) Error() string {
	return "morse: unknown codes replaced with " + strconv.QuoteRune(UnknownRune) + ": " + strings.Join(e.Codes, ", ")
}

// SyntaxError reports a character that cannot appear in Morse text.
type SyntaxError struct {
	Offset int  // byte offset in the input
	Char   rune // offending character
}

func (e *SyntaxError) Error() string {
	return "morse: invalid character " + strconv.QuoteRune(e.Char) + " at offset " + strconv.Itoa(e.Offset) +
		" (Morse text may contain only dots, dashes, spaces and / as word separator)"
}

// EncodeText converts plain text into Morse text: letters are separated by a
// single space and words by " / ". Input is case-insensitive and may mix Latin
// and Cyrillic. Runs of whitespace separate words.
//
// lang matters only for Cyrillic «И» (Russian "..", Ukrainian "-.--"). With
// English, the Cyrillic variant is guessed from the text (DetectLanguage).
//
// Characters without a Morse code are skipped; in that case the returned error
// is a *UnknownCharsError and the returned code is still valid.
func EncodeText(text string, lang Language) (string, error) {
	if lang != Russian && lang != Ukrainian {
		lang = DetectLanguage(text)
	}
	var words []string
	var unknown []rune
	for _, word := range strings.Fields(text) {
		var letters []string
		for _, r := range word {
			if c, ok := Code(r, lang); ok {
				letters = append(letters, c)
			} else {
				unknown = appendUniqueRune(unknown, r)
			}
		}
		if len(letters) > 0 {
			words = append(words, strings.Join(letters, LetterSep))
		}
	}
	code := strings.Join(words, WordSep)
	if len(unknown) > 0 {
		return code, &UnknownCharsError{Chars: unknown}
	}
	return code, nil
}

// NormalizeText returns text as it round-trips through EncodeText and
// DecodeText in its own language: upper case, aliases folded (Ё → Е, Ґ → Г,
// ’ → '), unsupported characters removed, words separated by single spaces.
func NormalizeText(text string) string {
	var b strings.Builder
	for _, word := range strings.Fields(text) {
		start := b.Len()
		if start > 0 {
			b.WriteByte(' ')
		}
		n := 0
		for _, r := range word {
			if _, ok := Code(r, English); ok {
				b.WriteRune(canonicalRune(r))
				n++
			}
		}
		if n == 0 { // the whole word was unsupported: drop its separator too
			s := b.String()[:start]
			b.Reset()
			b.WriteString(s)
		}
	}
	return b.String()
}

// DecodeText converts Morse text into plain text using the given alphabet.
//
// The parser is lenient about notation: dots may be written as . · • ∙ and
// dashes as - _ − – —. Letters are separated by whitespace; words by "/", "|",
// a line break, or two or more consecutive spaces.
//
// Codes missing from the alphabet become UnknownRune and the returned error is
// a *UnknownCodesError (the text is still usable). Characters that cannot
// appear in Morse text yield a *SyntaxError and an empty result.
func DecodeText(code string, lang Language) (string, error) {
	words, err := splitMorse(code)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var unknown []string
	for i, word := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		for _, c := range word {
			r, ok := Char(c, lang)
			if !ok {
				r = UnknownRune
				unknown = appendUniqueString(unknown, c)
			}
			b.WriteRune(r)
		}
	}
	if len(unknown) > 0 {
		return b.String(), &UnknownCodesError{Codes: unknown}
	}
	return b.String(), nil
}

// NormalizeMorse rewrites Morse text in canonical notation ("." and "-",
// letters separated by " ", words by " / ").
func NormalizeMorse(code string) (string, error) {
	words, err := splitMorse(code)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = strings.Join(w, LetterSep)
	}
	return strings.Join(parts, WordSep), nil
}

// IsMorse reports whether s looks like Morse text rather than plain text: it
// consists only of dots, dashes and separators, and contains at least one
// dot or dash.
func IsMorse(s string) bool {
	seen := false
	for _, r := range s {
		switch {
		case isDot(r) || isDash(r):
			seen = true
		case isWordBreak(r) || unicode.IsSpace(r):
		default:
			return false
		}
	}
	return seen
}

func isDot(r rune) bool {
	switch r {
	case '.', '·', '•', '∙', '⋅':
		return true
	}
	return false
}

func isDash(r rune) bool {
	switch r {
	case '-', '_', '−', '–', '—', '‒', '―':
		return true
	}
	return false
}

func isWordBreak(r rune) bool {
	return r == '/' || r == '|' || r == '\n' || r == '\r'
}

// splitMorse parses Morse text into words of canonical letter codes.
func splitMorse(s string) ([][]string, error) {
	var p morseParser
	for i, r := range s {
		switch {
		case isDot(r):
			p.symbol('.')
		case isDash(r):
			p.symbol('-')
		case isWordBreak(r):
			p.endToken()
			p.wordBreak = true
		case unicode.IsSpace(r):
			p.endToken()
			p.spaces++
		default:
			return nil, &SyntaxError{Offset: i, Char: r}
		}
	}
	return p.finish(), nil
}

// morseParser collects letter tokens; separators seen since the previous
// token decide whether the next token starts a new word.
type morseParser struct {
	words     [][]string
	cur       []string // letters of the current word
	tok       []byte   // the letter being read
	spaces    int      // spaces since the last token
	wordBreak bool     // "/", "|" or a line break since the last token
}

func (p *morseParser) symbol(c byte) {
	if len(p.tok) == 0 {
		if (p.wordBreak || p.spaces >= 2) && len(p.cur) > 0 {
			p.words = append(p.words, p.cur)
			p.cur = nil
		}
		p.spaces, p.wordBreak = 0, false
	}
	p.tok = append(p.tok, c)
}

func (p *morseParser) endToken() {
	if len(p.tok) > 0 {
		p.cur = append(p.cur, string(p.tok))
		p.tok = p.tok[:0]
	}
}

func (p *morseParser) finish() [][]string {
	p.endToken()
	if len(p.cur) > 0 {
		p.words = append(p.words, p.cur)
	}
	return p.words
}

func appendUniqueRune(s []rune, r rune) []rune {
	for _, x := range s {
		if x == r {
			return s
		}
	}
	return append(s, r)
}

func appendUniqueString(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
