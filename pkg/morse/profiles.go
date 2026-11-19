package morse

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Letter is a letter of a language-specific alphabet and its Morse code.
type Letter struct {
	Char rune
	Code string
}

// LanguageProfile describes one Morse alphabet and the statistics used to
// recognize its language. Profiles are plain data: adding a language means
// adding an entry to the registry, and the code tables and detector weights
// are derived from it at start-up.
type LanguageProfile struct {
	Lang    Language
	Tag     string   // ISO 639-1 code, as printed by Language.String
	Name    string   // English name
	Aliases []string // further names accepted by ParseLanguage (lower case)
	Script  Script

	// Letters extend the script's base alphabet. Where two letters share a
	// code, decoding produces the first one.
	Letters []Letter
	// Variants are letters that have no code of their own and are sent as
	// another letter (Ґ as Г, Ø as Ö, Í as I).
	Variants map[rune]rune
	// Freq is the letter frequency table in percent ("E12.7 T9.1 ...").
	// Letters missing from it count as very rare.
	Freq string
	// NGrams are the letter pairs (and a few anchored fragments: ^ marks the
	// start of a word, $ its end) most typical of the language, in three
	// tiers of decreasing weight separated by "|".
	NGrams string
	// Words are frequent words of the language.
	Words string
	// Tokens identify the language on their own, even in a short burst. For
	// English these are ham radio abbreviations, Q-codes and reports, so
	// that "CQ", "QTH" or "599" always read as ITU.
	Tokens string

	tables *langTables
}

// langTables are derived from a profile at start-up.
type langTables struct {
	decode   map[string]rune    // code → character: base letters, Letters, digits, punctuation
	encode   map[rune]string    // the profile's own Letters and Variants → code
	native   map[rune]bool      // letters written in the language
	variants map[rune]rune      // Variants
	freq     map[rune]float64   // log-likelihood ratio of each letter
	ngrams   map[string]float64 // NGrams with their tier weights
	words    map[string]float64 // Words and Tokens with their bonus
}

