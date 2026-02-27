package main

import (
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const version = "0.2.0"

type token struct {
	style bool
	text  string
	width int
}

type state struct {
	hash         string
	pos          int
	delayCounter int
}

type opts struct {
	width      int
	id         string
	speed      int
	separator  string
	direction  string
	pad        bool
	scrollDelay int
	maxLength  int
	reset      bool
}

func main() {
	o := opts{
		width:     30,
		id:        "default",
		speed:     1,
		separator: " - ",
		direction: "left",
		pad:       true,
	}

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-w", "--width":
			i++
			o.width = mustInt(args[i])
		case "-i", "--id":
			i++
			o.id = args[i]
		case "-s", "--speed":
			i++
			o.speed = mustInt(args[i])
		case "--separator":
			i++
			o.separator = args[i]
		case "--direction":
			i++
			o.direction = args[i]
		case "--pad":
			o.pad = true
		case "--no-pad":
			o.pad = false
		case "--scroll-delay":
			i++
			o.scrollDelay = mustInt(args[i])
		case "--max-length":
			i++
			o.maxLength = mustInt(args[i])
		case "--reset":
			o.reset = true
		case "--help":
			printUsage()
			return
		case "--version":
			fmt.Printf("tmux-marquee %s\n", version)
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown option: %s\n", args[i])
			os.Exit(1)
		}
	}

	stateDir := stateDirectory()
	_ = os.MkdirAll(stateDir, 0o700)
	stateFile := filepath.Join(stateDir, o.id)

	// Stale cleanup ~1% of invocations
	if rand.Intn(100) == 0 {
		cleanStale(stateDir)
	}

	if o.reset {
		os.Remove(stateFile)
		return
	}

	raw, _ := io.ReadAll(os.Stdin)
	text := strings.TrimRight(string(raw), "\n")
	text = ansiToTmux(text)

	if o.maxLength > 0 {
		text = truncateRunes(text, o.maxLength)
	}

	if text == "" {
		os.Remove(stateFile)
		fmt.Println("")
		return
	}

	tokens := tokenize(text)
	textCols := textWidth(tokens)

	if textCols <= o.width {
		if o.pad {
			padCount := o.width - textCols
			fmt.Println(text + strings.Repeat(" ", padCount))
		} else {
			fmt.Println(text)
		}
		os.Remove(stateFile)
		return
	}

	hash := contentHash(tokens)
	st := readState(stateFile)
	if st.hash != hash {
		st = state{hash: hash}
	}

	// Scroll delay
	if o.scrollDelay > 0 && st.delayCounter < o.scrollDelay {
		st.delayCounter++
		writeState(stateFile, state{hash: hash, pos: 0, delayCounter: st.delayCounter})
		fmt.Println(sliceColumns(tokens, textCols, 0, o.width))
		return
	}

	scrollText := text + o.separator
	scrollTokens := tokenize(scrollText)
	scrollCols := textWidth(scrollTokens)
	if scrollCols == 0 {
		fmt.Println("")
		return
	}

	pos := st.pos % scrollCols
	var visible string
	var nextPos int

	switch o.direction {
	case "bounce":
		bounceRange := textCols - o.width
		if bounceRange <= 0 {
			bounceRange = 1
		}
		cycle := bounceRange * 2
		bouncePos := pos % cycle
		if bouncePos >= bounceRange {
			bouncePos = cycle - bouncePos
		}
		visible = sliceColumns(tokens, textCols, bouncePos, o.width)
		nextPos = pos + o.speed

	case "right":
		rpos := (scrollCols - pos%scrollCols) % scrollCols
		visible = sliceColumns(scrollTokens, scrollCols, rpos, o.width)
		nextPos = pos + o.speed

	default: // left
		visible = sliceColumns(scrollTokens, scrollCols, pos, o.width)
		nextPos = pos + o.speed
	}

	writeState(stateFile, state{hash: hash, pos: nextPos, delayCounter: st.delayCounter})
	fmt.Println(visible)
}

