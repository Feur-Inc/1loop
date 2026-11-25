# Loopy: how it works

[USAGE.md](USAGE.md#loopy) says what Loopy does. This page says how it is
made, what its memory was measured to do, and what it does not do well.

## Parts

| Where | What |
| --- | --- |
| `internal/loopy` | What Loopy is made of (messages, cards, tasks, routines) and how a view reaches it (`Client`). No logic. |
| `internal/loopy/engine` | Loopy at work: its runs, tools, cards, guard, vault, routines, mail and calendar, and its HTTP API. |
| `internal/loopy/memory` | Its memory. |
| `internal/loopy/box` | Its WebAssembly computer. |
| `internal/ui/loopy_*.go`, `android/.../Loopy*.kt` | Views: they show the engine and answer its cards. |
| `internal/server/loopy.go`, `internal/ui/loopy_host.go` | The places it lives: what the engine needs of a host (models, browser, projects, notifications). |

The engine runs in the desktop app, in a headless server, or in the phone's
own engine, which is that same server. Views elsewhere use `/api/loopy/*`
with their pairing, and the `loopy` and `loopy_notice` events. Each place has
a Loopy of its own: nothing is synchronised or moved between them. On the
phone, the engine stays awake while a routine is on or a coding task runs.

Everything is in one folder, `loopy` beside the app's or the server's state:

| File | What |
| --- | --- |
| `loopy.json` | Conversation, tasks, routines, rules, settings. Written whole or not at all; one that cannot be read is put aside, never overwritten. |
| `memory/records.jsonl` | Every exchange as it was said, dated. Appended; rewritten only to erase. |
| `memory/facts.json` | Facts, habits, what was forgotten. |
| `home/` | Loopy's folder: `notes`, `inbox`, `work`. |
| `vault`, `vault.key` | Sign-ins and account passwords, encrypted. |

Loopy works with any model 1loop runs with its own tools (the providers' APIs,
ChatGPT accounts, OpenAI-compatible servers) and with Claude Code, whose own
tools, MCP servers, hooks and project files are off for it. Antigravity and
ChatGPT (Web) cannot be Loopy's model: they run only their own tools.

## Memory

### What it does

1. **Keep what was said, not a summary.** A round (a message and its answer)
   is the unit. LongMemEval ([2410.10813](https://arxiv.org/abs/2410.10813))
   found rounds better than sessions, and that replacing them with summaries
   or facts loses answers; Mnemon
   ([2609.36059](https://arxiv.org/abs/2609.36059)) keeps raw records for the
   same reason.
2. **Draw facts beside them.** From time to time (8 exchanges, or 10 minutes
   of quiet) a model reads what the user wrote and proposes facts: rules,
   ways of answering, preferences, habits, people, places, work, goals. Code
   checks each one: it must quote the user's own words, found in their
   message once pasted pages, quotes and code are taken out. A page or a mail
   cannot write in the memory.
3. **A fact that changes is ended, not overwritten.** "Lives in Lyon, until
   20 November" stays in the history and no longer guides anything.
4. **Said is not inferred.** What the user's words say outright guides Loopy
   at once; what was inferred guides it once seen twice. What is inferred
   never undoes what was said.
5. **A short profile is always in the prompt**: the rules (40 at most), how
   to write to them (25), and what they said of themselves (40).
6. **The rest comes only when it bears on the message.** Memory brought up
   out of place makes answers worse (OP-Bench
   [2601.13722](https://arxiv.org/abs/2601.13722): 26 to 61 % worse with
   memory systems; also MemSyco [2607.01071](https://arxiv.org/abs/2607.01071)
   and PersistBench [2602.01146](https://arxiv.org/abs/2602.01146)).
7. **How the user writes is counted, without a model**: language, *tu* or
   *vous*, length, capitals, abbreviations, what they asked of the answers
   more than once. Texts they edited before sending are kept as examples:
   PROSE ([2505.23815](https://arxiv.org/abs/2505.23815)), which builds on
   CIPHER ([2404.15269](https://arxiv.org/abs/2404.15269)), does best with a
   description and examples together.
8. **Forgotten is forgotten**: the fact leaves the file and is not noticed
   again; the exchange it comes from can be erased too.

Search joins two orders, by words (BM25, French and English endings) and by
meaning, with reciprocal rank fusion (k = 10). The meaning of a text is the
mean of static word vectors: `potion-multilingual-128M` reduced to its 80,000
commonest Latin tokens, 128 dimensions, int8 (11.6 MB, embedded; built by
`internal/loopy/memory/model/build_model.py`). No model to download or run.
The search is exact, over all exchanges.

What was left out, for lack of a measured gain here: a graph, a second model
to rank results, vectors from a neural model, scores that decay with time.

### Retrieval, without a model

LongMemEval-S (cleaned), the 467 questions that have evidence, about 229
exchanges each. The share of the evidence among the first exchanges found:

| | R@5 | R@10 | R@20 | all of it @20 |
| --- | --- | --- | --- | --- |
| Words only (BM25) | .734 | .832 | .907 | .850 |
| Meaning only | .713 | .828 | .900 | .833 |
| **Both, as shipped (11.6 MB)** | **.777** | **.889** | **.941** | **.893** |
| Both, with the whole 512 MB vectors | .805 | .888 | .947 | .901 |

The first two rows read 1,500 characters of each answer and the third 4,000;
with 1,500 the shipped one gives .779 / .880 / .940 / .891.

R@10 by kind of question: what the user said .969, knowledge updates .961,
what the assistant said .943, temporal .861, several sessions .845,
preferences .744. On French (Syntec, 100 questions, hit@5): .78 against .79
with the whole vectors. On a laptop, 0.4 to 0.7 ms to index an exchange and
0.3 to 0.4 ms to search.

### Answers, with a model

Claude Haiku answers from what each way brings back, and grades. LongMemEval-S,
the first 15 questions of each of the 7 kinds (105):

| | Right | Memory in the prompt |
| --- | --- | --- |
| No memory | 15 (14 %: the questions without an answer) | 0 |
| Words only | 84 (80 %) | 3,700 tokens |
| **One search, as Loopy does before answering** | **90 (86 %)** | 3,800 tokens |
| Several searches | 88 (84 %) | 4,500 tokens |

By kind, one search: knowledge updates 15/15, what the assistant said 15/15,
no answer 15/15, what the user said 14/15, temporal 13/15, preferences 10/15,
several sessions 8/15.

### Habits

Four invented people speak for a few days (a terse French speaker, a formal
one, a casual English speaker, a precise one). Then each is asked for things
that must come out their way ("no meat", "meetings only on Tuesdays and
Thursdays", "always *vous*", "a table, price first"), and for things where
nothing personal has a place. Three rounds, Haiku answering and grading:

| | Their way | Nothing personal out of place |
| --- | --- | --- |
| No memory | 18/51 | 12/12 |
| **Loopy's memory** | **41/51** | 11/12 (the miss is a wrong translation, not a leak) |
| The whole history in the prompt | 46/51 | 12/12 |

The whole history does better while it is a few messages long; it does not
fit once there are months of it. Three of the ten misses come from the bench:
Claude Code puts the account holder's name in its context, so the model signs
with it.

A first version chose the facts by how close they are to the message: 6/17
(one round), against 13/17 for the profile that is always there. "An idea for
dessert?" is not close to "allergic to nuts".

### How far to trust these numbers

- One model, small, is both reader and grader; no human checked the grades
  as a whole. Earlier runs of the two tables above came out lower (73 to 79 %
  for answers) because the grader refused right answers: it did not know the
  assistant has a memory, or its verdict was misread. Both were found by
  reading the misses, and every miss of the last run was read.
- 105 questions: a few points between two rows are noise. "One search" and
  "several searches" are equal here; 86 against 80 % is a lead, not a proof.
- The settings (k, passage length, answer length, thresholds) were chosen on
  LongMemEval itself. There is no held-out set.
- The people and wishes of the habits test were written for it.
- Published scores on LongMemEval or LoCoMo use other readers, graders and
  measures and are not comparable
  ([why](https://agentsystems.blog/agent-memory-benchmark)); LoCoMo is also
  [contested](https://blog.getzep.com/lies-damn-lies-statistics-is-mem0-really-sota-in-agent-memory/).
- Known to be weak: counting or adding up over many conversations, and
  preferences the question does not hint at.

### Measuring again

```sh
# Retrieval (no model): longmemeval_s_cleaned.json from
# huggingface.co/datasets/xiaowu0162/longmemeval-cleaned
LOOPY_LONGMEMEVAL=longmemeval_s_cleaned.json \
  go test -run LongMemEvalRetrieval -v ./internal/loopy/memory

# With a model: LOOPY_ASK is a command that reads a prompt on its input and
# prints the answer. LOOPY_EACH: questions of each kind, or rounds.
LOOPY_ASK=./ask.sh LOOPY_EACH=15 LOOPY_LONGMEMEVAL=... \
  go test -timeout 40m -run LongMemEvalAnswers -v ./internal/loopy/memory
LOOPY_ASK=./ask.sh LOOPY_EACH=3 \
  go test -timeout 25m -run 'HabitsAreFollowed|LearningIsRight' -v ./internal/loopy/memory

# The whole engine with a Claude Code model.
LOOPY_REAL=haiku go test -run RealModel -v ./internal/loopy/engine
```

The tables above cost about 1 dollar of Haiku, as Claude Code counts it.

## The box

`loopy_run` runs JavaScript in QuickJS-ng (v0.18.0, its released
`qjs-wasi.wasm`, 1.6 MB) on wazero, both inside 1loop. A script sees Loopy's
folder as `/` and nothing else: no network, no process, no environment, no
other file. Links are refused, whoever made them. 256 MB of memory, 30 s by
default and 5 minutes at most, 64 KB of output. Each run is a new instance.

Starting takes about half a second once (compiled code is cached in the
`box` folder), then 5 ms a script. Where code cannot be compiled at run time
it is interpreted, 10 to 20 times slower.

## What protects the user

Decided by code, whatever the model was made to say:

- **Before a press in the browser**, the button's words are read in the page:
  sending, buying, deleting, publishing, agreeing and security settings wait
  for a card. "Always allow" is by site, and never for buying, deleting or
  security. Enter and Ctrl+Enter count as presses.
- **Scripts in a page only read**: clicks, requests, navigation, storage and
  cookies are refused.
- **What others wrote** (pages, mails, files sent, webhooks, GitHub, the
  coding agent's answer) is fenced and marked as information.
- **What never leaves**: the vault's values, card numbers (Luhn), IBANs,
  keys, ID numbers and "password: …" are refused in what Loopy types,
  navigates to or writes in a script, and masked in drafts.
- **Sign-ins** go from 1loop's form or the vault into the page's password
  field, on https, on the site the card named. Loopy never gets the value;
  scripts are refused while it is in the page. The vault is AES-256-GCM; its
  key is protected by Windows (DPAPI) there, and is a file of the user's
  alone elsewhere, as 1loop's other keys are.
- **No shell, no file of the user's, no screen**: its tools are its own, the
  browser's, and nothing else.

Limits: the words of a button are read in French and English, so a button
that says nothing, or in another language, is not caught; what is fenced can
still mislead a model, which is why the rest does not depend on it. Memory
and the conversation are not encrypted on disk.

## Not done

- Voice on the desktop (the phone has it, through Android's recognition).
- Moving a Loopy from one place to another.
- Services other than mail (IMAP, SMTP) and calendars (CalDAV): through the
  browser only. Gmail and Outlook accounts need an app password.
- The vault's other kinds of entries in the settings, and password managers.
- The engine on a real arm64 phone is not tested: its tests pass on Android
  x86-64, and on arm64 emulated by QEMU but for the box's memory-limit test,
  which that emulation is too slow for.
- Size: the server and the phone's engine grow by 18 MB (11.6 of vectors, 1.6
  of QuickJS, the rest code).
