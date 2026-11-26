// SPDX-License-Identifier: MPL-2.0

package memory

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// The words search compares: lower case, without accents, common words and
// endings, in French and English (what they share with other Latin
// languages works there too); a run of Chinese or Japanese is cut in pairs.

var common = func() map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.Fields(`the a an of to and in is it i you for on that my with was this have do be are at as me what how can did from about your so but or if not we he she they them his her our would could any some just am been has had will there which when where who
le la les un une des du de d l et en est il elle je tu on nous vous ils elles que qui quoi dans pour sur au aux avec ce cet cette ces mon ma mes ton ta tes son sa ses notre nos votre vos leur leurs ne pas plus se s n y a ai as ont suis es sont etait ete etre avoir fait faire comme mais ou donc or ni car si j m t c qu moi toi lui me te quel quelle quels quelles ca cela par est-ce`) {
		out[word] = true
	}
	return out
}()

// plain is s in lower case, without accents.
func plain(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r == 'œ' || r == 'Œ':
			b.WriteString("oe")
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func cjk(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// words are the searched words of a text, in order.
func words(text string) []string {
	var out []string
	var word []rune
	end := func() {
		if len(word) == 0 {
			return
		}
		if cjk(word[0]) {
			if len(word) == 1 {
				out = append(out, string(word))
			}
			for i := 0; i+1 < len(word); i++ {
				out = append(out, string(word[i:i+2]))
			}
		} else if w := string(word); !common[w] {
			out = append(out, stem(w))
		}
		word = word[:0]
	}
	for _, r := range plain(text) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			end()
			continue
		}
		if len(word) > 0 && cjk(r) != cjk(word[0]) {
			end()
		}
		word = append(word, r)
	}
	end()
	return out
}

// stem drops the endings that only say a plural, a tense or a gender.
func stem(word string) string {
	for _, ending := range [...]string{"ements", "ement", "ations", "ation", "ings", "ing", "ees", "ed", "es", "er", "ez", "ee", "s", "x", "e"} {
		if len(word) >= len(ending)+3 && strings.HasSuffix(word, ending) {
			return word[:len(word)-len(ending)]
		}
	}
	return word
}

// clip is text cut to n runes.
func clip(text string, n int) string {
	if len(text) <= n {
		return text
	}
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n]) + "…"
}