func tokenize(s string) []token {
	var tokens []token
	i := 0
	runes := []rune(s)
	n := len(runes)
	for i < n {
		if i+1 < n && runes[i] == '#' && runes[i+1] == '[' {
			end := -1
			for j := i + 2; j < n; j++ {
				if runes[j] == ']' {
					end = j
					break
				}
			}
			if end >= 0 {
				tag := string(runes[i : end+1])
				tokens = append(tokens, token{style: true, text: tag})
				i = end + 1
				continue
			}
		}
		r := runes[i]
		w := runewidth.RuneWidth(r)
		tokens = append(tokens, token{text: string(r), width: w})
		i++
	}
	return tokens
}

func textWidth(tokens []token) int {
	w := 0
	for _, t := range tokens {
		if !t.style {
			w += t.width
		}
	}
	return w
}

func contentHash(tokens []token) string {
	var sb strings.Builder
	for _, t := range tokens {
		if !t.style {
			sb.WriteString(t.text)
		}
	}
	h := crc32.ChecksumIEEE([]byte(sb.String()))
	return strconv.FormatUint(uint64(h), 10)
}

func sliceColumns(tokens []token, totalCols, offset, width int) string {
	if totalCols == 0 {
		return ""
	}
	offset = offset % totalCols

	// Build column positions for each token
	type positioned struct {
		tok token
		col int
	}
	var pts []positioned
	col := 0
	for _, t := range tokens {
		pts = append(pts, positioned{tok: t, col: col})
		if !t.style {
			col += t.width
		}
	}

	// Collect style preamble: all style tags at or before offset
	var out strings.Builder
	for _, p := range pts {
		if p.tok.style && p.col <= offset {
			out.WriteString(p.tok.text)
		} else if !p.tok.style && p.col >= offset {
			break
		}
	}

	// Find first char token at or after offset
	startIdx := 0
	for i, p := range pts {
		if !p.tok.style && p.col >= offset {
			startIdx = i
			break
		}
	}

	// Collect visible output with wrap-around
	filled := 0
	n := len(pts)
	idx := startIdx
	for laps := 0; filled < width && laps < 3; laps++ {
		for idx < n && filled < width {
			p := pts[idx]
			if p.tok.style {
				out.WriteString(p.tok.text)
				idx++
				continue
			}
			if filled+p.tok.width > width {
				out.WriteByte(' ')
				filled = width
				break
			}
			out.WriteString(p.tok.text)
			filled += p.tok.width
			idx++
		}
		idx = 0
	}
	return out.String()
}

func stateDirectory() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "tmux-marquee")
	}
	if d := os.Getenv("TMPDIR"); d != "" {
		return filepath.Join(d, "tmux-marquee")
	}
	return "/tmp/tmux-marquee"
}

func readState(path string) state {
	data, err := os.ReadFile(path)
	if err != nil {
		return state{}
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 {
		return state{}
	}
	pos, _ := strconv.Atoi(lines[1])
	dc, _ := strconv.Atoi(lines[2])
	return state{hash: lines[0], pos: pos, delayCounter: dc}
}

func writeState(path string, s state) {
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	content := fmt.Sprintf("%s\n%d\n%d\n", s.hash, s.pos, s.delayCounter)
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return
	}
	os.Rename(tmp, path)
}