// profiles is the registry, in Language order (profiles[i].Lang == i+1).
var profiles = []LanguageProfile{
	{
		Lang: English, Tag: "en", Name: "English", Aliases: []string{"eng", "latin", "itu"}, Script: Latin,
		// ham traffic: Q, X, Z and K are more common than in prose (CQ, TNX, QRZ)
		Freq: "E12.7 T9.1 A8.2 O7.5 I7.0 N6.7 S6.3 H6.1 R6.0 D4.3 L4.0 C2.8 U2.8 M2.4 W2.4 F2.2 G2.0 Y2.0 P1.9 B1.5 " +
			"V1.0 K1.0 Q0.5 X0.5 Z0.3 J0.3",
		NGrams: "TH HE IN ER AN RE ON AT EN ND ^TH | TI ES OR TE OF ED IS IT AL AR ST TO NT NG SE HA AS OU IO LE VE CO ME DE HI ED$ NG$ " +
			"| RI RO IC NE EA RA CE LI CH LL BE MA SI OM UR LY$ ^WH",
		Words: "THE AND OF TO IN IS IT YOU THAT WAS FOR ON ARE WITH AS BE AT BY THIS HAVE FROM OR ONE HAD NOT BUT WHAT ALL " +
			"WERE WE WHEN YOUR CAN THERE HELLO WORLD",
		Tokens: "CQ DE K KN BK AR SK PSE QTH QSL QRZ QSO QRM QRN QRP QRT QRV QSB QSY RST TNX TKS TU UR ES FB OM YL XYL " +
			"GM GA GE GN HW CPY NAME OP WX RIG ANT PWR DX TEST 73 88 5NN 599 HR HI SOS AGN CUL",
	},
	{
		Lang: Ukrainian, Tag: "uk", Name: "Ukrainian", Aliases: []string{"ua", "ukr"}, Script: Cyrillic,
		Letters:  []Letter{{'И', "-.--"}, {'І', ".."}, {'Ї', ".---."}, {'Є', "..-.."}},
		Variants: map[rune]rune{'Ґ': 'Г'},
		Freq: "О9.4 А8.3 Н6.7 И6.0 І5.9 Т5.0 Е4.9 В4.6 Р4.6 С4.1 К3.7 Л3.6 У3.4 Д3.2 М3.1 П2.8 Я2.3 З2.2 Ь1.9 Г1.6 " +
			"Б1.6 Ч1.4 Й1.2 Х1.1 Ц1.0 Ю0.8 Ж0.8 Ї0.8 Ш0.7 Щ0.6 Є0.5 Ф0.2",
		NGrams: cyrillicNGrams + " НІ ВІ ІВ ЦІ ДІ ТІ ЛІ РІ ІЙ ЛИ ТИ ИЙ ВИ ИН МИ НИ ЩО ЯК " +
			"| ЦЬ ЬО ЙО СЬ ДЖ 'Я 'Ю 'Є ЄТ ЄЮ ЇХ ЇЙ ЗІ МІ КІ ЧИ ННЯ$ ТИ$ ІВ$ ЦЬ$ ИЙ$ ^Є ^ВІ",
		Words: "І Й ТА ЩО ЯК ЦЕ НЕ НА В У З ДО ЗА ВІД ПРО ДЛЯ АЛЕ ТАКОЖ ДУЖЕ ТУТ ТАМ ВЖЕ ЩЕ ЙОГО ЇЇ ЇХ ВІН ВОНА ВОНИ " +
			"МИ ВИ ТИ Я МЕНІ ТЕБЕ ТОБІ НАС ВАС БУВ БУЛА БУЛО БУЛИ Є МОЖНА ТРЕБА ЦЯ ЦЕЙ ЦІ ТОЙ ТІ ЯКИЙ ЯКА ЯКЕ ЯКІ ХТО " +
			"СЬОГОДНІ ЗАВТРА ВЧОРА ПРИВІТ ДЯКУЮ ТАК НІ ДОБРЕ ДЕНЬ ЧИ ЯКЩО КОЛИ ЩОБ БО ТОМУ ТІЛЬКИ ЛИШЕ АБО ПІСЛЯ ЧЕРЕЗ " +
			"МІЖ ПІД НАД БЕЗ ПРИ СВІЙ НАШ ВАШ МІЙ МОЯ ЧАС РІК ЛЮДИ ЖИТТЯ СПРАВИ ГАРНО ВІДПОВІДЬ ЧЕКАЄМО " +
			"ЕФІР ЕФІРУ ЕФІРІ ЕФІРЕ ЗВ'ЯЗОК ЗВ'ЯЗКУ АНТЕНА СИГНАЛ ПРИЙОМ ПЕРЕВІРКА ПЕРЕДАЧА РАДІО",
	},
	{
		Lang: Russian, Tag: "ru", Name: "Russian", Aliases: []string{"rus"}, Script: Cyrillic,
		Letters:  []Letter{{'И', ".."}, {'Ы', "-.--"}, {'Э', "..-.."}, {'Ъ', "--.--"}},
		Variants: map[rune]rune{'Ё': 'Е'},
		Freq: "О11.0 Е8.5 А8.0 И7.4 Н6.7 Т6.3 С5.5 Р4.7 В4.5 Л4.4 К3.5 М3.2 Д3.0 П2.8 У2.6 Я2.0 Ы1.9 Ь1.7 Г1.7 З1.7 " +
			"Б1.6 Ч1.4 Й1.2 Х1.0 Ж0.9 Ш0.7 Ю0.6 Ц0.5 Щ0.4 Э0.3 Ф0.3 Ъ0.04",
		// Ukrainian never writes Ь after Ш, Ч or Ж: those pairs are Russian.
		NGrams: cyrillicNGrams + " НИ ЛИ ИТ ИН ВИ ТИ ИЕ ИЙ ЖИ ШЬ ЧЬ ЖЬ " +
			"| ЫЙ ЫЕ ЫХ ЫЛ ТЫ ВЫ МЫ ЧТ ЕГ ЕЕ ЭТ ^ЭТ ИЯ$ ИЕ$ ТЬ$ ОГО$ ЫЙ$ ЕШЬ$",
		Words: "И В НЕ НА Я ЧТО ОН С КАК А ЭТО ПО НО ОНА ТАК ЕГО ВСЕ ЕЕ ОНИ БЫЛ БЫЛА БЫЛО БЫЛИ МНЕ ЕЩЕ УЖЕ ТОЛЬКО ДЛЯ " +
			"ДА НЕТ ГДЕ КОГДА ОЧЕНЬ ТОЖЕ ЕСЛИ ИЛИ ИЗ ЗА ДО ОТ У К МЫ ВЫ ТЫ ЖЕ БЫ ЛИ НИ ВОТ ЕЕ ИХ СЕБЯ ТЕБЯ ВАС НАС " +
			"ЭТОТ ЭТА ЭТИ КОТОРЫЙ КОТОРАЯ КТО ЧТОБЫ ПОТОМУ ТЕПЕРЬ СЕЙЧАС ЗДЕСЬ ТАМ ТУТ МОЖНО НАДО ПОСЛЕ ЧЕРЕЗ МЕЖДУ " +
			"ПОД НАД БЕЗ ПРИ СВОЙ НАШ ВАШ МОЙ МОЯ ВРЕМЯ ГОД ЛЮДИ ЖИЗНЬ ДЕЛА ДЕЛО ОТВЕТ ЖДЕМ ХОРОШО СЕГОДНЯ ЗАВТРА ВЧЕРА " +
			"ПРИВЕТ СПАСИБО ЭФИР ЭФИРА ЭФИРЕ СВЯЗЬ СВЯЗИ АНТЕННА СИГНАЛ ПРИЕМ ПРОВЕРКА ПЕРЕДАЧА РАДИО",
	},
	{
		Lang: German, Tag: "de", Name: "German", Aliases: []string{"ger", "deu", "deutsch"}, Script: Latin,
		Letters:  []Letter{{'Ä', ".-.-"}, {'Ö', "---."}, {'Ü', "..--"}, {'ẞ', "...--.."}},
		Variants: map[rune]rune{'ß': 'ẞ'},
		Freq: "E16.9 N10.5 I8.0 S6.9 R6.9 T5.8 A5.6 D5.0 H5.0 U3.8 L3.6 C3.2 G3.0 M2.6 O2.2 B2.0 W1.8 F1.5 K1.3 Z1.2 " +
			"V0.8 P0.7 Ü0.7 Ä0.5 ẞ0.4 Ö0.3 J0.2 X0.05 Y0.05 Q0.02",
		NGrams: "ER EN CH DE EI TE IN ND IE GE | ST NE BE ES UN RE AN HE AU NG SE IT DI IC SC LE DA NS IS ÜR ÄN ÖR ^GE EN$ " +
			"| SS TS ZU EL ET UE ÄH ÜB ÖN ÜN ÄT ÖS ÄR WE GT ẞE CHT",
		Words: "DER DIE UND IN DEN VON ZU DAS MIT SICH DES AUF FÜR IST IM DEM NICHT EIN EINE ALS AUCH ES AN WIR ICH SIE ER " +
			"WIE ODER ABER NACH BEI AUS NOCH ÜBER SO WENN NUR WAR HALLO DANKE",
	},
	{
		Lang: French, Tag: "fr", Name: "French", Aliases: []string{"fre", "fra", "francais", "français"}, Script: Latin,
		Letters:  []Letter{{'À', ".--.-"}, {'Ç', "-.-.."}, {'È', ".-..-"}, {'É', "..-.."}},
		Variants: map[rune]rune{'Â': 'A', 'Ê': 'E', 'Ë': 'E', 'Î': 'I', 'Ï': 'I', 'Ô': 'O', 'Û': 'U', 'Ù': 'U', 'Ÿ': 'Y'},
		Freq: "E14.7 S7.9 A7.6 I7.5 T7.2 N7.1 R6.6 U6.3 O5.8 L5.5 D3.7 C3.3 M3.0 P2.5 É1.9 V1.6 Q1.4 F1.1 B0.9 G0.9 " +
			"H0.7 J0.6 À0.5 X0.4 È0.3 Y0.3 Ç0.1 Z0.1 K0.05 W0.05",
		NGrams: "ES LE DE EN ON NT RE ER TE ET | EL AN SE LA AI NE OU QU ME IT IE EM UR IS EC UE TI RA ÉT ÉE RÉ DÉ ES$ NT$ " +
			"| CE CO PA US LL TR SS EU ÈR ÈS ÇA ÇO AU ^L' ^D' ÉS ÉR",
		Words: "LE LA LES DE DES ET EN UN UNE DU EST QUE QUI DANS POUR PAS AU SUR CE IL ELLE NOUS VOUS À OU SE NE PAR PLUS " +
			"SON SA MAIS AVEC TRÈS ÉTÉ ÇA BONJOUR MERCI",
	},
	{
		Lang: Spanish, Tag: "es", Name: "Spanish", Aliases: []string{"spa", "espanol", "español"}, Script: Latin,
		Letters:  []Letter{{'Á', ".--.-"}, {'É', "..-.."}, {'Ñ', "--.--"}, {'Ó', "---."}, {'Ü', "..--"}},
		Variants: map[rune]rune{'Í': 'I', 'Ú': 'U'},
		Freq: "E13.7 A12.5 O8.7 S8.0 R6.9 N6.7 I6.9 D5.9 L5.0 C4.7 T4.6 U4.1 M3.2 P2.5 B1.4 G1.0 V0.9 Y0.9 Q0.9 Ó0.8 " +
			"H0.7 F0.7 Á0.5 Z0.5 J0.4 É0.4 Ñ0.3 X0.2 Ü0.02 K0.02 W0.02",
		NGrams: "DE ES EN EL LA OS AR UE RA RE | ER AS ON ST AD AL OR TA CO CI NT IA TE AN SE DO NA IO LO ÓN OS$ AS$ " +
			"| QU PO CA ME UN TO LE NO MO SA RI IE DA ÑO ÑA ÁS ÉS ÓN$ DAD$",
		Words: "DE LA QUE EL EN Y A LOS SE DEL LAS UN POR CON NO UNA SU PARA ES AL LO COMO MÁS PERO SUS LE YA O ESTE MUY " +
			"HOLA GRACIAS AÑO SEÑOR ESPAÑA",
	},
	{
		Lang: Polish, Tag: "pl", Name: "Polish", Aliases: []string{"pol", "polski"}, Script: Latin,
		Letters: []Letter{{'Ą', ".-.-"}, {'Ć', "-.-.."}, {'Ę', "..-.."}, {'Ł', ".-..-"}, {'Ń', "--.--"},
			{'Ó', "---."}, {'Ś', "...-..."}, {'Ź', "--..-."}, {'Ż', "--..-"}},
		Freq: "A8.9 I8.2 O7.8 E7.7 Z5.6 N5.5 R4.7 W4.7 S4.3 T4.0 C4.0 Y3.8 K3.5 D3.3 P3.1 M2.8 U2.5 J2.3 L2.1 Ł1.8 " +
			"B1.5 G1.4 Ę1.1 H1.1 Ą1.0 Ó0.9 Ż0.8 Ś0.7 Ć0.4 F0.3 Ń0.2 Ź0.06",
		NGrams: "IE NI ZE CZ PR NA PO RZ OW SZ | RA ST EN AN WA KI DZ OR NO TA ZA AL ER IA ŁA SK OD CH WI NE ĘS " +
			"| ÓW SĄ IĘ ŚĆ ŻE ŁO ŁE AŁ ĄC ŃS RÓ JE TO OS DO ÓW$ IĘ$ AĆ$ OŚĆ",
		Words: "I W NIE NA SIĘ Z JEST ŻE DO TO JAK ALE PO CO TAK ZA OD JA TY ON ONA MY WY CZY JUŻ TEŻ BYŁ BYŁA " +
			"DZIĘKUJĘ CZEŚĆ DZIEŃ DOBRY",
	},
	{
		Lang: Swedish, Tag: "sv", Name: "Swedish", Aliases: []string{"swe", "svenska", "nordic"}, Script: Latin,
		Letters:  []Letter{{'Å', ".--.-"}, {'Ä', ".-.-"}, {'Ö', "---."}, {'É', "..-.."}},
		Variants: map[rune]rune{'Æ': 'Ä', 'Ø': 'Ö'},
		Freq: "E10.1 A9.4 N8.5 R8.4 T7.7 S6.6 I5.8 L5.3 D4.7 O4.5 M3.5 K3.1 G2.9 V2.4 H2.1 F2.0 U1.9 Ä1.8 P1.8 B1.5 " +
			"C1.5 Å1.3 Ö1.3 Y0.7 J0.6 X0.2 W0.1 É0.05 Z0.02 Q0.02",
		NGrams: "EN ER AN DE ET AR TT TE ST IN | OR GE ND RA ON LL KA SK NA ÄR ÖR NG SO IG LA VA IL ÅR ÅN " +
			"| HÖ FÖ MÅ PÅ GÅ ÄN ÄL ÖV KÄ SÄ TÄ ÄT ^SJ ^SK",
		Words: "OCH ATT DET I EN SOM PÅ ÄR AV FÖR MED TILL DEN HAR DE INTE OM ETT MEN JAG VI DU HAN HON SÅ KAN HEJ TACK",
	},
	{
		Lang: Turkish, Tag: "tr", Name: "Turkish", Aliases: []string{"tur", "turkce", "türkçe"}, Script: Latin,
		Letters: []Letter{{'Ç', "-.-.."}, {'Ğ', "--.-."}, {'İ', ".-..-"}, {'Ö', "---."}, {'Ş', ".--.."}, {'Ü', "..--"}},
		// dotless ı: its capital is plain I; listed so that the detector sees it
		Variants: map[rune]rune{'ı': 'I'},
		Freq: "A11.9 E8.9 I13.7 N7.5 R6.9 L5.9 D4.7 K4.7 M3.8 Y3.3 U3.2 T3.1 S3.0 B2.8 O2.5 Ü1.9 Ş1.8 Z1.5 G1.3 Ç1.2 " +
			"H1.1 Ğ1.1 V1.0 C1.0 P0.9 Ö0.8 F0.5 J0.03",
		NGrams: "AR LA AN ER İN LE DE EN IN DA | KA İR Rİ Bİ MA Lİ Nİ Dİ YA AL EK LI IR TA RA İL EL Sİ LAR$ LER$ " +
			"| ÇI ÇE ÇA ŞI ŞE ŞA ĞI ĞU Ğİ ÜN ÜR ÖR ÖN YO OR AĞ EĞ ÜZ Kİ Mİ",
		Words: "VE BİR BU DA DE İÇİN İLE NE Kİ O DAHA ÇOK GİBİ EN VAR YOK BEN SEN Mİ MERHABA EVET HAYIR TEŞEKKÜRLER",
	},
}

