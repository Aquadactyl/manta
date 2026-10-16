package server

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/system"

	"github.com/pterodactyl/wings/environment"
)

type diskSpaceLimiter struct {
	o      sync.Once
	mu     sync.Mutex
	server *Server
}

func newDiskLimiter(s *Server) *diskSpaceLimiter {
	return &diskSpaceLimiter{server: s}
}

// Reset the disk space limiter status.
func (dsl *diskSpaceLimiter) Reset() {
	dsl.mu.Lock()
	dsl.o = sync.Once{}
	dsl.mu.Unlock()
}

// Trigger the disk space limiter which will attempt to stop a running server instance within
// 15 seconds, and terminate it forcefully if it does not stop.
//
// This function is only executed one time, so whenever a server is marked as booting the limiter
// should be reset, so it can properly be triggered as needed.
func (dsl *diskSpaceLimiter) Trigger() {
	dsl.o.Do(func() {
		dsl.server.PublishConsoleOutputFromDaemon("Server is exceeding the assigned disk space limit, stopping process now.")
		if err := dsl.server.Environment.WaitForStop(dsl.server.Context(), time.Minute, true); err != nil {
			dsl.server.Log().WithField("error", err).Error("failed to stop server after exceeding space limit!")
		}
	})
}

// processConsoleOutputEvent handles output from a server's Docker container
// and runs through different limiting logic to ensure that spam console output
// does not cause negative effects to the system. This will also monitor the
// output lines to determine if the server is started yet, and if the output is
// not being throttled, will send the data over to the websocket.
func (s *Server) processConsoleOutputEvent(v []byte) {
	// Always check the console output, including lines that are throttled, so that
	// the line marking the server as started is not missed. Lines are checked in
	// the order they are read.
	s.onConsoleOutput(v)

	// If the console is being throttled, do nothing else with it, we don't want
	// to waste time. This code previously terminated server instances after violating
	// different throttle limits. That code was clunky and difficult to reason about,
	// in addition to being a consistent pain point for users.
	//
	// In the interest of building highly efficient software, that code has been removed
	// here, and we'll rely on the host to detect bad actors through their own means.
	if !s.Throttler().Allow() {
		return
	}

	s.Sink(system.LogSink).Push(v)
}

// StartEventListeners adds all the internal event listeners we want to use for
// a server. These listeners can only be removed by deleting the server as they
// should last for the duration of the process' lifetime.
func (s *Server) StartEventListeners() {
	c := make(chan []byte, 8)
	limit := newDiskLimiter(s)

	s.Log().Debug("registering event listeners: console, state, resources...")
	s.Environment.Events().On(c)
	s.Environment.SetLogCallback(s.processConsoleOutputEvent)

	go func() {
		for {
			select {
			case v := <-c:
				go s.handleEnvironmentEvent(v, limit)
			case <-s.Context().Done():
				return
			}
		}
	}()
}

func (s *Server) handleEnvironmentEvent(v []byte, limit *diskSpaceLimiter) {
	// Inspect the envelope without allocating a generic map/float tree for
	// resource counters that will immediately be decoded into Stats anyway.
	var e struct {
		Topic string
		Data  json.RawMessage
	}
	if err := events.DecodeTo(v, &e); err != nil {
		return
	}
	switch e.Topic {
	case environment.ResourceEvent:
		var stats environment.Stats
		// Missing/null data previously decoded to zero-valued Stats.
		if len(e.Data) > 0 {
			if err := events.DecodeTo(e.Data, &stats); err != nil {
				s.Log().WithField("error", err).Warn("failed to decode server resource event")
				return
			}
		}
		s.resources.UpdateStats(stats)
		// Enforce disk limits independently of connected console viewers.
		if !s.Filesystem().HasSpaceAvailable(true) {
			limit.Trigger()
		}
		bus := s.Events()
		if bus.HasListeners() {
			bus.Publish(StatsEvent, s.Proc())
		}
	case environment.StateChangeEvent:
		starting := bytes.Equal(e.Data, []byte(`"starting"`))
		// Normal state strings need no second decode. Keep JSON's escape
		// handling for equivalent strings such as "\u0073tarting".
		if !starting && bytes.IndexByte(e.Data, '\\') >= 0 {
			var state string
			starting = json.Unmarshal(e.Data, &state) == nil && state == environment.ProcessStartingState
		}
		if starting {
			limit.Reset()
			s.Throttler().Reset()
		}
		s.OnStateChange()
	case environment.DockerImagePullStatus:
		s.Events().Publish(InstallOutputEvent, e.Data)
	case environment.DockerImagePullStarted:
		s.PublishConsoleOutputFromDaemon("Pulling Docker container image, this could take a few minutes to complete...")
	case environment.DockerImagePullCompleted:
		s.PublishConsoleOutputFromDaemon("Finished pulling Docker container image")
	}
}

var stripAnsiRegex = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput(data []byte) {
	// Startup markers are the only console content inspected here. Once running,
	// avoid copying each line or waiting for process-configuration locks.
	if s.Environment.State() != environment.ProcessStartingState {
		return
	}

	processConfiguration := s.ProcessConfiguration()

	// Make a copy of the data provided since it is by reference, otherwise you'll
	// potentially introduce a race condition by modifying the value.
	v := make([]byte, len(data))
	copy(v, data)

	// Check if the server is currently starting.
	if s.Environment.State() == environment.ProcessStartingState {
		// Check if we should strip ansi color codes.
		if processConfiguration.Startup.StripAnsi {
			v = stripAnsiRegex.ReplaceAll(v, []byte(""))
		}

		// Iterate over all the done lines.
		for _, l := range processConfiguration.Startup.Done {
			if !l.Matches(v) {
				continue
			}

			s.Log().WithFields(log.Fields{
				"match":   l.String(),
				"against": strconv.QuoteToASCII(string(v)),
			}).Debug("detected server in running state based on console line output")

			// If the specific line of output is one that would mark the server as started,
			// set the server to that state. Only do this if the server is not currently stopped
			// or stopping.
			s.Environment.SetState(environment.ProcessRunningState)
			break
		}
	}

	// Output from the server process is not used to mark it as stopping or stopped.
	// The environment marks the server as stopping when the stop command is sent to
	// it.
}
