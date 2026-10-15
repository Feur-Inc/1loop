//go:build !server

package main

// Formulas. A model writes mathematics as LaTeX ($…$, \(…\), $$…$$, \[…\]).
// Nothing short of TeX sets it properly, so 1loop uses the TeX system of
// the computer when there is one: latex composes the formula, dvipng turns
// it into a picture with a transparent background, in the text's colour,
// and tells how far it hangs below the baseline so it sits on the line.
//
// It is done beside the interface (a formula takes a few tenths of a
// second the first time) and kept: in memory for the session, on disk in
// ~/.cache/1loop/math for the next ones. Until a picture is ready, and for
// good where no TeX is installed or a formula does not compile, the source
// is shown as code.
//
// The formula comes from the model, so TeX runs with its shell escape off
// and its file access restricted to its own working directory.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
)

// mathPicture is a typeset formula, sized in the interface's units.
type mathPicture struct {
	image  image.Image
	width  float32
	height float32
	depth  float32 // how much of the height is below the baseline
}

// afterTypeset is called on the interface thread once the blocks waiting
// for a formula have been laid out again.
var afterTypeset func()

// mathOversample is how many pixels are rendered per unit: enough for a
// high density screen, scaled down smoothly on the others.
const mathOversample = 3

var formulas struct {
	sync.Mutex
	known   map[string]*mathPicture // nil for a formula that cannot be typeset
	pending map[string]bool
	waiters []func()
	tools   int // 0 not looked for yet, 1 found, -1 missing
	queue   chan mathJob
}

type mathJob struct {
	key, source string
	display     bool
	size        float32
}

func mathTools() bool {
	if formulas.tools == 0 {
		formulas.tools = -1
		_, errLatex := exec.LookPath("latex")
		_, errPNG := exec.LookPath("dvipng")
		if errLatex == nil && errPNG == nil {
			formulas.tools = 1
		}
		devf("app", "formulas: TeX system found %v", formulas.tools == 1)
	}
	return formulas.tools == 1
}

// typeset returns the picture of a formula when it is ready. Otherwise it
// starts composing it when it can; pending then says a picture will come,
// and whenTypeset tells when.
func typeset(source string, display bool, size float32) (picture *mathPicture, pending bool) {
	formulas.Lock()
	defer formulas.Unlock()
	if formulas.known == nil {
		formulas.known, formulas.pending = map[string]*mathPicture{}, map[string]bool{}
		formulas.queue = make(chan mathJob, 256)
		go mathWorker()
	}
	if !mathTools() || len(source) > 4000 {
		return nil, false
	}
	key := fmt.Sprintf("%v|%.1f|%s", display, size, source)
	if picture, done := formulas.known[key]; done {
		return picture, false
	}
	if !formulas.pending[key] {
		select {
		case formulas.queue <- mathJob{key, source, display, size}:
			formulas.pending[key] = true
		default:
			return nil, false // too many at once: the source stays
		}
	}
	return nil, true
}

// whenTypeset calls f on the interface thread once, the next time a
// formula has been composed.
func whenTypeset(f func()) {
	formulas.Lock()
	formulas.waiters = append(formulas.waiters, f)
	formulas.Unlock()
}

func mathWorker() {
	for job := range formulas.queue {
		started := time.Now()
		picture, err := composeFormula(job.source, job.display, job.size)
		if err != nil {
			devf("app", "formula not typeset (%v): %s", err, devText(job.source, 80))
		} else {
			devf("perf", "formula typeset in %s: %s", time.Since(started).Round(time.Millisecond), devText(job.source, 60))
		}
		formulas.Lock()
		formulas.known[job.key] = picture
		delete(formulas.pending, job.key)
		waiters := formulas.waiters
		formulas.waiters = nil
		formulas.Unlock()
		if len(waiters) > 0 {
			fyne.Do(func() {
				for _, waiter := range waiters {
					waiter()
				}
				if afterTypeset != nil {
					afterTypeset() // the blocks changed height: the conversation follows
				}
			})
		}
	}
}

var (
	dvipngDepth = regexp.MustCompile(`depth=(-?\d+)`)
	// What a formula has no use for and could misuse.
	texForbidden = regexp.MustCompile(`\\(input|include|openin|openout|read|write|immediate|csname|catcode|def|edef|gdef|xdef|let|newcommand|renewcommand|usepackage|documentclass|special|verbatiminput|lstinputlisting)\b`)
	texBlocks    = regexp.MustCompile(`\\begin\{(align|alignat|equation|gather|multline|flalign|eqnarray)\*?\}`)
)