// cyrillicNGrams are the first two tiers shared by Russian and Ukrainian;
// each profile continues the second tier with its own pairs and adds a third.
const cyrillicNGrams = "НА ПО СТ ТО ЕН НО РА КО ПР ТА ОВ ВА | ОР РО ЗА ДО НЕ ЛА АН ОД ЕР ЛЕ РЕ ЕТ ВО ОС ЕС АЛ ОЛ ЛО КА ГО " +
	"НН ЛЬ ОМ ОТ ЕЛ ДЕ"

// Detector weights.
const (
	unknownPenalty = 6.0 // a code or letter the alphabet does not have
	markerWeight   = 4.0 // a letter or code only some alphabets have, split between them
	wordBonus      = 3.0 // a frequent word (half for one-letter words)
	tokenBonus     = 8.0 // a signal word (ham abbreviation, Q-code)
	callsignBonus  = 6.0 // a callsign-like word (letters and digits) reads as ITU English

	uniformLetter = 1.0 / 30 // reference letter probability for Freq
	rareLetter    = 0.0002   // probability of a letter missing from Freq
)

// rareScore is the log-likelihood ratio of a letter missing from Freq.
var rareScore = math.Log(rareLetter / uniformLetter)

var tierWeights = [...]float64{3, 2, 1}

