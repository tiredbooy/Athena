package tui

import (
	"fmt"
	"sync"
	"time"
)

var thinkingTips = []string{
	"checking your notes…",
	"thinking…",
	"putting this together…",
	"still working…",
}

type Loader struct {
	start time.Time

	mu sync.Mutex

	stop  chan struct{}
	done  bool
	phase string // "think" | "reason" | ""

	wg sync.WaitGroup
}

func NewLoader() *Loader {
	return &Loader{
		start: time.Now(),
		stop:  make(chan struct{}),
		phase: "think",
	}
}

func (l *Loader) Start() {
	l.wg.Add(1)
	go l.spin()
}

func (l *Loader) Step(name string, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Kept for optional diagnostics without making ordinary chat noisy.
	_ = name
	_ = d
}

func (l *Loader) Memory(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	_ = name
}

func (l *Loader) Info(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = line
}

// Waiting marks the single activity line as model work. Start already starts
// it so vault search is not a silent pause.
func (l *Loader) Waiting() {
	l.mu.Lock()
	l.phase = "think"
	l.mu.Unlock()
}

func (l *Loader) spin() {
	defer l.wg.Done()

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	i := 0
	tipIdx := 0
	lastTip := time.Now()

	for {
		select {
		case <-l.stop:
			fmt.Print("\r\033[2K")
			return
		case <-ticker.C:
			l.mu.Lock()
			phase := l.phase
			l.mu.Unlock()

			if phase == "" {
				continue
			}

			if time.Since(lastTip) > 4*time.Second {
				tipIdx = (tipIdx + 1) % len(thinkingTips)
				lastTip = time.Now()
			}

			elapsed := time.Since(l.start).Seconds()
			frame := frames[i%len(frames)]
			i++

			var line string
			switch phase {
			case "reason":
				line = fmt.Sprintf("%s Thinking… %.1fs", frame, elapsed)
			case "stream":
				line = fmt.Sprintf("%s Writing… %.1fs", frame, elapsed)
			default:
				tip := thinkingTips[tipIdx]
				line = fmt.Sprintf("%s %s  %.1fs", frame, tip, elapsed)
			}
			fmt.Printf("\r\033[2K%s", line)
		}
	}
}

// NoteReasoning flips the spinner into a "reasoning" state when the model
// emits thinking tokens (no reply text yet).
func (l *Loader) NoteReasoning(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	l.phase = "reason"
	_ = n
}

// TransitionToReply switches the activity message once visible response
// tokens begin arriving. The actual reply is rendered after markdown/action
// cleanup, preventing raw fences and tool JSON from flashing in the UI.
func (l *Loader) TransitionToReply() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.done {
		l.phase = "stream"
	}
}

// NoteStream updates the char counter; used if we ever keep a status line
// during streaming. Currently TransitionToReply stops the spinner first.
func (l *Loader) NoteStream(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = n
	l.phase = "stream"
}

func (l *Loader) Stop() {
	l.mu.Lock()
	if l.done {
		l.mu.Unlock()
		return
	}
	l.done = true
	l.phase = ""
	close(l.stop)
	l.mu.Unlock()

	l.wg.Wait()
	fmt.Print("\r\033[2K")
}
