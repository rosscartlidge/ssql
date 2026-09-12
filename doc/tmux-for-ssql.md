# tmux for the ssql Codelab

The [CLI Codelab](cli-codelab.md) asks you to work inside tmux, because
ssql's Alt-h, Alt-g and Alt-r answers open as popups over your command
line there and vanish when you are done. tmux is a terminal multiplexer:
a program that runs a shell inside your terminal window, can draw over
it, and keeps that shell alive if your terminal closes. You need about
five things from it; this page is those five, with the settings that
make it behave like the terminal you already know.

## 1. Install and start

```bash
sudo apt-get install -y tmux        # Debian/Ubuntu (24.04 already has it); macOS: brew install tmux
tmux -V                             # 3.2 or newer for the popups
```

Start a **named** session — the name is how you come back to it:

```bash
tmux new -s ssql
```

Your prompt returns with a green status bar along the bottom showing
`[ssql]`: you are in a bash shell inside tmux. Everything in the codelab
is typed here.

## 2. Make the mouse work like a normal terminal

By default tmux ignores the mouse, so the wheel does nothing and a
drag-select does the terminal's own selection, which cannot see the
scrolled-off lines. Turn the mouse on, once, in `~/.tmux.conf`:

```bash
cat >> ~/.tmux.conf <<'TMUXCONF'
set -g mouse on                  # wheel scrolls, drag selects, click focuses a pane
set -g history-limit 50000       # how many lines the wheel can scroll back through
set -s set-clipboard on          # a drag-select also goes to the system clipboard where the terminal allows it
set -sg escape-time 10           # Alt-h / Alt-g / Alt-r register at once (default 500 ms feels stuck)
TMUXCONF
tmux source-file ~/.tmux.conf    # apply to the running session (new sessions read it anyway)
```

What you get:

- **Scrolling.** The mouse wheel scrolls back through output. So do
  PageUp and PageDown after `Ctrl-b [`. Press `q` to jump back to the
  bottom, or just type.
- **Copying.** Drag to select; releasing the button copies. Paste inside
  tmux with `Ctrl-b ]`. Whether it also lands on your system clipboard
  depends on the terminal: it does in xterm, kitty, WezTerm, iTerm2 and
  most modern terminals; GNOME Terminal and Konsole ignore that request.
  On those, hold **Shift** while dragging — the selection then belongs
  to the terminal, not tmux, and copies as it always did. To make every
  tmux selection reach the clipboard on Linux instead, add one line
  (needs `xclip` on X11; on Wayland use `wl-copy` in its place):

  ```bash
  echo 'bind -T copy-mode MouseDragEnd1Pane send -X copy-pipe-and-cancel "xclip -selection clipboard -in"' >> ~/.tmux.conf
  ```

- **Pasting into tmux** from outside works as usual: middle-click or the
  terminal's paste shortcut.

The `escape-time` line matters for ssql: Alt-h is sent to bash as
Escape then h, and tmux waits `escape-time` milliseconds to decide
whether an Escape starts a key sequence. At the default 500 ms the
popups feel delayed; at 10 ms they are instant.

## 3. Leave and come back

Your session keeps running when you disconnect from it — close the
terminal, lose the SSH connection, come back tomorrow.

```bash
# inside tmux: detach (the session keeps running)
Ctrl-b d

# from any terminal later:
tmux ls                  # what sessions exist
tmux attach -t ssql      # back where you were, output and all
```

`tmux new -A -s ssql` attaches if the session exists and creates it if
not — a good line to alias. To rename a session from inside it,
`Ctrl-b $`. To end it for good, type `exit` in its last shell.

## 4. More than one shell

Handy in the codelab's section 4, where `ssql serve` needs a terminal
of its own:

```
Ctrl-b c        new window (a tab; the status bar numbers them)
Ctrl-b n / p    next / previous window
Ctrl-b %        split the current window side by side
Ctrl-b "        split top and bottom
Ctrl-b arrow    move between panes (or click, with the mouse on)
Ctrl-b x        close the current pane
```

## 5. If something looks wrong

- **The popups print inline instead of floating.** tmux is older than
  3.2 (`tmux -V`), or the shell you are typing in is not inside tmux
  (`echo $TMUX` is empty). Inline is the fallback and works.
- **Alt-h does nothing.** The terminal is keeping Alt for itself: press
  Escape then h, or on macOS enable "Use Option as Meta key" in the
  terminal's keyboard settings.
- **Colours look off** after enabling the mouse. Add
  `set -g default-terminal "tmux-256color"` to `~/.tmux.conf`.
- **A setting did not take.** `~/.tmux.conf` is read when the tmux
  *server* starts; `tmux source-file ~/.tmux.conf` applies it now, or
  `tmux kill-server` and start again.

Back to the [CLI Codelab](cli-codelab.md#1-setup).