// encodeEntry is how a character is sent: its code and the character it
// decodes back to (Ґ → Г, Ø → Ö, ’ → ').
type encodeEntry struct {
	code  string
	canon rune
}

var (
	encodeTable    map[rune]encodeEntry   // every supported character, any language
	codeOwners     map[string]int         // how many alphabets decode a code
	letterOwners   map[rune]int           // how many alphabets have a letter beyond their script's base
	scriptDefaults [Cyrillic + 1]Language // the first registered language of each script
)

func init() {
	encodeTable = make(map[rune]encodeEntry, 160)
	for _, m := range []map[rune]string{digits, punctuation, latinLetters, cyrillicLetters} {
		for r, c := range m {
			encodeTable[r] = encodeEntry{c, r}
		}
	}
	for r, to := range punctAliases {
		encodeTable[r] = encodeEntry{punctuation[to], to}
	}
	codeOwners = make(map[string]int, 120)
	letterOwners = make(map[rune]int, 64)
	for i := range profiles {
		p := &profiles[i]
		p.tables = buildTables(p)
		registerProfile(p)
	}
}

// buildTables derives the code tables and detector weights of a profile.
func buildTables(p *LanguageProfile) *langTables {
	t := &langTables{
		decode:   make(map[string]rune, 80),
		encode:   make(map[rune]string, len(p.Letters)+len(p.Variants)),
		native:   make(map[rune]bool, 48),
		variants: p.Variants,
		freq:     parseFreq(p.Freq),
		ngrams:   make(map[string]float64, 64),
		words:    make(map[string]float64, 64),
	}
	t.addCodes(p)
	t.addStatistics(p)
	return t
}

