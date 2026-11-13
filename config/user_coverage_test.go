//go:build !darwin

package config

import (
	"errors"
	"os/user"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsurePterodactylUserPlatformAndCreationPaths(t *testing.T) {
	oldReadOSRelease := readOSRelease
	oldCurrentUser := currentSystemUser
	oldLookupUser := lookupSystemUser
	oldEnsurePlatformUser := ensurePlatformUserFn
	oldRunCommand := runUserCommand
	t.Cleanup(func() {
		readOSRelease = oldReadOSRelease
		currentSystemUser = oldCurrentUser
		lookupSystemUser = oldLookupUser
		ensurePlatformUserFn = oldEnsurePlatformUser
		runUserCommand = oldRunCommand
	})

	configure := func(systemID string, rootless bool) {
		readOSRelease = func() (map[string]string, error) { return map[string]string{"ID": systemID}, nil }
		currentSystemUser = user.Current
		lookupSystemUser = user.Lookup
		ensurePlatformUserFn = ensurePlatformUser
		runUserCommand = oldRunCommand
		cfg := &Configuration{}
		cfg.System.Username = "coverage-user"
		cfg.System.User.Rootless.Enabled = rootless
		Set(cfg)
	}

	t.Run("platform handled user", func(t *testing.T) {
		configure("linux", false)
		ensurePlatformUserFn = func() (bool, error) { return true, nil }
		require.NoError(t, EnsurePterodactylUser())
	})

	t.Run("platform user error", func(t *testing.T) {
		configure("linux", false)
		want := errors.New("platform user setup failed")
		ensurePlatformUserFn = func() (bool, error) { return false, want }
		require.ErrorIs(t, EnsurePterodactylUser(), want)
	})

	t.Run("distroless uses environment overrides", func(t *testing.T) {
		configure("distroless", false)
		t.Setenv("WINGS_USERNAME", "custom-user")
		t.Setenv("WINGS_UID", "1234")
		t.Setenv("WINGS_GID", "5678")
		require.NoError(t, EnsurePterodactylUser())
		require.Equal(t, "custom-user", Get().System.Username)
		require.Equal(t, 1234, Get().System.User.Uid)
		require.Equal(t, 5678, Get().System.User.Gid)
	})

	t.Run("distroless defaults", func(t *testing.T) {
		configure("distroless", false)
		t.Setenv("WINGS_USERNAME", "")
		t.Setenv("WINGS_UID", "")
		t.Setenv("WINGS_GID", "")
		require.NoError(t, EnsurePterodactylUser())
		require.Equal(t, "pterodactyl", Get().System.Username)
		require.Equal(t, 988, Get().System.User.Uid)
		require.Equal(t, 988, Get().System.User.Gid)
	})

	t.Run("release read error", func(t *testing.T) {
		configure("linux", false)
		want := errors.New("release unavailable")
		readOSRelease = func() (map[string]string, error) { return nil, want }
		require.ErrorIs(t, EnsurePterodactylUser(), want)
		_, err := getSystemName()
		require.ErrorIs(t, err, want)
	})

	t.Run("rootless uses current user", func(t *testing.T) {
		configure("linux", true)
		currentSystemUser = func() (*user.User, error) {
			return &user.User{Username: "current", Uid: "41", Gid: "42"}, nil
		}
		require.NoError(t, EnsurePterodactylUser())
		require.Equal(t, "current", Get().System.Username)
		require.Equal(t, 41, Get().System.User.Uid)
		require.Equal(t, 42, Get().System.User.Gid)
	})

	t.Run("rootless current user error", func(t *testing.T) {
		configure("linux", true)
		want := errors.New("current user unavailable")
		currentSystemUser = func() (*user.User, error) { return nil, want }
		require.ErrorIs(t, EnsurePterodactylUser(), want)
	})

	t.Run("existing user", func(t *testing.T) {
		configure("linux", false)
		lookupSystemUser = func(string) (*user.User, error) {
			return &user.User{Username: "existing", Uid: "51", Gid: "52"}, nil
		}
		require.NoError(t, EnsurePterodactylUser())
		require.Equal(t, 51, Get().System.User.Uid)
		require.Equal(t, 52, Get().System.User.Gid)
	})

	t.Run("lookup returns a non-unknown-user error", func(t *testing.T) {
		configure("linux", false)
		want := errors.New("directory service unavailable")
		lookupSystemUser = func(string) (*user.User, error) { return nil, want }
		require.ErrorIs(t, EnsurePterodactylUser(), want)
	})

	t.Run("useradd failure", func(t *testing.T) {
		configure("linux", false)
		lookupSystemUser = func(string) (*user.User, error) { return nil, user.UnknownUserError("coverage-user") }
		want := errors.New("useradd failed")
		runUserCommand = func(name string, args ...string) ([]byte, error) {
			require.Equal(t, "useradd", name)
			require.Contains(t, args, "coverage-user")
			return nil, want
		}
		require.ErrorIs(t, EnsurePterodactylUser(), want)
	})

	t.Run("alpine addgroup failure", func(t *testing.T) {
		configure("alpine", false)
		lookupSystemUser = func(string) (*user.User, error) { return nil, user.UnknownUserError("coverage-user") }
		want := errors.New("addgroup failed")
		runUserCommand = func(name string, args ...string) ([]byte, error) {
			require.Equal(t, "addgroup", name)
			require.Equal(t, []string{"-S", "coverage-user"}, args)
			return nil, want
		}
		require.ErrorIs(t, EnsurePterodactylUser(), want)
	})

	t.Run("alpine adduser failure", func(t *testing.T) {
		configure("alpine", false)
		lookups := 0
		lookupSystemUser = func(string) (*user.User, error) {
			lookups++
			return nil, user.UnknownUserError("coverage-user")
		}
		commands := 0
		runUserCommand = func(name string, _ ...string) ([]byte, error) {
			commands++
			if name == "addgroup" {
				return nil, nil
			}
			require.Equal(t, "adduser", name)
			return nil, errors.New("adduser failed")
		}
		require.ErrorContains(t, EnsurePterodactylUser(), "adduser failed")
		require.Equal(t, 2, commands)
	})

	t.Run("alpine user is created", func(t *testing.T) {
		configure("alpine", false)
		lookups := 0
		lookupSystemUser = func(string) (*user.User, error) {
			lookups++
			if lookups == 1 {
				return nil, user.UnknownUserError("coverage-user")
			}
			return &user.User{Username: "coverage-user", Uid: "61", Gid: "62"}, nil
		}
		runUserCommand = func(string, ...string) ([]byte, error) { return nil, nil }
		require.NoError(t, EnsurePterodactylUser())
		require.Equal(t, 61, Get().System.User.Uid)
		require.Equal(t, 62, Get().System.User.Gid)
	})

	t.Run("created user lookup failure", func(t *testing.T) {
		configure("linux", false)
		lookups := 0
		lookupSystemUser = func(string) (*user.User, error) {
			lookups++
			if lookups == 1 {
				return nil, user.UnknownUserError("coverage-user")
			}
			return nil, errors.New("new user not visible")
		}
		runUserCommand = func(string, ...string) ([]byte, error) { return nil, nil }
		require.ErrorContains(t, EnsurePterodactylUser(), "new user not visible")
	})
}
