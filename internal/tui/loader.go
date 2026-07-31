package tui

import (
	"fmt"
	"sync"
	"time"
)

type Loader struct {
	start time.Time

	mu sync.Mutex

	stop chan struct{}
	done bool

	wg sync.WaitGroup
}

func NewLoader() *Loader {
	return &Loader{
		start: time.Now(),
		stop:  make(chan struct{}),
	}
}

func (l *Loader) Start() {
	fmt.Println()
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("Athena")
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("Pipeline")
	fmt.Println()
}

func (l *Loader) Step(name string, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Printf("✓ %-28s %5d ms\n", name, d.Milliseconds())
}

func (l *Loader) Memory(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Printf("    • %s\n", name)
}

func (l *Loader) Waiting() {
	fmt.Println()
	fmt.Println("Model")
	fmt.Println()

	l.wg.Add(1)

	go func() {
		defer l.wg.Done()

		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		i := 0

		for {
			select {

			case <-l.stop:
				fmt.Print("\r\033[2K")
				return

			case <-ticker.C:

				fmt.Printf(
					"\r%s Athena is thinking... %.1fs",
					frames[i],
					time.Since(l.start).Seconds(),
				)

				i = (i + 1) % len(frames)
			}
		}
	}()
}

func (l *Loader) Stop() {
	l.mu.Lock()

	if l.done {
		l.mu.Unlock()
		return
	}

	l.done = true
	close(l.stop)

	l.mu.Unlock()

	l.wg.Wait()

	fmt.Print("\r\033[2K")
}