func cleanStale(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-1 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func truncateRunes(s string, maxRunes int) string {
	count := utf8.RuneCountInString(s)
	if count <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

var ansiColors = [8]string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

// ansiToTmux converts ANSI SGR escape sequences to tmux #[...] style tags.
// Non-SGR escape sequences are stripped.
func ansiToTmux(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\x1b' && s[i+1] == '[' {
			// Find end of CSI sequence
			j := i + 2
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				params := s[i+2 : j]
				tag := sgrToTmux(params)
				if tag != "" {
					out.WriteString("#[")
					out.WriteString(tag)
					out.WriteByte(']')
				}
				i = j + 1
				continue
			}
			// Non-SGR CSI sequence — strip it
			if j < len(s) {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func sgrToTmux(params string) string {
	if params == "" || params == "0" {
		return "default"
	}
	codes := strings.Split(params, ";")
	var attrs []string
	for ci := 0; ci < len(codes); ci++ {
		n, err := strconv.Atoi(codes[ci])
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			attrs = append(attrs, "default")
		case n == 1:
			attrs = append(attrs, "bold")
		case n == 2:
			attrs = append(attrs, "dim")
		case n == 3:
			attrs = append(attrs, "italics")
		case n == 4:
			attrs = append(attrs, "underscore")
		case n == 7:
			attrs = append(attrs, "reverse")
		case n == 9:
			attrs = append(attrs, "strikethrough")
		case n == 22:
			attrs = append(attrs, "nobold", "nodim")
		case n == 23:
			attrs = append(attrs, "noitalics")
		case n == 24:
			attrs = append(attrs, "nounderscore")
		case n == 27:
			attrs = append(attrs, "noreverse")
		case n == 29:
			attrs = append(attrs, "nostrikethrough")
		case n >= 30 && n <= 37:
			attrs = append(attrs, "fg="+ansiColors[n-30])
		case n == 38:
			if a, skip := parseExtendedColor("fg", codes[ci+1:]); a != "" {
				attrs = append(attrs, a)
				ci += skip
			}
		case n == 39:
			attrs = append(attrs, "fg=default")
		case n >= 40 && n <= 47:
			attrs = append(attrs, "bg="+ansiColors[n-40])
		case n == 48:
			if a, skip := parseExtendedColor("bg", codes[ci+1:]); a != "" {
				attrs = append(attrs, a)
				ci += skip
			}
		case n == 49:
			attrs = append(attrs, "bg=default")
		case n >= 90 && n <= 97:
			attrs = append(attrs, "fg=bright"+ansiColors[n-90])
		case n >= 100 && n <= 107:
			attrs = append(attrs, "bg=bright"+ansiColors[n-100])
		}
	}
	return strings.Join(attrs, ",")
}

// parseExtendedColor handles 38;5;N (256-color) and 38;2;R;G;B (truecolor)
func parseExtendedColor(layer string, rest []string) (string, int) {
	if len(rest) < 1 {
		return "", 0
	}
	mode, _ := strconv.Atoi(rest[0])
	switch mode {
	case 5: // 256-color: 38;5;N
		if len(rest) < 2 {
			return "", 1
		}
		return layer + "=colour" + rest[1], 2
	case 2: // truecolor: 38;2;R;G;B
		if len(rest) < 4 {
			return "", 1
		}
		r, _ := strconv.Atoi(rest[1])
		g, _ := strconv.Atoi(rest[2])
		b, _ := strconv.Atoi(rest[3])
		return fmt.Sprintf("%s=#%02x%02x%02x", layer, r, g, b), 4
	}
	return "", 1
}

func mustInt(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid integer: %s\n", s)
		os.Exit(1)
	}
	return v
}

func printUsage() {
	fmt.Print(`tmux-marquee — scroll text for tmux status bar

Usage: echo "long text" | tmux-marquee [OPTIONS]

Options:
  -w, --width N        Display width in columns (default: 30)
  -i, --id NAME        Instance ID for independent state (default: "default")
  -s, --speed N        Characters to advance per tick (default: 1)
  --separator STR      Text between loop iterations (default: " - ")
  --direction DIR      Scroll direction: left, right, bounce (default: left)
  --pad                Pad short text with trailing spaces (default)
  --no-pad             Don't pad short text
  --scroll-delay N     Wait N ticks before starting scroll (default: 0)
  --max-length N       Truncate input beyond N chars (0 = unlimited)
  --reset              Clear state for this ID and exit
  --help               Show this help
  --version            Show version

Examples:
  # Basic scrolling in tmux status bar
  set -g status-right '#(my-cmd | tmux-marquee -w 30 -i sr)'

  # Use tmux's client width
  set -g status-right '#(my-cmd | tmux-marquee -w #{client_width} -i sr)'

  # Multiple independent marquees
  set -g status-right '#(cmd1 | tmux-marquee -w 20 -i a) #(cmd2 | tmux-marquee -w 20 -i b)'
`)
}
