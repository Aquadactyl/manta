package events

import (
	"testing"
	"time"

	. "github.com/franela/goblin"
)

func TestNewBus(t *testing.T) {
	g := Goblin(t)

	g.Describe("Events", func() {
		var bus *Bus
		g.BeforeEach(func() {
			bus = NewBus()
		})

		g.Describe("NewBus", func() {
			g.It("is not nil", func() {
				g.Assert(bus).IsNotNil("Bus expected to not be nil")
			})
		})

		g.Describe("Publish", func() {
			const topic = "test"
			const message = "this is a test message!"

			g.It("publishes message", func() {
				bus := NewBus()

				listener := make(chan []byte)
				bus.On(listener)

				done := make(chan struct{}, 1)
				go func() {
					select {
					case v := <-listener:
						m := MustDecode(v)
						g.Assert(m.Topic).Equal(topic)
						g.Assert(m.Data).Equal(message)
					case <-time.After(1 * time.Second):
						g.Fail("listener did not receive message in time")
					}
					done <- struct{}{}
				}()
				bus.Publish(topic, message)
				<-done

				// Cleanup
				bus.Off(listener)
			})

			g.It("publishes message to all listeners", func() {
				bus := NewBus()

				listener := make(chan []byte)
				listener2 := make(chan []byte)
				listener3 := make(chan []byte)
				bus.On(listener)
				bus.On(listener2)
				bus.On(listener3)

				done := make(chan struct{}, 1)
				go func() {
					for i := 0; i < 3; i++ {
						select {
						case v := <-listener:
							m := MustDecode(v)
							g.Assert(m.Topic).Equal(topic)
							g.Assert(m.Data).Equal(message)
						case v := <-listener2:
							m := MustDecode(v)
							g.Assert(m.Topic).Equal(topic)
							g.Assert(m.Data).Equal(message)
						case v := <-listener3:
							m := MustDecode(v)
							g.Assert(m.Topic).Equal(topic)
							g.Assert(m.Data).Equal(message)
						case <-time.After(1 * time.Second):
							g.Fail("all listeners did not receive the message in time")
							i = 3
						}
					}

					done <- struct{}{}
				}()
				bus.Publish(topic, message)
				<-done

				// Cleanup
				bus.Off(listener)
				bus.Off(listener2)
				bus.Off(listener3)
			})

			g.It("strips colon suffix from namespaced topic", func() {
				bus := NewBus()
				listener := make(chan []byte, 1)
				bus.On(listener)

				bus.Publish("backup completed:12345", "done")
				v := <-listener
				m := MustDecode(v)
				g.Assert(m.Topic).Equal("backup completed")
				g.Assert(m.Data).Equal("done")
				bus.Off(listener)
			})
		})

		g.Describe("Decode", func() {
			g.It("decodes valid json into custom struct", func() {
				type payload struct {
					Name string `json:"name"`
				}
				data := []byte(`{"name":"test-name"}`)
				var p payload
				err := DecodeTo(data, &p)
				g.Assert(err).IsNil()
				g.Assert(p.Name).Equal("test-name")
			})

			g.It("returns error when DecodeTo encounters invalid json", func() {
				var e Event
				err := DecodeTo([]byte(`not-json`), &e)
				g.Assert(err).IsNotNil()
			})

			g.It("panics when MustDecode encounters invalid json", func() {
				defer func() {
					r := recover()
					g.Assert(r != nil).IsTrue()
				}()
				MustDecode([]byte(`invalid-json`))
			})
		})
	})
}
