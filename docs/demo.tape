# The readme gif, as code. `vhs docs/demo.tape` regenerates it.
#
# Needs `pick6 fetch` to have run — the mock draws real players from the cache,
# which is the whole point of it — and a pick6 on PATH. The seed is fixed, so
# re-recording gives you the same draft and the diff is only ever the pixels.
#
# What it shows, in order: the board thinking while a scripted room drafts, the
# banner firing on a real run, `a` to stop the clock, `/` to look somebody up,
# and `tab` to the data table underneath it all.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 20
Set Width 1400
Set Height 920
Set Padding 20
Set TypingSpeed 55ms
Set Framerate 20
# The board's own ink, so the gif and the terminal agree at the edges.
Set Theme { "background": "#1A1B26", "foreground": "#C0CAF5", "cursor": "#7DCFFF" }

# PATH and truecolor, off camera. Lipgloss reads COLORTERM to decide whether it
# can have the palette it was designed in; without it the six position hues
# quantize to 256 and the whole point of matching sleeper is lost.
Hide
Type "export PATH=$HOME/bin:$PATH COLORTERM=truecolor" Enter
Type "clear" Enter
Show

Type "pick6 mock -slot 3 -seed 6"
Enter
Sleep 3s

# The room drafts. Around round 3 a run fires and the ordering re-sorts under it.
Sleep 13s

# Stop the clock and look someone up.
Type "a"
Sleep 1500ms
Type "/"
Sleep 700ms
Type "smith"
Sleep 1800ms
Enter
Sleep 2500ms

# Everything the engine holds, one flat table.
Tab
Sleep 3s
Tab
Sleep 1500ms

Type "q"
Sleep 800ms
