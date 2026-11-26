// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// With a real model, run by hand: LOOPY_REAL names a Claude Code model
// ("haiku"), whose account it uses. Nothing runs without it.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/loopy"
)

func TestRealModel(t *testing.T) {
	model := os.Getenv("LOOPY_REAL")
	if model == "" {
		t.Skip("LOOPY_REAL is not set")
	}
	dir := t.TempDir()
	host := &fakeHost{cfg: agent.Config{Provider: "claudecode", Model: model, ContextKey: "loopy"}, backend: &agent.App{Approvals: map[string]chan agent.ApprovalAnswer{}}}
	e, err := Open(context.Background(), dir, host)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	writeFile(t, filepath.Join(e.folder.dir, "notes", "depenses.csv"), "mois;montant\njanvier;1 204,50 €\nfévrier;987,25 €\nmars;1 100,00 €\n")
	ask := func(text string) string {
		t.Helper()
		before := len(said(e))
		if _, err := e.Send(text, nil); err != nil {
			t.Fatal(err)
		}
		end := time.Now().Add(2 * time.Minute)
		for time.Sleep(200 * time.Millisecond); !e.idle(); time.Sleep(200 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("%q: no answer (%+v)", text, e.View().Label)
			}
		}
		answer := strings.Join(said(e)[before:], " / ")
		t.Logf("> %s\n  %s", text, answer)
		return answer
	}
	last := func(kind string) *loopy.Card {
		for _, m := range slices.Backward(e.View().Messages) {
			if m.Card != nil && m.Card.Kind == kind {
				return m.Card
			}
		}
		return nil
	}
	ask("salut")
	ask("pour mes trajets je prend toujours le train, jamais l'avion, retient le. et répond moi en 2 phrases max")
	facts := ""
	for _, f := range e.memory.Facts(false) {
		facts += f.Kind + ": " + f.Text + "\n"
	}
	t.Log(facts)
	if !strings.Contains(strings.ToLower(facts), "train") || !strings.Contains(facts, "2") && !strings.Contains(facts, "deux") {
		t.Error("it did not remember")
	}
	if got := ask("combien j'ai depensé au total dans notes/depenses.csv ?"); !strings.Contains(strings.ReplaceAll(strings.ReplaceAll(got, " ", ""), " ", ""), "3291,75") {
		t.Error("the sum is wrong")
	}
	ask("ecris un mail a paul@example.com pour lui dire que jeudi 18h c'est bon")
	if mail := last("mail"); mail == nil {
		t.Error("no mail card")
	} else {
		t.Logf("mail: %+v\n%s", mail.Fields, mail.Body)
	}
	ask("rappelle moi tous les lundis a 9h de sortir les poubelles")
	if routine := last("routine"); routine == nil {
		t.Error("no routine card")
	} else {
		t.Logf("routine: %+v", routine.Fields)
	}
	ask("dans le projet app, le bouton Valider ne marche plus, fais corriger ça")
	if task := last("delegate"); task == nil {
		t.Error("no task card")
	} else {
		t.Logf("task: %s\n%s", task.Title, task.Body)
	}
	e.Clear() // a fresh conversation: what it knows stays
	if got := strings.ToLower(ask("faut que j'aille a Marseille vendredi, tu me conseilles quoi comme transport ?")); !strings.Contains(got, "train") && !strings.Contains(got, "tgv") {
		t.Error("it forgot the train")
	}
	ask("merci")
}
