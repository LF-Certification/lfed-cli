package iostreams

import (
	"fmt"
	"sync"
	"time"
)

// The 8-dot braille frames fill the whole cell, so they sit centred on
// the text line. The 6-dot set leaves the bottom row empty and looks high.
var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// StartProgress shows a spinner and label on stderr until the returned
// function is called. It writes nothing when stderr is not a terminal, so
// pipes and scripts see only the command's output. The stop function
// erases the line and is safe to call more than once.
func (s *IOStreams) StartProgress(label string) (stop func()) {
	if !s.stderrTTY {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(s.ErrOut, "\r%s %s", spinnerFrames[i%len(spinnerFrames)], label)
			select {
			case <-done:
				fmt.Fprint(s.ErrOut, "\r\x1b[K")
				return
			case <-t.C:
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
}