// addCodes fills the code tables: the script's letters, digits and
// punctuation, then the profile's own letters and variants.
func (t *langTables) addCodes(p *LanguageProfile) {
	base := scriptLetters(p.Script)
	for _, m := range []map[rune]string{base, digits, punctuation} {
		for r, c := range m {
			t.decode[c] = r
		}
	}
	for r := range base {
		t.native[r] = true
	}
	for _, l := range p.Letters {
		if _, taken := t.decode[l.Code]; !taken { // shared code: the first letter wins
			t.decode[l.Code] = l.Char
		}
		t.encode[l.Char] = l.Code
		t.native[l.Char] = true
	}
	for v, to := range p.Variants {
		c, ok := t.encode[to]
		if !ok {
			c = base[to]
		}
		t.encode[v] = c
		t.native[v] = true
	}
}

// addStatistics weighs the profile's n-grams, words and signal words.
func (t *langTables) addStatistics(p *LanguageProfile) {
	tiers := strings.Split(p.NGrams, "|")
	if len(tiers) > len(tierWeights) {
		panic("morse: " + p.Name + ": too many n-gram tiers")
	}
	for tier, part := range tiers {
		for _, g := range strings.Fields(part) {
			t.ngrams[foldKey(g)] += tierWeights[tier]
		}
	}
	for _, w := range strings.Fields(p.Words) {
		t.words[foldKey(w)] = wordBonus * min(1, 0.5*float64(len([]rune(w))))
	}
	for _, w := range strings.Fields(p.Tokens) {
		t.words[foldKey(w)] = tokenWeight(w)
	}
}

