package environment

import (
	"sync"
)

type Settings struct {
	Mounts      []Mount
	Allocations Allocations
	Limits      Limits
	Labels      map[string]string
}

// Configuration holds the settings and environment variables for an environment.
// defined within it.
type Configuration struct {
	mu sync.RWMutex

	environmentVariables []string
	settings             Settings
}

// NewConfiguration creates an environment configuration with the given settings and variables.
// defined within it.
func NewConfiguration(s Settings, envVars []string) *Configuration {
	return &Configuration{
		environmentVariables: envVars,
		settings:             s,
	}
}

// SetSettings updates the settings for this environment so modified servers can
// automatically push those changes to the environment.
func (c *Configuration) SetSettings(s Settings) {
	c.mu.Lock()
	c.settings = s
	c.mu.Unlock()
}

// SetEnvironmentVariables replaces this environment’s variables with the provided slice.
// array of them with a new one.
func (c *Configuration) SetEnvironmentVariables(ev []string) {
	c.mu.Lock()
	c.environmentVariables = ev
	c.mu.Unlock()
}

// Limits returns the limits assigned to this environment.
func (c *Configuration) Limits() Limits {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Limits
}

// Allocations returns the allocations associated with this environment.
func (c *Configuration) Allocations() Allocations {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Allocations
}

// Mounts returns all mounts associated with this environment.
func (c *Configuration) Mounts() []Mount {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Mounts
}

// Labels returns the container labels associated with this instance.
func (c *Configuration) Labels() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Labels
}

// EnvironmentVariables returns the variables associated with this instance.
func (c *Configuration) EnvironmentVariables() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.environmentVariables
}
