package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// Progress draws a progress bar for a download of a known size:
//
//	[██████████░░░░░░░░░░]  52%  198.3M / 380.1M  3412/6617 files  4.2M/s  0:41 left
//
// It only draws on a terminal, so logs and GitHub runners stay clean. A nil
// *Progress is valid and does nothing.
type Progress struct {
	total, done   int64
	files, nfiles int
	start, drawn  time.Time
	bar           int // width in cells, fixed so the bar doesn't jitter
}

// NewProgress starts a bar for total bytes over n files, or returns nil when
// stdout isn't a terminal.
func NewProgress(total int64, n int) *Progress {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil
	}
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}
	// Room for the longest text it will show, "    [", "] " and the cursor.
	longest := len(fmt.Sprintf("100%%  %s / %s  %d/%d files  999.9M/s  99:59:59 left", Size(total), Size(total), n, n))
	return &Progress{total: total, nfiles: n, start: time.Now(), bar: min(30, width-longest-8)}
}

// Add counts bytes received.
func (p *Progress) Add(n int64) {
	if p == nil {
		return
	}
	p.done += n
	if time.Since(p.drawn) >= 100*time.Millisecond {
		p.draw()
	}
}

// File counts one file finished.
func (p *Progress) File() {
	if p != nil {
		p.files++
	}
}

// Done draws the bar one last time and ends its line.
func (p *Progress) Done() {
	if p == nil {
		return
	}
	p.draw()
	fmt.Println()
}

func (p *Progress) draw() {
	p.drawn = time.Now()
	frac := 1.0
	if p.total > 0 {
		frac = min(float64(p.done)/float64(p.total), 1)
	}
	line := fmt.Sprintf("%4.0f%%  %s / %s  %d/%d files", frac*100, Size(p.done), Size(p.total), p.files, p.nfiles)
	if secs := time.Since(p.start).Seconds(); secs >= 1 && p.done > 0 {
		rate := float64(p.done) / secs
		line += fmt.Sprintf("  %s/s", Size(int64(rate)))
		if left := float64(p.total-p.done) / rate; frac < 1 {
			line += "  " + clock(time.Duration(left)*time.Second) + " left"
		}
	}

	full, empty := "█", "░"
	if runtime.GOOS == "windows" {
		full, empty = "#", "-" // the classic console's code page has no blocks
	}
	out := "    " + line
	if p.bar >= 10 {
		n := int(frac * float64(p.bar))
		out = fmt.Sprintf("    %s[%s%s]%s %s", C, strings.Repeat(full, n), strings.Repeat(empty, p.bar-n), N, line)
	}
	fmt.Printf("\r%s\033[K", out)
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Size is a byte count for people: 512B, 4.2K, 380.1M, 1.3G.
func Size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}
