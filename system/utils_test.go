package system

import (
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/franela/goblin"
)

type errReader struct{}

func (errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func Test_Utils(t *testing.T) {
	g := Goblin(t)

	g.Describe("ScanReader", func() {
		g.BeforeEach(func() {
			maxBufferSize = 10
		})

		g.It("should truncate and return long lines", func() {
			reader := strings.NewReader("hello world this is a long line\nof text that should be truncated\nnot here\nbut definitely on this line")

			var lines []string
			err := ScanReader(reader, func(line []byte) {
				lines = append(lines, string(line))
			})

			g.Assert(err).IsNil()
			g.Assert(lines).Equal([]string{"hello worl", "of text th", "not here", "but defini"})
		})

		g.It("should replace cariage returns with newlines", func() {
			reader := strings.NewReader("test\rstring\r\nanother\rline\nhodor\r\r\rheld the door\nmaterial gourl\n")
			var lines []string
			err := ScanReader(reader, func(line []byte) {
				lines = append(lines, string(line))
			})

			g.Assert(err).IsNil()
			g.Assert(lines).Equal([]string{"test\rstrin", "another\rli", "hodor\r\r\rhe", "material g"})
		})

		g.It("should return error when reader errors with non-EOF", func() {
			err := ScanReader(errReader{}, func(line []byte) {})
			g.Assert(err).IsNotNil()
		})
	})

	g.Describe("AtomicBool", func() {
		var b *AtomicBool
		g.BeforeEach(func() {
			b = NewAtomicBool(false)
		})

		g.It("initalizes with the provided start value", func() {
			b = NewAtomicBool(true)
			g.Assert(b.Load()).IsTrue()

			b = NewAtomicBool(false)
			g.Assert(b.Load()).IsFalse()
		})

		g.Describe("AtomicBool#Store", func() {
			g.It("stores the provided value", func() {
				g.Assert(b.Load()).IsFalse()
				b.Store(true)
				g.Assert(b.Load()).IsTrue()
			})

			// This test makes no assertions, it just expects to not hit a race condition
			// by having multiple things writing at the same time.
			g.It("handles contention from multiple routines", func() {
				var wg sync.WaitGroup

				wg.Add(100)
				for i := 0; i < 100; i++ {
					go func(i int) {
						b.Store(i%2 == 0)
						wg.Done()
					}(i)
				}
				wg.Wait()
			})
		})

		g.Describe("AtomicBool#SwapIf", func() {
			g.It("swaps the value out if different than what is stored", func() {
				o := b.SwapIf(false)
				g.Assert(o).IsFalse()
				g.Assert(b.Load()).IsFalse()

				o = b.SwapIf(true)
				g.Assert(o).IsTrue()
				g.Assert(b.Load()).IsTrue()

				o = b.SwapIf(true)
				g.Assert(o).IsFalse()
				g.Assert(b.Load()).IsTrue()

				o = b.SwapIf(false)
				g.Assert(o).IsTrue()
				g.Assert(b.Load()).IsFalse()
			})
		})

		g.Describe("can be marshaled with JSON", func() {
			type testStruct struct {
				Value AtomicBool `json:"value"`
			}

			var o testStruct
			err := json.Unmarshal([]byte(`{"value":true}`), &o)

			g.Assert(err).IsNil()
			g.Assert(o.Value.Load()).IsTrue()

			b, err2 := json.Marshal(&o)
			g.Assert(err2).IsNil()
			g.Assert(b).Equal([]byte(`{"value":true}`))
		})
	})

	g.Describe("FirstNotEmpty", func() {
		g.It("returns first non-empty string", func() {
			g.Assert(FirstNotEmpty("", "first", "second")).Equal("first")
			g.Assert(FirstNotEmpty("", "", "third")).Equal("third")
			g.Assert(FirstNotEmpty("", "")).Equal("")
		})
	})

	g.Describe("MustInt", func() {
		g.It("parses valid integer", func() {
			g.Assert(MustInt("123")).Equal(123)
			g.Assert(MustInt("-45")).Equal(-45)
		})

		g.It("panics on invalid integer", func() {
			defer func() {
				r := recover()
				g.Assert(r != nil).IsTrue()
			}()
			MustInt("invalid")
		})
	})

	g.Describe("FormatBytes", func() {
		g.It("formats byte sizes properly", func() {
			g.Assert(FormatBytes(500)).Equal("500 B")
			g.Assert(FormatBytes(1024)).Equal("1.0 KiB")
			g.Assert(FormatBytes(1536)).Equal("1.5 KiB")
			g.Assert(FormatBytes(1048576)).Equal("1.0 MiB")
			g.Assert(FormatBytes(1073741824)).Equal("1.0 GiB")
			g.Assert(FormatBytes(1099511627776)).Equal("1.0 TiB")
		})
	})

	g.Describe("AtomicString", func() {
		g.It("handles store, load and JSON serialization", func() {
			s := NewAtomicString("initial")
			g.Assert(s.Load()).Equal("initial")

			s.Store("updated")
			g.Assert(s.Load()).Equal("updated")

			type testStruct struct {
				Val *AtomicString `json:"val"`
			}
			data, err := json.Marshal(&testStruct{Val: s})
			g.Assert(err).IsNil()
			g.Assert(string(data)).Equal(`{"val":"updated"}`)

			var unmarshaled testStruct
			err = json.Unmarshal([]byte(`{"val":"from_json"}`), &unmarshaled)
			g.Assert(err).IsNil()
			g.Assert(unmarshaled.Val.Load()).Equal("from_json")
		})
	})

	g.Describe("Atomic[T]", func() {
		g.It("handles store, load and JSON serialization for generic types", func() {
			a := NewAtomic(42)
			g.Assert(a.Load()).Equal(42)

			a.Store(100)
			g.Assert(a.Load()).Equal(100)

			type testStruct struct {
				Val *Atomic[int] `json:"val"`
			}
			data, err := json.Marshal(&testStruct{Val: a})
			g.Assert(err).IsNil()
			g.Assert(string(data)).Equal(`{"val":100}`)

			var unmarshaled testStruct
			err = json.Unmarshal([]byte(`{"val":999}`), &unmarshaled)
			g.Assert(err).IsNil()
			g.Assert(unmarshaled.Val.Load()).Equal(999)
		})
	})
}

func Benchmark_ScanReader(b *testing.B) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var str string
	for i := 0; i < 10; i++ {
		str += strings.Repeat("hello \rworld", r.Intn(2000)) + "\n"
	}
	reader := strings.NewReader(str)

	b.ResetTimer()
	for b.Loop() {
		_ = ScanReader(reader, func(line []byte) {
			// no op
		})
	}
}