// registerProfile adds a profile to the cross-language tables.
func registerProfile(p *LanguageProfile) {
	if scriptDefaults[p.Script] == Auto {
		scriptDefaults[p.Script] = p.Lang
	}
	t := p.tables
	for c := range t.decode {
		codeOwners[c]++
	}
	for r, c := range t.encode {
		if _, ok := encodeTable[r]; !ok { // the first profile wins (Ukrainian И)
			encodeTable[r] = encodeEntry{c, canonical(p, r)}
		}
		letterOwners[r]++
	}
}

// parseFreq turns "E12.7 T9.1" into log-likelihood ratios against a uniform
// letter distribution: common letters score above zero, rare ones below.
func parseFreq(s string) map[rune]float64 {
	m := make(map[rune]float64, 40)
	for _, f := range strings.Fields(s) {
		r, size := utf8.DecodeRuneInString(f)
		pct, err := strconv.ParseFloat(f[size:], 64)
		if err != nil {
			panic("morse: bad letter frequency " + quote(f))
		}
		m[matchFold(r)] += pct
	}
	for r, pct := range m {
		m[r] = math.Log(pct / 100 / uniformLetter)
	}
	return m
}

// tokenWeight is tokenBonus for signal words with letters. Numbers (73, 599)
// read the same in every alphabet, so they only break ties.
func tokenWeight(w string) float64 {
	if strings.Trim(w, "0123456789") == "" {
		return 1
	}
	return tokenBonus
}

// foldKey applies matchFold to a table entry.
func foldKey(s string) string { return strings.Map(matchFold, s) }

// canonical is the letter a variant is sent as (Ґ → Г), or r itself.
func canonical(p *LanguageProfile, r rune) rune {
	if to, ok := p.Variants[r]; ok {
		return to
	}
	return r
}

// Profiles returns a copy of the registered language profiles, in Language
// order.
func Profiles() []LanguageProfile {
	out := make([]LanguageProfile, len(profiles))
	for i, p := range profiles {
		p.Aliases = append([]string(nil), p.Aliases...)
		p.Letters = append([]Letter(nil), p.Letters...)
		v := make(map[rune]rune, len(p.Variants))
		for k, to := range p.Variants {
			v[k] = to
		}
		p.Variants = v
		out[i] = p
	}
	return out
}

// profileOf returns the profile of a concrete language, or nil for Auto and
// out-of-range values.
func profileOf(l Language) *LanguageProfile {
	if l <= Auto || l >= numLanguages {
		return nil
	}
	return &profiles[l-1]
}

func profileOrEnglish(l Language) *LanguageProfile {
	if p := profileOf(l); p != nil {
		return p
	}
	return &profiles[English-1]
}
