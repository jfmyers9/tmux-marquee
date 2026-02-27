# tmux-marquee

Scroll any text in your tmux status bar. Pipe a command's output
through `tmux-marquee` and it handles the rest — no need to build
marquee logic into every status bar tool.

```
echo "some long status text" | tmux-marquee -w 30 -i mywidget
```

Text shorter than the width passes through unchanged. Scroll
position persists between invocations via a state file keyed by
`--id`, so multiple independent marquees work side by side.

## Install

Build from source (requires Go 1.21+):

```sh
go build -o tmux-marquee .
cp tmux-marquee ~/.local/bin/
```

## Usage

```
echo "long text" | tmux-marquee [OPTIONS]
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-w, --width N` | 30 | Display width in columns |
| `-i, --id NAME` | default | Instance ID for independent state |
| `-s, --speed N` | 1 | Columns to advance per tick |
| `--separator STR` | `" - "` | Text between loop iterations |
| `--direction DIR` | left | `left`, `right`, or `bounce` |
| `--pad` | on | Pad short text with trailing spaces |
| `--no-pad` | | Don't pad |
| `--scroll-delay N` | 0 | Wait N ticks before scrolling |
| `--max-length N` | 0 | Truncate input beyond N chars |
| `--reset` | | Clear state for this ID and exit |

## tmux.conf examples

Basic:

```tmux
set -g status-interval 2
set -g status-right '#(my-cmd | tmux-marquee -w 40 -i sr)'
```

Multiple segments in one marquee:

```tmux
set -g status-right '#(echo "$(cmd1) | $(cmd2)" | tmux-marquee -w 80 -i status) | %H:%M'
```

Dynamic width from tmux:

```tmux
set -g status-right '#(my-cmd | tmux-marquee -w #{client_width} -i sr)'
```

## How it works

Each invocation reads stdin, loads the scroll position from
`$XDG_RUNTIME_DIR/tmux-marquee/<id>` (or `$TMPDIR`), outputs the
visible slice, and saves the next position. tmux's `status-interval`
drives the animation — each tick advances the scroll by `--speed`
columns.

Content changes are detected via checksum. When the input text
changes, the scroll position resets to the beginning.

CJK and emoji characters are handled correctly (counted as 2
display columns) via the `go-runewidth` library.

## License

MIT
