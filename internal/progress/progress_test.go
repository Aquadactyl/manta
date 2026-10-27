package progress_test

import (
	"bytes"
	"testing"

	"github.com/franela/goblin"

	"github.com/pterodactyl/wings/internal/progress"
)

func TestProgress(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("Progress", func() {
		g.It("properly initializes", func() {
			total := uint64(1000)
			p := progress.NewProgress(total)
			g.Assert(p).IsNotNil()
			g.Assert(p.Total()).Equal(total)
			g.Assert(p.Written()).Equal(uint64(0))
		})

		g.It("increments written when Write is called", func() {
			v := []byte("hello")
			p := progress.NewProgress(1000)
			_, err := p.Write(v)
			g.Assert(err).IsNil()
			g.Assert(p.Written()).Equal(uint64(len(v)))
		})

		g.It("renders a progress bar", func() {
			v := bytes.Repeat([]byte{' '}, 100)
			p := progress.NewProgress(1000)
			_, err := p.Write(v)
			g.Assert(err).IsNil()
			g.Assert(p.Written()).Equal(uint64(len(v)))
			g.Assert(p.Progress(25)).Equal("[==                       ] 100 B / 1000 B")
		})

		g.It("renders a progress bar when written exceeds total", func() {
			v := bytes.Repeat([]byte{' '}, 1001)
			p := progress.NewProgress(1000)
			_, err := p.Write(v)
			g.Assert(err).IsNil()
			g.Assert(p.Written()).Equal(uint64(len(v)))
			g.Assert(p.Progress(25)).Equal("[=========================] 1001 B / 1000 B")
		})

		g.It("updates total when SetTotal is called", func() {
			p := progress.NewProgress(500)
			g.Assert(p.Total()).Equal(uint64(500))
			p.SetTotal(2000)
			g.Assert(p.Total()).Equal(uint64(2000))
		})

		g.It("forwards writes to underlying writer if set", func() {
			var buf bytes.Buffer
			p := progress.NewProgress(100)
			p.Writer = &buf

			n, err := p.Write([]byte("foobar"))
			g.Assert(err).IsNil()
			g.Assert(n).Equal(6)
			g.Assert(buf.String()).Equal("foobar")
			g.Assert(p.Written()).Equal(uint64(6))
		})
		g.It("handles overflow gracefully", func() {
			p := progress.NewProgress(1)
			// simulate huge written
			p.SetTotal(1)
			// We can write to written using Write
			p.Write(make([]byte, 1000))
			g.Assert(p.Progress(25)).Equal("[=========================] 1000 B / 1 B")

			// Overflow float to negative int
			p2 := progress.NewProgress(1)
			p2.SetTotal(1)
			// Set written to huge by calling Write multiple times or with large size
			p2.Write(make([]byte, 1000000000)) // will test ticks
		})
	})
}
