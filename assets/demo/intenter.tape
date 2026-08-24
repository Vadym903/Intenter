# intenter.tape — VHS script for the README demo.
#
#   make demo        # or: vhs assets/demo/intenter.tape
#
# Run it from the repository root; the paths below are repository-relative.
# The theme is fixed rather than inherited: the GIF is embedded in a README
# that renders on both GitHub themes, so the frame carries its own dark
# background and light text and stays legible either way. The palette is the
# one assets/demo/intenter.svg uses, so the animated and static visuals match.

Output assets/demo/intenter.gif

Set Shell "sh"
# 14px fits the 33-line block explanation, which is the closing frame, into
# 640 pixels. Anything larger scrolls the verdict off the top.
Set FontSize 14
Set FontFamily "JetBrains Mono"
Set Width 1200
Set Height 640
Set Padding 24
Set Margin 0
Set TypingSpeed 30ms
Set PlaybackSpeed 1
Set Framerate 24
Set Theme { "name": "intenter", "background": "#0d1117", "foreground": "#e6edf3", "cursor": "#58a6ff", "selection": "#30363d", "black": "#0d1117", "red": "#f85149", "green": "#3fb950", "yellow": "#d29922", "blue": "#58a6ff", "magenta": "#bc8cff", "cyan": "#39c5cf", "white": "#e6edf3", "brightBlack": "#8b949e", "brightRed": "#ff7b72", "brightGreen": "#56d364", "brightYellow": "#e3b341", "brightBlue": "#79c0ff", "brightMagenta": "#d2a8ff", "brightCyan": "#56d4dd", "brightWhite": "#f0f6fc" }

Hide
Type "clear" Enter
Show

Type "assets/demo/session.sh" Sleep 500ms Enter

# The session paces itself; the recording just has to outlast it. The budget is
# 30 seconds — if this ever waits longer, shorten the session, not the wait.
Wait+Screen@40s /demo complete/
Sleep 2s

Screenshot assets/demo/intenter.png
