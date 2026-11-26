// SPDX-License-Identifier: MPL-2.0

//go:build !js

package memory

// Search by meaning: a text's vector is the mean of its words' (static
// vectors, model/build_model.py), so it needs no model at run time, no
// download and no accelerator, and takes microseconds.

import (
	_ "embed"
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"unicode"
	"unsafe"

	"golang.org/x/text/unicode/norm"
)

//go:embed model/vectors.bin
var vectorData string

type vectorModel struct {
	dim     int
	ids     map[string]int32
	scores  []float32
	scale   []float32
	data    string // int8, a row a token
	worst   float32
	longest int
}

var model = sync.OnceValue(func() *vectorModel {
	raw := vectorData
	if len(raw) < 10 || raw[:4] != "LPV1" {
		return nil
	}
	bytes := unsafe.Slice(unsafe.StringData(raw), len(raw)) // read only
	n, dim := int(binary.LittleEndian.Uint32(bytes[4:])), int(binary.LittleEndian.Uint16(bytes[8:]))
	m := &vectorModel{dim: dim, ids: make(map[string]int32, n), scores: make([]float32, n), scale: make([]float32, n)}
	at := 10
	for i := range n {
		size := int(bytes[at])
		m.scores[i] = math.Float32frombits(binary.LittleEndian.Uint32(bytes[at+1:]))
		name := raw[at+5 : at+5+size]
		m.ids[name] = int32(i)
		m.longest = max(m.longest, size)
		m.worst = min(m.worst, m.scores[i])
		at += 5 + size
	}
	for i := range n {
		m.scale[i] = math.Float32frombits(binary.LittleEndian.Uint32(bytes[at:])) / 127
		at += 4
	}
	if len(raw) < at+n*dim {
		return nil
	}
	m.data = raw[at : at+n*dim]
	return m
})

// Vector is a text's meaning, of length 1 in int8 (a product of two is
// their closeness times 127²); nil when no word of it is known.
type Vector []int8

// normalize brings a text to the tokens' form: compatibility characters
// plain, ASCII punctuation on its own, spaces as ▁.
func normalize(text string) string {
	var b strings.Builder
	b.Grow(len(text) + 16)
	space := true
	b.WriteString("▁")
	for _, r := range norm.NFKC.String(text) {
		punct := r < 128 && unicode.In(r, unicode.Punct, unicode.Symbol)
		if unicode.IsSpace(r) || punct {
			if !space {
				b.WriteString("▁")
				space = true
			}
			if !punct {
				continue
			}
		}
		b.WriteRune(r)
		space = false
		if punct {
			b.WriteString("▁")
			space = true
		}
	}
	return strings.TrimSuffix(b.String(), "▁")
}

// tokens cuts a word group into the likeliest tokens (unigram, Viterbi).
func (m *vectorModel) tokens(text string, each func(id int32)) {
	for text != "" { // a word at a time: no token spans two
		next := strings.Index(text[len("▁"):], "▁")
		word := text
		if next >= 0 {
			word, text = text[:next+len("▁")], text[next+len("▁"):]
		} else {
			text = ""
		}
		m.cut(word, each)
	}
}

func (m *vectorModel) cut(word string, each func(id int32)) {
	if len(word) > 400 {
		word = word[:400]
	}
	n := len(word)
	best := make([]float32, n+1)
	from := make([]int, n+1)
	ids := make([]int32, n+1)
	for i := 1; i <= n; i++ {
		best[i] = float32(math.Inf(-1))
	}
	for i := 0; i < n; i++ {
		if math.IsInf(float64(best[i]), -1) {
			continue
		}
		for j := i + 1; j <= n && j-i <= m.longest; j++ {
			if id, ok := m.ids[word[i:j]]; ok && best[i]+m.scores[id] > best[j] {
				best[j], from[j], ids[j] = best[i]+m.scores[id], i, id
			}
		}
		size := 1
		for i+size < n && word[i+size]&0xC0 == 0x80 {
			size++
		}
		if unknown := best[i] + m.worst - 10; unknown > best[i+size] {
			best[i+size], from[i+size], ids[i+size] = unknown, i, -1
		}
	}
	for at := n; at > 0; at = from[at] {
		if ids[at] >= 0 {
			each(ids[at])
		}
	}
}

// Embed is the meaning of a text.
func Embed(text string) Vector {
	m := model()
	if m == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	sum := make([]float32, m.dim)
	count := 0
	m.tokens(normalize(text), func(id int32) {
		count++
		row := m.data[int(id)*m.dim : (int(id)+1)*m.dim]
		scale := m.scale[id]
		for i := range sum {
			sum[i] += float32(int8(row[i])) * scale
		}
	})
	if count == 0 {
		return nil
	}
	length := float32(0)
	for _, v := range sum {
		length += v * v
	}
	if length == 0 {
		return nil
	}
	length = float32(math.Sqrt(float64(length)))
	out := make(Vector, m.dim)
	for i, v := range sum {
		out[i] = int8(math.Round(float64(v / length * 127)))
	}
	return out
}

// Close is how close two meanings are: about 1 the same, 0 unrelated.
func (v Vector) Close(other Vector) float32 {
	if len(v) == 0 || len(v) != len(other) {
		return 0
	}
	sum := int32(0)
	for i, a := range v {
		sum += int32(a) * int32(other[i])
	}
	return float32(sum) / (127 * 127)
}
