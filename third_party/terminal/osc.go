package terminal

import (
	"log"
	"net/url"
)

func (t *Terminal) handleOSC(code string) {
	if len(code) <= 2 || code[1] != ';' {
		return
	}

	switch code[0] {
	case '0':
		// set icon name, if Fyne supports in the future
		t.setTitle(code[2:])
	case '1':
		// set icon name, if Fyne supports in the future
	case '2':
		t.setTitle(code[2:])
	case '7':
		t.setDirectory(code[2:])
	default:
		if t.debug {
			log.Println("Unrecognised OSC:", code)
		}
	}
}

// setDirectory records the directory the shell reports. It used to change
// the whole process's working directory, and read past the end of a short
// URI (1loop, #450); this is upstream's later version.
func (t *Terminal) setDirectory(uri string) {
	t.config.PWD = directoryOf(uri)
	t.onConfigure()
}

// directoryOf returns the path of the file URI OSC 7 reports
// ("file://host/path", the path percent-encoded), or the text as it is when
// it is not one.
func directoryOf(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
		return u.Path
	}
	return uri
}

func (t *Terminal) setTitle(title string) {
	t.config.Title = title
	t.onConfigure()
}
