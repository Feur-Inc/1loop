// SPDX-License-Identifier: MPL-2.0

//go:build !js

package memory

// Retrieval on LongMemEval (arXiv 2410.10813): LOOPY_LONGMEMEVAL is the path
// of longmemeval_s_cleaned.json. How much of the evidence of each question
// is among the exchanges found, over the whole set.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

type longMemEval struct {
	ID       string   `json:"question_id"`
	Type     string   `json:"question_type"`
	Question string   `json:"question"`
	Answer   any      `json:"answer"`
	Date     string   `json:"question_date"`
	Dates    []string `json:"haystack_dates"`
	Sessions [][]struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		HasAnswer bool   `json:"has_answer"`
	} `json:"haystack_sessions"`
}

func TestLongMemEvalRetrieval(t *testing.T) {
	path := os.Getenv("LOOPY_LONGMEMEVAL")
	if path == "" {
		t.Skip("LOOPY_LONGMEMEVAL is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var questions []longMemEval
	if err := json.Unmarshal(data, &questions); err != nil {
		t.Fatal(err)
	}
	type tally struct{ r5, r10, r20, all20, n float64 }
	by := map[string]*tally{}
	var setting, searching time.Duration
	rounds := 0
	for _, q := range questions {
		if strings.HasSuffix(q.ID, "_abs") {
			continue
		}
		ix := newIndex()
		gold := map[string]bool{}
		at := time.Now()
		for s, session := range q.Sessions {
			for i, turn := range session {
				if turn.Role != "user" {
					continue
				}
				id := fmt.Sprintf("%d/%d", s, i)
				answer, has := "", turn.HasAnswer
				if i+1 < len(session) && session[i+1].Role == "assistant" {
					answer, has = session[i+1].Content, has || session[i+1].HasAnswer
				}
				ix.set(id, turn.Content, clip(answer, answerKey))
				rounds++
				if has {
					gold[id] = true
				}
			}
		}
		setting += time.Since(at)
		if len(gold) == 0 {
			continue
		}
		at = time.Now()
		hits := ix.search(q.Question, 20, nil)
		searching += time.Since(at)
		for _, name := range []string{"all", q.Type} {
			if by[name] == nil {
				by[name] = &tally{}
			}
			y, got := by[name], 0.0
			y.n++
			for i, hit := range hits {
				if gold[hit.id] {
					got++
				}
				switch i {
				case 4:
					y.r5 += got / float64(len(gold))
				case 9:
					y.r10 += got / float64(len(gold))
				}
			}
			y.r20 += got / float64(len(gold))
			if int(got) == len(gold) {
				y.all20++
			}
		}
	}
	var names []string
	for name := range by {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		y := by[name]
		t.Logf("%-26s n=%3.0f  R@5 %.3f  R@10 %.3f  R@20 %.3f  all the evidence @20 %.3f", name, y.n, y.r5/y.n, y.r10/y.n, y.r20/y.n, y.all20/y.n)
	}
	t.Logf("%d exchanges: %s to index one, %s a search over about %d", rounds, setting/time.Duration(rounds), searching/time.Duration(by["all"].n), rounds/len(questions))
	if y := by["all"]; y.r20/y.n < .93 {
		t.Errorf("R@20 %.3f: the search got worse", y.r20/y.n)
	}
}
