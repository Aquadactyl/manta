package config

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigureTimezoneFallbacks(t *testing.T) {
	oldDetect, oldRead, oldCommand := detectPlatformTimezoneFn, readTimezoneFile, runTimezoneCommand
	t.Cleanup(func() {
		detectPlatformTimezoneFn = oldDetect
		readTimezoneFile = oldRead
		runTimezoneCommand = oldCommand
	})
	t.Setenv("TZ", "")

	configure := func(timezone string) {
		cfg := &Configuration{}
		cfg.System.Timezone = timezone
		Set(cfg)
		detectPlatformTimezoneFn = func() (string, bool) { return "", false }
		readTimezoneFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
		runTimezoneCommand = func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("timedatectl unavailable")
		}
	}

	t.Run("environment value", func(t *testing.T) {
		configure("")
		t.Setenv("TZ", "America/Chicago")
		require.NoError(t, ConfigureTimezone())
		require.Equal(t, "America/Chicago", Get().System.Timezone)
	})

	t.Run("platform value", func(t *testing.T) {
		configure("")
		detectPlatformTimezoneFn = func() (string, bool) { return "America/Denver", true }
		require.NoError(t, ConfigureTimezone())
		require.Equal(t, "America/Denver", Get().System.Timezone)
	})

	t.Run("timezone file value", func(t *testing.T) {
		configure("")
		readTimezoneFile = func(path string) ([]byte, error) {
			require.Equal(t, "/etc/timezone", path)
			return []byte("UTC\n"), nil
		}
		require.NoError(t, ConfigureTimezone())
		require.Equal(t, "UTC", Get().System.Timezone)
	})

	t.Run("timezone file permission error", func(t *testing.T) {
		configure("")
		want := &os.PathError{Op: "open", Path: "/etc/timezone", Err: os.ErrPermission}
		readTimezoneFile = func(string) ([]byte, error) { return nil, want }
		require.ErrorIs(t, ConfigureTimezone(), os.ErrPermission)
	})

	t.Run("timedatectl failure falls back to utc", func(t *testing.T) {
		configure("")
		require.NoError(t, ConfigureTimezone())
		require.Equal(t, "UTC", Get().System.Timezone)
	})

	t.Run("unparseable timedatectl output falls back to utc", func(t *testing.T) {
		configure("")
		runTimezoneCommand = func(context.Context, string, ...string) ([]byte, error) {
			return []byte("timezone unknown"), nil
		}
		require.NoError(t, ConfigureTimezone())
		require.Equal(t, "UTC", Get().System.Timezone)
	})

	t.Run("timedatectl returns a timezone", func(t *testing.T) {
		configure("")
		runTimezoneCommand = func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Time zone: America/Los_Angeles (PDT, -0700)"), nil
		}
		require.NoError(t, ConfigureTimezone())
		require.Equal(t, "America/Los_Angeles", Get().System.Timezone)
	})

	t.Run("invalid configured timezone", func(t *testing.T) {
		configure("Mars/Nope")
		require.ErrorContains(t, ConfigureTimezone(), "supplied timezone Mars/Nope is invalid")
	})
}
