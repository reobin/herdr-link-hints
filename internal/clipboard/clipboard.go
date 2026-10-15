// Package clipboard copies text through OSC 52.
//
// Herdr surfaces a pane child's OSC 52 write as a clipboard write and
// re-emits it to the user's clipboard, so printing the sequence from
// the picker pane is enough to copy.
package clipboard

import (
	"encoding/base64"
	"io"
)

// Sequence formats text as an OSC 52 clipboard write. BEL terminates it
// because some terminals still only honour that form.
func Sequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// Copy writes the OSC 52 sequence for text to w.
func Copy(w io.Writer, text string) error {
	_, err := io.WriteString(w, Sequence(text))
	return err
}