// composeFormula runs latex and dvipng on one formula.
func composeFormula(source string, display bool, size float32) (*mathPicture, error) {
	if texForbidden.MatchString(source) {
		return nil, fmt.Errorf("a command not allowed in a formula")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("v2|%v|%.1f|%s", display, size, source)))
	name := hex.EncodeToString(sum[:12])
	cache := ""
	if dir, err := os.UserCacheDir(); err == nil {
		cache = filepath.Join(dir, "1loop", "math")
		if data, err := os.ReadFile(filepath.Join(cache, name+".png")); err == nil {
			if meta, err := os.ReadFile(filepath.Join(cache, name+".depth")); err == nil {
				if depth, err := strconv.Atoi(strings.TrimSpace(string(meta))); err == nil {
					return mathFromPNG(data, depth)
				}
			}
		}
	}
	// Environments that take the whole line become their inline twins, so
	// the picture is as wide as the formula, not as the page.
	if texBlocks.MatchString(source) {
		display = true
		for from, to := range map[string]string{"align": "aligned", "alignat": "alignedat", "flalign": "aligned", "eqnarray": "aligned",
			"gather": "gathered", "multline": "gathered", "equation": ""} {
			for _, star := range []string{"*", ""} {
				begin, end := `\begin{`+to+`}`, `\end{`+to+`}`
				if to == "" {
					begin, end = "", ""
				}
				source = strings.ReplaceAll(source, `\begin{`+from+star+`}`, begin)
				source = strings.ReplaceAll(source, `\end{`+from+star+`}`, end)
			}
		}
	}
	body := "$" + source + "$"
	if display {
		body = `$\displaystyle ` + source + "$"
	}
	// The preview package crops the page to the formula and keeps its
	// baseline, which dvipng then reports as a depth.
	document := `\documentclass[12pt]{article}
\usepackage{amsmath,amssymb,amsfonts}
\usepackage[active,tightpage]{preview}
\pagestyle{empty}
\begin{document}
\begin{preview}` + body + `\end{preview}
\end{document}
`
	work, err := os.MkdirTemp("", "1loop-math-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := os.WriteFile(filepath.Join(work, "f.tex"), []byte(document), 0600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	env := append(os.Environ(), "openin_any=p", "openout_any=p", "shell_escape=f")
	latex := exec.CommandContext(ctx, "latex", "-no-shell-escape", "-interaction=batchmode", "-halt-on-error", "f.tex")
	latex.Dir, latex.Env = work, env
	if err := latex.Run(); err != nil {
		return nil, fmt.Errorf("it does not compile")
	}
	// 12 pt text at this density is as tall as the interface's text.
	// (TeX's letters are smaller in their body than the interface's face.)
	density := int(size*mathOversample*72.27/12*1.18 + 0.5)
	ink := fmt.Sprintf("rgb %.3f %.3f %.3f", float32(colText.R)/255, float32(colText.G)/255, float32(colText.B)/255)
	var report bytes.Buffer
	dvipng := exec.CommandContext(ctx, "dvipng", "-T", "tight", "-D", strconv.Itoa(density), "-bg", "Transparent", "-fg", ink,
		"--depth", "-o", "f.png", "f.dvi")
	dvipng.Dir, dvipng.Env, dvipng.Stdout = work, env, &report
	if err := dvipng.Run(); err != nil {
		return nil, fmt.Errorf("no picture: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(work, "f.png"))
	if err != nil {
		return nil, err
	}
	depth := 0
	if found := dvipngDepth.FindStringSubmatch(report.String()); found != nil {
		depth, _ = strconv.Atoi(found[1])
	}
	if cache != "" && os.MkdirAll(cache, 0700) == nil {
		os.WriteFile(filepath.Join(cache, name+".png"), data, 0600)
		os.WriteFile(filepath.Join(cache, name+".depth"), []byte(strconv.Itoa(depth)), 0600)
	}
	return mathFromPNG(data, depth)
}

func mathFromPNG(data []byte, depth int) (*mathPicture, error) {
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := picture.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return nil, fmt.Errorf("an empty picture")
	}
	return &mathPicture{image: picture, width: float32(bounds.Dx()) / mathOversample, height: float32(bounds.Dy()) / mathOversample,
		depth: float32(depth) / mathOversample}, nil
}
