# real popup flow in an isolated herdr session, so no live pane or focus is touched.
# go build -o picker .
# env -u HERDR_ENV -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID -u HERDR_SOCKET_PATH vhs docs/demo.tape -o demo.mp4
# herdr session stop demo
# trim the herdr boot and the cat, crop the sidebar, then encode:
# ffmpeg -ss 5.0 -t 4.0 -i demo.mp4 -vf "crop=1266:520:374:0,fps=15,tpad=stop_mode=clone:stop_duration=0.6,split[a][b];[a]palettegen=max_colors=64:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle" docs/demo.gif
# Hide breaks recording in vhs 0.12, hence the trim. vhs cannot send ctrl+space, so the prefix goes as its kitty encoding.
Output docs/demo.gif
Set Shell zsh
Set FontFamily "Geist Mono"
Set FontSize 13
Set Width 1640
Set Height 520
Set Padding 0
Set TypingSpeed 10ms
Set Theme "Catppuccin Mocha"

Sleep 0.5s
Type "herdr --session demo" Enter
Sleep 3s
Type "clear && cat internal/demo/testdata/pane.txt" Enter
Sleep 2s
Escape
Type@1ms "[32;5u"
Type "f"
Sleep 1.2s
Down
Sleep 0.5s
Down
Sleep 0.8s
Escape
Sleep 1.5s
Escape
Type@1ms "[32;5u"
Type "d"
Sleep 0.5s
