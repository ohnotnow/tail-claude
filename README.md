# Top Cat (`tc`)

A side-terminal **watcher** for live Claude Code sessions. It tails the current
session log, renders Claude's surfaced output nicely, and flags the moments
where a session is sliding into confident, no-brakes momentum — so you can step
in (or hand it to a fresh-eyes reviewer) before something slips through.

```
🐱 Top Cat                                                          ● live
⚠ brake-drought: 8 turns — glance / fresh eyes?   a nudge to glance, never a verdict
17:35 ▸ Right, let's build the…  │  ## What I've done
17:36 · I'll start with the read… │
17:38 ▸ sounds good, go for it    │  Wired up the parser and it all works — the good
17:41 · Done — the core works,…   │  news is the tests are green and nothing's loose 🎉
…                                 │  …
↑/↓ navigate · tab → reading pane · a follow · q quit
```

## Why this exists

While Claude works, you're often away — on a call, in another window — for half
an hour at a time. You come back **cold** and have to (a) catch whether the
session has drifted into smooth, unchallenged momentum, and (b) reconstruct what
happened, tying a change to the words around it. Claude Code's own TUI shows
only the first few lines of each response, so things slip past. `tc` is the
always-open side-terminal that surfaces the whole timeline and keeps a running
read on the momentum.

## What it watches (and what it can't)

`tc` renders the **surfaced assistant text** and your prompts as a timeline.

**File edits show up too.** `Edit` and `Write` calls appear as their own entries
(`✎ session.go`, `＋ README.md`) right where they happened — selecting one shows
the old→new change (red/green) in the reading pane. So "let me fix that" is
followed by the edit itself, not a silent gap. Read/Bash/search calls are
*deliberately* left out: they're already narrated by the surrounding words, so
showing them would just be noise.

It does **not** show Claude's *thinking* — those blocks are stored on disk with
an encrypted signature only, their text empty (verified across thousands of
blocks). The plaintext reasoning is never written out, so no tool can surface
it. The momentum tell therefore has to be read from the *output*, where it
survives in smoothed form.

## Install

```bash
go build -o tc .      # then put tc on your PATH
# or
go install .
```

Single self-contained binary, no network access, local files only.

## Usage

Run it in (or pointed at) a project you're running Claude Code in:

```bash
tc                       # watch the current directory's newest session
tc --dir /path/to/proj   # watch a specific project
tc --print               # dump the timeline to stdout and exit (no TUI)
```

`tc` follows the newest session log for the project and **re-targets
automatically** if you start a fresh session.

### Keys

| Key            | Action                                              |
|----------------|-----------------------------------------------------|
| `↑`/`↓`, `j`/`k` | move the list (or scroll the reading pane when it's focused) |
| `→`/`l`        | focus the reading pane                              |
| `←`/`h`        | focus the message list                             |
| `tab`          | toggle focus between the two panes                 |
| `a`            | snap back to live-follow (however far back you are) |
| `q` / `ctrl+c` | quit                                                |

**Live-follow & scroll-lock:** while following, `tc` auto-selects each new
message as it lands. The instant you navigate back to read history, it stops
following and **never yanks the view to the bottom** when a new message
arrives — like `less +F`. Press `a` to rejoin the tail.

## The signals

### Brake-drought meter (primary)

The headline signal counts **assistant turns since the last "brake-word"** — a
moment of genuine self-correction or pausing to verify (`wait`, `let me
verify`, `hold on`, `i'm not sure`, `correction`, …). It goes red past a
threshold (default 6).

It tracks the **absence of brakes**, not the presence of alarming language,
because the dangerous session is *word-quiet*: it doesn't reach for dramatic
words, it just rolls smoothly. A signal that only fired on alarming words would
go dark exactly when you most need it — and a dark screen reads as a false
"all clear".

> **It's a nudge, not a verdict.** A red meter means *"worth a glance — maybe
> time for fresh eyes or a second opinion"*, **not** "something is wrong". Some
> smooth sessions are smooth because the task is genuinely simple and there's
> nothing to brake about. The meter can't tell those apart — you can. The
> caveat is kept on screen for exactly this reason.

### Doom-word highlighting (secondary)

Phrases like *"the good news is"*, *"should be fine"*, *"all green"* and a lone
🎉 are highlighted inline in the reading pane as **attention-directors**. They
are never a detector and their absence is never a green light — they just make
your eye land on the smug-momentum tells.

## Configuration (optional)

`tc` ships with the default word lists baked in, so it works the instant you run
it. To tune them, copy the example to `~/.config/tc/config.yaml`:

```bash
mkdir -p ~/.config/tc
cp config.yaml.example ~/.config/tc/config.yaml
```

Any list you set **replaces** the corresponding default (the example contains
the full defaults, so start there and edit). You can change `brake_words`,
`doom_words` and `drought_threshold`.

## A note on the defaults

The default word lists aren't guesses — they were checked against 127 real
sessions. The most useful finding: `actually` was **77% of all brake-word hits**
yet turned out to be a discourse marker that often appears *inside* confident
prose ("here's what I **actually** found"). It's deliberately left out of the
defaults; including it would make the meter lie.

---

Built in Go with [Bubble Tea](https://github.com/charmbracelet/bubbletea) +
[Glamour](https://github.com/charmbracelet/glamour).
